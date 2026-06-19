# service-ownership Specification

## Purpose

Модель владельцев сервиса в каталоге проектов и сквозной сценарий «Изменение
владельцев»: схема владельцев в Postgres, repository/usecase с guarded-CAS на
конкурентные изменения, gRPC `SetServiceOwners` (декларативная замена набора,
идемпотентно) и Temporal-workflow «Изменение владельцев» (Saga), который
синхронизирует участников GitLab, политики Vault (моки), записывает owners в
каталог, синхронизирует роли в IDM и инвалидирует кэш решений IDM. Источник —
docs/IDP_MVP_plan.md (Этап 3, «Изменение владельцев»), ADR-0004/0005/0008/0010/0011.

## ADDED Requirements

### Requirement: Схема владельцев сервиса в Postgres

Каталог ДОЛЖЕН (MUST) хранить владельцев сервиса в отдельной таблице
`service_owners` со связью `service_id → services.id` (внешний ключ с каскадным
удалением) и уникальностью пары `(service_id, owner)` на уровне БД. На таблицу
`services` ДОЛЖНА (MUST) добавляться целочисленная колонка версии владельцев
`owners_version` (NOT NULL, начальное значение 0) для optimistic-concurrency.
Схема ДОЛЖНА (MUST) применяться обратимой миграцией goose (пара `Up`/`Down` в
одном файле, запуск `GOWORK=off`, инструмент пинован в `./tools`); `Down`
ДОЛЖЕН (MUST) полностью снимать введённые объекты.

#### Scenario: Применение и откат миграции владельцев

- **GIVEN** база `postgres-projects` со схемой каталога без владельцев
- **WHEN** применяется `goose up`, затем `goose down`
- **THEN** `up` создаёт таблицу `service_owners` (FK на `services`, уникальность
  `(service_id, owner)`) и колонку `owners_version` на `services`, а `down`
  полностью снимает их без остаточных объектов; повторный `up` идемпотентен

#### Scenario: Дубликат владельца отклоняется БД

- **GIVEN** у сервиса уже есть владелец `(service_id=s1, owner=alice)`
- **WHEN** предпринимается вставка такой же пары `(s1, alice)`
- **THEN** БД отклоняет дубликат по уникальному ограничению, без молчаливого
  дублирования

### Requirement: Декларативная замена владельцев через guarded-CAS

Repository/usecase каталога ДОЛЖЕН (MUST) реализовать декларативную замену
набора владельцев сервиса: на вход подаётся желаемый полный набор владельцев,
изменения `service_owners` (вставки добавленных, удаления отозванных) и
инкремент `owners_version` ДОЛЖНЫ (MUST) выполняться в одной транзакции.
Конкурентные изменения ДОЛЖНЫ (MUST) защищаться guarded-CAS по версии:
`UPDATE services SET owners_version=owners_version+1, updated_at=now()
WHERE id=$id AND owners_version=$expected`; при `RowsAffected==0` слой ДОЛЖЕН
(MUST) возвращать `errs.ErrConflict` и НЕ ДОЛЖЕН (MUST NOT) применять
check-then-act. Операция ДОЛЖНА (MUST) быть идемпотентной: повторная замена тем
же набором при актуальной версии не изменяет состав владельцев (но может
сдвинуть версию). Применение к несуществующему сервису ДОЛЖНО (MUST) давать
`errs.ErrNotFound`.

#### Scenario: Успешная замена набора владельцев

- **GIVEN** сервис со `status=ACTIVE`, владельцами `{alice}` и `owners_version=3`
- **WHEN** выполняется замена набором `{alice, bob}` с `expected_version=3`
- **THEN** в транзакции добавляется `bob`, `owners_version` становится 4,
  `updated_at` обновляется, возвращается итоговый набор `{alice, bob}`

#### Scenario: Конфликт версии (конкурентное изменение)

- **GIVEN** сервис с `owners_version=4`
- **WHEN** выполняется замена с устаревшим `expected_version=3`
- **THEN** `RowsAffected==0`, состав владельцев не меняется, возвращается
  `errs.ErrConflict`

#### Scenario: Идемпотентная повторная замена тем же набором

- **GIVEN** сервис с владельцами `{alice, bob}` и `owners_version=4`
- **WHEN** выполняется замена тем же набором `{alice, bob}` с `expected_version=4`
- **THEN** состав владельцев остаётся `{alice, bob}`, без дублей и потерь

#### Scenario: Замена владельцев несуществующего сервиса

- **GIVEN** в каталоге нет записи `(project=p1, name=missing)`
- **WHEN** выполняется замена владельцев для неё
- **THEN** возвращается `errs.ErrNotFound`, записи не создаются

### Requirement: Отражение владельцев в чтении каталога

