# service-decommissioning Specification

## Purpose

Доменная операция вывода сервиса из эксплуатации (soft delete / decommission) в
каталоге проектов и сквозной сценарий: soft-delete через guarded-CAS
`ACTIVE→DECOMMISSIONED` (данные сохраняются, `decommissioned_at`, идемпотентность),
gRPC `DecommissionService` и Temporal-workflow «Вывод из эксплуатации» (Saga),
который проверяет снятие нагрузки K8s (предусловие), отзывает доступы во внешних
системах (GitLab archive + revoke, Harbor read-only + robot revoke, Vault revoke
SecretID) и переводит статус в каталоге. Источник — docs/IDP_MVP_plan.md (Этап 3,
«Удаление сервиса»), ADR-0003/0004/0005/0008/0010/0012.

## Requirements

### Requirement: Колонка времени вывода из эксплуатации

Каталог ДОЛЖЕН (MUST) хранить отметку времени вывода сервиса из эксплуатации в
nullable-колонке `decommissioned_at` (`timestamptz`) на таблице `services`,
проставляемой при переходе в `decommissioned` и `NULL` для прочих статусов. Схема
ДОЛЖНА (MUST) применяться обратимой миграцией goose (пара `Up`/`Down` в одном
файле, запуск `GOWORK=off`, инструмент пинован в `./tools`); `Down` ДОЛЖЕН (MUST)
полностью снимать колонку.

#### Scenario: Применение и откат миграции decommissioned_at

- **GIVEN** база `postgres-projects` со схемой каталога без `decommissioned_at`
- **WHEN** применяется `goose up`, затем `goose down`
- **THEN** `up` добавляет nullable-колонку `decommissioned_at` на `services`, а
  `down` полностью её снимает без остаточных объектов; повторный `up` идемпотентен

### Requirement: Soft-delete через guarded-CAS ACTIVE→DECOMMISSIONED

Repository/usecase каталога ДОЛЖЕН (MUST) реализовать доменную операцию
soft-delete: перевод сервиса из `active` в `decommissioned` guarded-CAS-запросом
`UPDATE services SET status='decommissioned', decommissioned_at=now(),
updated_at=now() WHERE id=$id AND status='active'`; при `RowsAffected==0` слой НЕ
ДОЛЖЕН (MUST NOT) применять check-then-act, а ДОЛЖЕН (MUST) разобрать актуальный
статус и вернуть: для `decommissioned` — идемпотентный успех (no-op), для
`creating`/`failed` — `errs.ErrPrecondition`, для конкурентной смены статуса —
`errs.ErrConflict`. Данные каталога (строка сервиса и его владельцы) ДОЛЖНЫ (MUST)
СОХРАНЯТЬСЯ — физического удаления нет. Применение к несуществующему сервису
ДОЛЖНО (MUST) давать `errs.ErrNotFound`.

#### Scenario: Успешный перевод активного сервиса в decommissioned

- **GIVEN** сервис со `status=active` и `decommissioned_at IS NULL`
- **WHEN** выполняется soft-delete
- **THEN** `status` становится `decommissioned`, `decommissioned_at` проставляется
  текущим временем, `updated_at` обновляется, строка сервиса и владельцы остаются
  в БД

#### Scenario: Идемпотентный повтор на уже выведенном сервисе

- **GIVEN** сервис со `status=decommissioned`
- **WHEN** выполняется soft-delete повторно
- **THEN** возвращается идемпотентный успех (no-op), `status` остаётся
  `decommissioned`, исходный `decommissioned_at` не перезаписывается

#### Scenario: Недопустимый исходный статус (предусловие)

- **GIVEN** сервис со `status=creating` (или `failed`)
- **WHEN** выполняется soft-delete
- **THEN** возвращается `errs.ErrPrecondition`, статус не меняется, данные не
  трогаются

#### Scenario: Конфликт guarded-CAS при конкурентной смене статуса

- **GIVEN** сервис, чей `status` сменился с `active` конкурентной операцией между
  чтением и CAS
- **WHEN** выполняется guarded-CAS soft-delete
- **THEN** `RowsAffected==0` трактуется как конкурентный конфликт, возвращается
  `errs.ErrConflict`

#### Scenario: Soft-delete несуществующего сервиса

- **GIVEN** в каталоге нет записи `(project=p1, name=missing)`
- **WHEN** выполняется soft-delete для неё
- **THEN** возвращается `errs.ErrNotFound`, записи не создаются

### Requirement: Отражение decommissioned_at в чтении каталога

Сервис `projects` ДОЛЖЕН (MUST) возвращать статус и `decommissioned_at` (для
выведенных сервисов) в `GetService` и `ListServices`. Для не выведенных сервисов
`decommissioned_at` ДОЛЖЕН (MUST) отсутствовать/быть пустым. Отражение НЕ ДОЛЖНО
(MUST NOT) ломать keyset-пагинацию листинга.

#### Scenario: GetService возвращает статус и время вывода

- **GIVEN** сервис `(p1, svc)` со `status=decommissioned` и проставленным
  `decommissioned_at`
- **WHEN** вызывается `GetService(p1, svc)`
- **THEN** ответ содержит `status=DECOMMISSIONED` и `decommissioned_at`

#### Scenario: ListServices включает статус каждой записи

- **GIVEN** в проекте есть активные и выведенные сервисы
- **WHEN** вызывается `ListServices` с keyset-пагинацией
- **THEN** каждая запись содержит свой `status` (и `decommissioned_at` для
  выведенных), порядок строк сохраняется по `(created_at, id)`, без дублей и
  пропусков

### Requirement: gRPC DecommissionService

Сервис `projects` ДОЛЖЕН (MUST) реализовать gRPC
`DecommissionService(project, name, load_drained)`: валидировать запрос
(`project`/`name` обязательны), проверять право через IDM (см. требование
авторизации), читать запись каталога и запускать Temporal-workflow «Вывод из
эксплуатации» с детерминированным `WorkflowID` ТОЛЬКО для сервиса в статусе
`active`. При отсутствии записи ДОЛЖЕН (MUST) возвращаться `codes.NotFound`; при
уже выведенном сервисе — идемпотентный успех (workflow не стартует); при статусе
`creating`/`failed` или неподтверждённом предусловии снятой нагрузки —
`codes.FailedPrecondition`; при конкурентном конфликте — `codes.Aborted`; при
невалидном запросе — `codes.InvalidArgument`. Внутренние ошибки НЕ ДОЛЖНЫ (MUST
NOT) раскрываться клиенту (`err.Error()` наружу не отдаётся; детали — в лог по
ключу slog `err`). Семантика повторного вызова идемпотентна.

#### Scenario: Запуск вывода из эксплуатации

- **GIVEN** сервис `(p1, svc)` со `status=active`, субъект имеет право
  `decommission`, нагрузка снята (`load_drained=true`)
- **WHEN** вызывается `DecommissionService(p1, svc, load_drained=true)`
- **THEN** стартует Temporal-workflow «Вывод из эксплуатации» с детерминированным
  `WorkflowID`, клиенту возвращается подтверждение без раскрытия деталей

#### Scenario: Идемпотентный повтор на выведенном сервисе

- **GIVEN** сервис `(p1, svc)` со `status=decommissioned`
- **WHEN** вызывается `DecommissionService(p1, svc, ...)`
- **THEN** возвращается успех с итоговым состоянием, новый workflow не стартует

#### Scenario: Предусловие не выполнено (нагрузка не снята)

- **GIVEN** сервис `(p1, svc)` со `status=active`, но `load_drained=false`
- **WHEN** вызывается `DecommissionService(p1, svc, load_drained=false)`
- **THEN** возвращается `codes.FailedPrecondition`, workflow не стартует, побочных
  эффектов нет

#### Scenario: Недопустимый исходный статус

- **GIVEN** сервис `(p1, svc)` со `status=creating`
- **WHEN** вызывается `DecommissionService(p1, svc, ...)`
- **THEN** возвращается `codes.FailedPrecondition`, workflow не стартует

#### Scenario: Отсутствующий сервис

- **GIVEN** записи `(p1, missing)` нет в каталоге
- **WHEN** вызывается `DecommissionService(p1, missing, ...)`
- **THEN** возвращается `codes.NotFound`, workflow не стартует