Сервис `projects` ДОЛЖЕН (MUST) возвращать актуальный набор владельцев и версию
владельцев в `GetService` и `ListServices` (поле `owners` и сопутствующая версия
для optimistic-concurrency). Порядок владельцев в ответе ДОЛЖЕН (MUST) быть
детерминированным (например, лексикографическим), чтобы сравнения и тесты были
стабильны. Чтение владельцев для листинга НЕ ДОЛЖНО (MUST NOT) приводить к
N+1-эффекту, ломающему keyset-пагинацию.

#### Scenario: GetService возвращает владельцев и версию

- **GIVEN** сервис `(p1, svc)` с владельцами `{alice, bob}` и `owners_version=4`
- **WHEN** вызывается `GetService(p1, svc)`
- **THEN** ответ содержит `owners=[alice, bob]` (в детерминированном порядке) и
  версию владельцев `4`

#### Scenario: ListServices включает владельцев каждой записи

- **GIVEN** в проекте несколько сервисов с разными владельцами
- **WHEN** вызывается `ListServices` с keyset-пагинацией
- **THEN** каждая запись страницы содержит свой набор `owners`, порядок строк
  сохраняется по `(created_at, id)`, без дублей и пропусков

### Requirement: gRPC SetServiceOwners

Сервис `projects` ДОЛЖЕН (MUST) реализовать gRPC `SetServiceOwners(project,
name, owners, expected_version)`: валидировать запрос (`project`/`name`
обязательны; набор владельцев нормализуется — без пустых строк и дублей),
проверять право через IDM (см. требование авторизации) и запускать
Temporal-workflow «Изменение владельцев» с детерминированным `WorkflowID`. При
отсутствии записи каталога ДОЛЖЕН (MUST) возвращаться `codes.NotFound`, при
конфликте версии — `codes.FailedPrecondition`, при невалидном запросе —
`codes.InvalidArgument`. Внутренние ошибки НЕ ДОЛЖНЫ (MUST NOT) раскрываться
клиенту (`err.Error()` наружу не отдаётся; детали — в лог по ключу slog `err`).

#### Scenario: Запуск изменения владельцев

- **GIVEN** сервис `(p1, svc)` существует и субъект имеет право `change_owners`
- **WHEN** вызывается `SetServiceOwners(p1, svc, owners=[alice, bob], expected_version=4)`
- **THEN** стартует Temporal-workflow «Изменение владельцев» с детерминированным
  `WorkflowID`, клиенту возвращается подтверждение запуска без раскрытия деталей

#### Scenario: Невалидный набор владельцев

- **WHEN** вызывается `SetServiceOwners` с пустым `project`/`name` или набором,
  содержащим пустые строки
- **THEN** возвращается `codes.InvalidArgument`, workflow не стартует

#### Scenario: Отсутствующий сервис

- **GIVEN** записи `(p1, missing)` нет в каталоге
- **WHEN** вызывается `SetServiceOwners(p1, missing, ...)`
- **THEN** возвращается `codes.NotFound`, workflow не стартует

### Requirement: Авторизация операции изменения владельцев (fail-closed)

Изменение владельцев ДОЛЖНО (MUST) быть защищено RBAC-операцией `change_owners`
на ресурсе `project:<project>`. Сервис `projects` ДОЛЖЕН (MUST) вызывать IDM
`CheckAccess` ПЕРЕД доменной операцией и запуском workflow (defense-in-depth).
Отказ политики, недоступность или ошибка IDM ДОЛЖНЫ (MUST) приводить к
`codes.PermissionDenied` (fail-closed), без побочных эффектов и без раскрытия
внутренних деталей; `subject` берётся из claims контекста.

#### Scenario: Право есть — операция допускается

- **GIVEN** субъект `demo-user` имеет право `(change_owners, project:demo)`
- **WHEN** вызывается `SetServiceOwners(demo, svc, ...)`
- **THEN** IDM возвращает `allowed=true`, и операция продолжается

#### Scenario: Права нет — отказ

- **GIVEN** субъект без права `(change_owners, project:demo)`
- **WHEN** вызывается `SetServiceOwners(demo, svc, ...)`
- **THEN** возвращается `codes.PermissionDenied`, владельцы не меняются, workflow
  не стартует

#### Scenario: IDM недоступен — fail-closed

- **GIVEN** IDM недоступен (ошибка вызова `CheckAccess`)
- **WHEN** вызывается `SetServiceOwners`
- **THEN** возвращается `codes.PermissionDenied`, деталь ошибки уходит только в
  лог по ключу slog `err`, побочных эффектов нет

### Requirement: Workflow «Изменение владельцев» (Saga)