### Requirement: Авторизация операции вывода из эксплуатации (fail-closed)

Вывод из эксплуатации ДОЛЖЕН (MUST) быть защищён RBAC-операцией `decommission` на
ресурсе `project:<project>`. Сервис `projects` ДОЛЖЕН (MUST) вызывать IDM
`CheckAccess` ПЕРЕД доменной операцией и запуском workflow (defense-in-depth).
Отказ политики, недоступность или ошибка IDM ДОЛЖНЫ (MUST) приводить к
`codes.PermissionDenied` (fail-closed), без побочных эффектов и без раскрытия
внутренних деталей; `subject` берётся из claims контекста.

#### Scenario: Право есть — операция допускается

- **GIVEN** субъект `demo-user` имеет право `(decommission, project:demo)`
- **WHEN** вызывается `DecommissionService(demo, svc, ...)`
- **THEN** IDM возвращает `allowed=true`, и операция продолжается

#### Scenario: Права нет — отказ

- **GIVEN** субъект без права `(decommission, project:demo)`
- **WHEN** вызывается `DecommissionService(demo, svc, ...)`
- **THEN** возвращается `codes.PermissionDenied`, статус не меняется, workflow не
  стартует

#### Scenario: IDM недоступен — fail-closed

- **GIVEN** IDM недоступен (ошибка вызова `CheckAccess`)
- **WHEN** вызывается `DecommissionService`
- **THEN** возвращается `codes.PermissionDenied`, деталь ошибки уходит только в
  лог по ключу slog `err`, побочных эффектов нет

### Requirement: Предварительная проверка снятой нагрузки K8s

Workflow «Вывод из эксплуатации» ДОЛЖЕН (MUST) выполнять предварительный шаг
проверки снятой нагрузки K8s ДО любых отзывов доступа, через интерфейс
`LoadChecker` (граница под будущий K8s-worker). В MVP, при отсутствии K8s-worker,
проверка ДОЛЖНА (MUST) опираться на явное предусловие (флаг `load_drained`,
переданный вызывающей стороной), а НЕ на имитацию запроса к несуществующему
кластеру. Невыполненное предусловие ДОЛЖНО (MUST) приводить к завершению с ошибкой
предусловия (`FailedPrecondition`) ДО любых побочных эффектов. Граница `LoadChecker`
ДОЛЖНА (MUST) позволять будущему K8s-worker подменить реализацию реальным запросом
к кластеру без изменения тела workflow и контракта.

#### Scenario: Предусловие выполнено — продолжаем

- **GIVEN** запрос с подтверждённой снятой нагрузкой (`load_drained=true`)
- **WHEN** workflow выполняет шаг `EnsureLoadDrained`
- **THEN** проверка проходит, и workflow продолжает к отзыву доступов

#### Scenario: Предусловие не выполнено — отказ до побочных эффектов

- **GIVEN** запрос без подтверждения снятой нагрузки (`load_drained=false`)
- **WHEN** workflow выполняет шаг `EnsureLoadDrained`
- **THEN** workflow завершается ошибкой предусловия, ни одного отзыва доступа
  (GitLab/Harbor/Vault) и изменения каталога не произведено

### Requirement: Workflow «Вывод из эксплуатации» (Saga)

Сервис `projects` ДОЛЖЕН (MUST) определять отдельный публичный контракт workflow
«Вывод из эксплуатации» (имя workflow, имена activities, детерминированный
`WorkflowID` на пару `(project, name)`), исполняемый DevInfra worker'ом. Тело
workflow ДОЛЖНО (MUST) быть детерминированным (весь I/O — в activities с
RetryPolicy/таймаутами/heartbeat). Порядок шагов: (0) предусловие снятой нагрузки
K8s; (1) GitLab archive + отзыв доступов; (2) Harbor → read-only + отзыв Robot;
(3) Vault → отзыв активных SecretID/токенов — **ТОЧКА НЕВОЗВРАТА** (необратимый
отзыв доступа); (4) guarded-CAS `ACTIVE→DECOMMISSIONED` в каталоге. Окончательный
(non-retryable) сбой ДО точки невозврата ДОЛЖЕН (MUST) запускать идемпотентные
компенсации в обратном порядке (Harbor → writable, GitLab → unarchive), каталог не
трогать. Сбой ПОСЛЕ точки невозврата (включая конфликт guarded-CAS на шаге 4) НЕ
ДОЛЖЕН (MUST NOT) приводить к молчаливому откату: workflow ДОЛЖЕН (MUST) форвард-
only ретраить идемпотентные шаги, а при исчерпании — фиксировать алерт оператору
структурным логом (ADR-0005/0008). Каталог = целевой источник правды.

#### Scenario: Happy-path вывода из эксплуатации

- **GIVEN** сервис `(p1, svc)` со `status=active` и подтверждённой снятой нагрузкой
- **WHEN** выполняется workflow «Вывод из эксплуатации»
- **THEN** последовательно: предусловие пройдено, GitLab архивирован и доступы
  отозваны, Harbor переведён в read-only и Robot отозван, активные SecretID/токены
  Vault отозваны, статус каталога переведён guarded-CAS в `decommissioned` с
  `decommissioned_at`, workflow завершается успешно

#### Scenario: Компенсация при сбое до точки невозврата

- **GIVEN** GitLab уже архивирован, а перевод Harbor в read-only завершился
  non-retryable-ошибкой (до отзыва Vault)
- **WHEN** workflow обрабатывает сбой
- **THEN** запускаются идемпотентные компенсации в обратном порядке (GitLab →
  unarchive), статус каталога НЕ меняется, workflow завершается ошибкой без
  частичных необратимых отзывов

#### Scenario: Алерт после точки невозврата

- **GIVEN** активные SecretID/токены Vault уже отозваны (точка невозврата
  пройдена), а guarded-CAS-перевод каталога окончательно недоступен или конфликтует
- **WHEN** workflow обрабатывает сбой
- **THEN** молчаливого отката НЕТ; workflow форвард-only ретраит идемпотентный шаг
  каталога, а при исчерпании фиксирует алерт оператору структурным логом (доступ
  остаётся отозванным)

#### Scenario: Конфликт guarded-CAS на переводе каталога

- **GIVEN** отзывы доступа выполнены, но `status` сервиса сменился конкурентной
  операцией
- **WHEN** workflow выполняет guarded-CAS-перевод каталога
- **THEN** перевод возвращает `errs.ErrConflict`; поскольку точка невозврата
  пройдена, фиксируется алерт оператору (без возврата доступа)

#### Scenario: Идемпотентный повтор workflow

- **GIVEN** workflow «Вывод из эксплуатации» уже выполнялся для `(p1, svc)`
- **WHEN** он запускается повторно с тем же детерминированным `WorkflowID`
- **THEN** идемпотентные шаги (archive/read-only/revoke/CAS) не приводят к
  повторным побочным эффектам и не ломают итоговое состояние

### Requirement: Тестовое покрытие вывода из эксплуатации

Тесты ДОЛЖНЫ (MUST) быть table-driven с `t.Parallel()`; пакеты с горутинами — под
`goleak`. Workflow ДОЛЖЕН (MUST) покрываться Temporal testsuite с замоканными
activities: happy-path, предусловие «нагрузка не снята» (отказ до любых побочных
эффектов), ветка компенсации (сбой до точки невозврата), алерт после точки
невозврата и конфликт guarded-CAS. Repository soft-delete ДОЛЖЕН (MUST)
тестироваться стабом/in-memory в дефолтном прогоне и pgx против тест-БД под тегом
`integration` (DSN из `*_TEST_DSN`, `Skip` без БД).

#### Scenario: Дефолтный прогон без БД и сети

- **WHEN** выполняется `go test ./...` без тега `integration`
- **THEN** проходят тесты workflow (testsuite с моками: happy, предусловие,
  компенсация, алерт, конфликт) и доменные тесты soft-delete (guarded-CAS,
  идемпотентность, предусловие) на стабе/in-memory без Postgres и сети

#### Scenario: Integration-тесты soft-delete под тегом

- **WHEN** выполняется прогон с тегом `integration` при доступной БД
- **THEN** запускаются тесты repository soft-delete против реального Postgres
  (guarded-CAS `ACTIVE→DECOMMISSIONED`, сохранность данных/владельцев,
  `decommissioned_at`)