Сервис `projects` ДОЛЖЕН (MUST) определять отдельный от провижна публичный
контракт workflow «Изменение владельцев» (имя workflow, имена activities,
детерминированный `WorkflowID` на пару `(project, name)`), исполняемый DevInfra
worker'ом. Тело workflow ДОЛЖНО (MUST) быть детерминированным (весь I/O — в
activities с RetryPolicy/таймаутами/heartbeat). Порядок шагов: (1) синхронизация
участников GitLab по diff add/remove; (2) синхронизация политик Vault; (3)
guarded-CAS-запись owners в каталог (ТОЧКА НЕВОЗВРАТА); (4) синхронизация ролей
IDM (выдать роль добавленным, отозвать у удалённых); (5) инвалидация кэша
решений IDM по затронутым субъектам. Окончательный (non-retryable) сбой ДО точки
невозврата ДОЛЖЕН (MUST) запускать идемпотентные компенсации в обратном порядке
(восстановление прежнего состава участников GitLab/политик Vault). Сбой ПОСЛЕ
точки невозврата НЕ ДОЛЖЕН (MUST NOT) приводить к молчаливому откату: workflow
ДОЛЖЕН (MUST) ретраить идемпотентные шаги IDM, а при исчерпании — фиксировать
алерт оператору структурным логом (ADR-0005/0008). При пустом diff (нет
добавленных и удалённых) workflow ДОЛЖЕН (MUST) завершаться как no-op без
обращения к интеграциям.

#### Scenario: Happy-path смены владельцев

- **GIVEN** сервис `(p1, svc)` с владельцами `{alice}` и валидный desired-набор
  `{alice, bob}` (diff: add=`{bob}`, remove=`{}`)
- **WHEN** выполняется workflow «Изменение владельцев»
- **THEN** последовательно: участники GitLab синхронизированы, политики Vault
  обновлены, owners в каталоге переведены guarded-CAS к `{alice, bob}`, в IDM
  выдана роль `bob`, кэш решений IDM по `bob` инвалидирован, workflow завершается
  успешно

#### Scenario: Компенсация при сбое до точки невозврата

- **GIVEN** синхронизация участников GitLab выполнена, а синхронизация политик
  Vault завершилась non-retryable-ошибкой
- **WHEN** workflow обрабатывает сбой
- **THEN** запускаются идемпотентные компенсации в обратном порядке
  (восстановление прежнего состава участников GitLab), owners в каталоге НЕ
  меняются, workflow завершается ошибкой без частичных изменений

#### Scenario: Конфликт guarded-CAS на записи owners

- **GIVEN** participants GitLab и политики Vault уже синхронизированы, но
  `owners_version` в каталоге сменилась конкурентной операцией
- **WHEN** workflow выполняет guarded-CAS-запись owners
- **THEN** запись возвращает `errs.ErrConflict` (до точки невозврата), запускаются
  компенсации GitLab/Vault, workflow завершается ошибкой конфликта

#### Scenario: Сбой IDM-синхронизации после точки невозврата

- **GIVEN** owners уже зафиксированы в каталоге (точка невозврата пройдена), а
  синхронизация ролей/инвалидация кэша IDM окончательно недоступна
- **WHEN** workflow обрабатывает сбой
- **THEN** молчаливого отката НЕТ; workflow ретраит идемпотентные шаги IDM, а при
  исчерпании фиксирует алерт оператору структурным логом (каталог остаётся
  источником правды)

#### Scenario: Идемпотентный no-op при пустом diff

- **GIVEN** desired-набор владельцев совпадает с текущим
- **WHEN** выполняется workflow «Изменение владельцев»
- **THEN** workflow завершается успешно как no-op, без обращения к GitLab/Vault/
  IDM и без изменения каталога

### Requirement: Тестовое покрытие изменения владельцев

Тесты ДОЛЖНЫ (MUST) быть table-driven с `t.Parallel()`; пакеты с горутинами —
под `goleak`. Workflow ДОЛЖЕН (MUST) покрываться Temporal testsuite с
замоканными activities: happy-path, ветка компенсации (сбой до точки невозврата),
конфликт guarded-CAS и сбой IDM после точки невозврата. Repository владельцев
ДОЛЖЕН (MUST) тестироваться стабом/in-memory в дефолтном прогоне и pgx против
тест-БД под тегом `integration` (DSN из `*_TEST_DSN`, `Skip` без БД).

#### Scenario: Дефолтный прогон без БД и сети

- **WHEN** выполняется `go test ./...` без тега `integration`
- **THEN** проходят тесты workflow (testsuite с моками) и доменные тесты
  (guarded-CAS-конфликт, идемпотентность) на стабе/in-memory без Postgres и сети

#### Scenario: Integration-тесты владельцев под тегом

- **WHEN** выполняется прогон с тегом `integration` при доступной БД
- **THEN** запускаются тесты repository владельцев против реального Postgres
  (guarded-CAS по версии, уникальность `(service_id, owner)`, каскад FK)
