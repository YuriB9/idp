# service-transfer Specification

## Purpose

Доменная операция переноса сервиса в другой проект (transfer) в каталоге проектов
и сквозной сценарий: смена колонки `project` `source→target` через guarded-CAS с
проверкой свободы `(target_project, name)` (id записи и владельцы сохраняются,
транзитный статус `transferring`, идемпотентность), gRPC `TransferService` и
Temporal-workflow «Перенос» (Saga) с ТОЧКОЙ НЕВОЗВРАТА на transfer GitLab, который
переносит репозиторий GitLab в новую группу, мигрирует пути/политики Vault,
обновляет метаданные/права Harbor, фиксирует каталог и переносит роли владельцев в
IDM. Источник — docs/IDP_MVP_plan.md (Этап 3, «Перенос сервиса»),
ADR-0003/0004/0005/0008/0010/0011/0013.

## Requirements

### Requirement: Транзитный статус переноса в каталоге

Каталог ДОЛЖЕН (MUST) поддерживать транзитный статус `transferring` для сервиса на
время выполнения переноса. Значение `transferring` ДОЛЖНО (MUST) добавляться в
ограничение `CHECK status IN (...)` таблицы `services` обратимой миграцией goose
(пара `Up`/`Down` в одном файле, запуск `GOWORK=off`, инструмент пинован в
`./tools`); `Down` ДОЛЖЕН (MUST) полностью снимать `transferring` из ограничения.
Статус `transferring` ДОЛЖЕН (MUST) защищать сервис от конкурентных операций:
любая guarded-CAS-операция, требующая `status='active'` (повторный перенос,
вывод из эксплуатации, изменение владельцев), на сервисе в `transferring` ДОЛЖНА
(MUST) получать отказ-предусловие/конфликт.

#### Scenario: Применение и откат миграции transferring

- **GIVEN** база `postgres-projects` со схемой каталога без `transferring` в CHECK
- **WHEN** применяется `goose up`, затем `goose down`
- **THEN** `up` расширяет `CHECK status IN (...)` значением `transferring`, а
  `down` полностью его снимает; повторный `up` идемпотентен; существующие строки не
  затрагиваются

#### Scenario: Защита от конкурентных операций в transferring

- **GIVEN** сервис со `status=transferring`
- **WHEN** к нему применяется конкурентный перенос/вывод из эксплуатации/изменение
  владельцев (guarded-CAS требует `status='active'`)
- **THEN** операция получает `errs.ErrPrecondition`/`errs.ErrConflict`, статус не
  меняется, побочных эффектов нет

### Requirement: Смена проекта-владельца через guarded-CAS (две фазы)

Repository/usecase каталога ДОЛЖЕН (MUST) реализовать доменную операцию переноса в
ДВЕ guarded-CAS-фазы с сохранением id записи и владельцев. id записи каталога
ДОЛЖЕН (MUST) СОХРАНЯТЬСЯ; владельцы (`service_owners`, FK `service_id`) ДОЛЖНЫ
(MUST) переезжать вместе с записью без переписывания строк владельцев.
`decommissioned_at` ДОЛЖЕН (MUST) оставаться `NULL`.

Фаза начала ДОЛЖНА (MUST) в одной транзакции проверять свободу `(target_project,
name)` (занято → `errs.ErrConflict`) и выполнять guarded-CAS `UPDATE services SET
status='transferring' WHERE id=$id AND status='active'`; при `RowsAffected==0` слой
НЕ ДОЛЖЕН (MUST NOT) применять check-then-act, а ДОЛЖЕН (MUST) разобрать актуальный
статус: `transferring` → перенос уже идёт; `creating`/`failed`/`decommissioned` →
`errs.ErrPrecondition`; конкурентная смена → `errs.ErrConflict`.

Фаза фиксации ДОЛЖНА (MUST) выполнять guarded-CAS `UPDATE services SET
project=$target, status='active' WHERE id=$id AND status='transferring'` (с
повторной проверкой свободы `(target_project, name)`); `RowsAffected==0` →
`errs.ErrConflict`. Фаза фиксации ДОЛЖНА (MUST) быть идемпотентной: повтор на уже
перенесённой записи (`project=target`, `status=active`) → успех (no-op).

Компенсация начала ДОЛЖНА (MUST) выполнять guarded-CAS `transferring→active`
(`project` не менялся).

#### Scenario: Успешное начало переноса (active→transferring)

- **GIVEN** сервис `(source, svc)` со `status=active` и свободной парой
  `(target, svc)`
- **WHEN** выполняется фаза начала переноса в target
- **THEN** `status` становится `transferring`, `project` остаётся `source`, id и
  владельцы не меняются

#### Scenario: Занятое имя в target → конфликт (до side-effect)

- **GIVEN** в каталоге уже есть запись `(target, svc)`
- **WHEN** выполняется фаза начала переноса `(source, svc) → target`
- **THEN** возвращается `errs.ErrConflict`, статус `(source, svc)` остаётся
  `active`, побочных эффектов нет

#### Scenario: Недопустимый исходный статус (предусловие)

- **GIVEN** сервис `(source, svc)` со `status=creating` (или `failed`/
  `decommissioned`)
- **WHEN** выполняется фаза начала переноса
- **THEN** возвращается `errs.ErrPrecondition`, статус не меняется

#### Scenario: Конфликт guarded-CAS при конкурентной смене статуса

- **GIVEN** сервис, чей `status` сменился с `active` конкурентной операцией между
  чтением и CAS
- **WHEN** выполняется guarded-CAS фазы начала
- **THEN** `RowsAffected==0` трактуется как конфликт, возвращается `errs.ErrConflict`

#### Scenario: Успешная фиксация переноса (transferring→active, project=target)

- **GIVEN** сервис `(source, svc)` со `status=transferring`
- **WHEN** выполняется фаза фиксации в target
- **THEN** `project` становится `target`, `status` — `active`, id записи и
  владельцы сохранены; повторная фиксация на `(target, svc, active)` — идемпотентный
  no-op

#### Scenario: Перенос несуществующего сервиса

- **GIVEN** в каталоге нет записи `(source, missing)`
- **WHEN** выполняется перенос для неё
- **THEN** возвращается `errs.ErrNotFound`, записи не создаются

### Requirement: gRPC TransferService

Сервис `projects` ДОЛЖЕН (MUST) реализовать gRPC `TransferService(project, name,
target_project)`: валидировать запрос (`project`/`name`/`target_project`
обязательны, `target_project != project`), проверять права через IDM на ОБА
проекта (см. требование двусторонней авторизации), читать запись каталога и
запускать Temporal-workflow «Перенос» с детерминированным `WorkflowID` ТОЛЬКО для
сервиса в статусе `active`. При отсутствии записи ДОЛЖЕН (MUST) возвращаться
`codes.NotFound`; при уже перенесённом сервисе (`project` уже равен `target`,
`active`) — идемпотентный успех (workflow не стартует); при статусе
`creating`/`failed`/`decommissioned`/`transferring` — `codes.FailedPrecondition`;
при занятом имени в target или конкурентном конфликте — `codes.Aborted`; при
невалидном запросе — `codes.InvalidArgument`. Внутренние ошибки НЕ ДОЛЖНЫ (MUST
NOT) раскрываться клиенту (`err.Error()` наружу не отдаётся; детали — в лог по
ключу slog `err`). Семантика повторного вызова идемпотентна.

#### Scenario: Запуск переноса

- **GIVEN** сервис `(demo, svc)` со `status=active`, субъект имеет права
  `transfer` на `project:demo` и `transfer_in` на `project:demo2`, пара
  `(demo2, svc)` свободна
- **WHEN** вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** стартует Temporal-workflow «Перенос» с детерминированным `WorkflowID`,
  клиенту возвращается подтверждение без раскрытия деталей

#### Scenario: Идемпотентный повтор на уже перенесённом сервисе

- **GIVEN** сервис уже находится в `(demo2, svc)` со `status=active`
- **WHEN** повторно вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** возвращается успех с итоговым состоянием, новый workflow не стартует

#### Scenario: Недопустимый исходный статус

- **GIVEN** сервис `(demo, svc)` со `status=creating` (или `transferring`)
- **WHEN** вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** возвращается `codes.FailedPrecondition`, workflow не стартует

#### Scenario: Занятое имя в target

- **GIVEN** в `project:demo2` уже есть сервис с именем `svc`
- **WHEN** вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** возвращается `codes.Aborted` (конфликт), workflow не стартует

#### Scenario: Невалидный запрос (target совпадает с source)

- **GIVEN** запрос с `project == target_project`
- **WHEN** вызывается `TransferService`
- **THEN** возвращается `codes.InvalidArgument`, workflow не стартует

#### Scenario: Отсутствующий сервис

- **GIVEN** записи `(demo, missing)` нет в каталоге
- **WHEN** вызывается `TransferService(demo, missing, target_project=demo2)`
- **THEN** возвращается `codes.NotFound`, workflow не стартует

### Requirement: Двусторонняя авторизация переноса (fail-closed)

Перенос ДОЛЖЕН (MUST) быть защищён ДВУМЯ RBAC-действиями: `transfer` на ресурсе
`project:<source>` И `transfer_in` на ресурсе `project:<target>`. Сервис `projects`
ДОЛЖЕН (MUST) вызывать IDM `CheckAccess` для ОБОИХ проектов ПЕРЕД доменной
операцией и запуском workflow (defense-in-depth). Отказ политики по ЛЮБОМУ из двух
проектов, недоступность или ошибка IDM ДОЛЖНЫ (MUST) приводить к
`codes.PermissionDenied` (fail-closed), без побочных эффектов и без раскрытия
внутренних деталей; `subject` берётся из claims контекста.

#### Scenario: Оба права есть — операция допускается

- **GIVEN** субъект имеет `(transfer, project:demo)` и `(transfer_in, project:demo2)`
- **WHEN** вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** обе проверки `CheckAccess` дают `allowed=true`, операция продолжается

#### Scenario: Нет права transfer на source — отказ

- **GIVEN** субъект без права `(transfer, project:demo)`
- **WHEN** вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** возвращается `codes.PermissionDenied`, статус не меняется, workflow не
  стартует

#### Scenario: Нет права transfer_in на target — отказ

- **GIVEN** субъект имеет `(transfer, project:demo)`, но НЕ имеет
  `(transfer_in, project:demo2)`
- **WHEN** вызывается `TransferService(demo, svc, target_project=demo2)`
- **THEN** возвращается `codes.PermissionDenied` (нельзя «вынести» сервис в чужой
  проект), workflow не стартует

#### Scenario: IDM недоступен — fail-closed

- **GIVEN** IDM недоступен (ошибка вызова `CheckAccess`)
- **WHEN** вызывается `TransferService`
- **THEN** возвращается `codes.PermissionDenied`, деталь ошибки уходит только в лог
  по ключу slog `err`, побочных эффектов нет

### Requirement: Workflow «Перенос» (Saga) с точкой невозврата

Сервис `projects` ДОЛЖЕН (MUST) определять отдельный публичный контракт workflow
«Перенос» (имя workflow, имена activities, детерминированный `WorkflowID` на пару
`(source, name)`), исполняемый DevInfra worker'ом. Тело workflow ДОЛЖНО (MUST)
быть детерминированным (весь I/O — в activities с RetryPolicy/таймаутами/heartbeat).
Порядок шагов: (0) `CatalogBeginTransfer` guarded-CAS `active→transferring`
(компенсируемо); (1) `GitLabTransferRepo` перенос репозитория в новую группу —
**ТОЧКА НЕВОЗВРАТА** (необратимо); (2) `VaultMigratePaths` копия секретов
`source→target` + новые политики + очистка старых; (3) `HarborUpdateMetadata`
обновление метаданных/прав; (4) `CatalogCommitTransfer` guarded-CAS
`transferring→active` + `project=target`; (5) `TransferOwnerRoles` перенос ролей
владельцев в IDM. Окончательный (non-retryable) сбой ДО точки невозврата (включая
сбой запуска шага 1 после успешного CAS начала) ДОЛЖЕН (MUST) запускать
идемпотентную компенсацию `CatalogAbortTransfer` (`transferring→active`), внешние
системы не трогать. Сбой ПОСЛЕ точки невозврата (включая конфликт guarded-CAS на
шаге 4) НЕ ДОЛЖЕН (MUST NOT) приводить к молчаливому откату: workflow ДОЛЖЕН (MUST)
форвард-only ретраить идемпотентные шаги, а при исчерпании — фиксировать алерт
оператору структурным логом (ADR-0005/0008); сервис может оставаться в
`transferring` до ручного довыполнения. Каталог = целевой источник правды.

#### Scenario: Happy-path переноса

- **GIVEN** сервис `(source, svc)` со `status=active`, пара `(target, svc)` свободна
- **WHEN** выполняется workflow «Перенос»
- **THEN** последовательно: каталог переведён в `transferring`, репозиторий GitLab
  перенесён в новую группу, пути/политики Vault мигрированы (копия + новые политики
  + очистка старых), метаданные/права Harbor обновлены, каталог зафиксирован
  guarded-CAS (`project=target`, `status=active`), роли владельцев перенесены в IDM
  с инвалидацией кэша, workflow завершается успешно

#### Scenario: Компенсация при сбое до точки невозврата

- **GIVEN** каталог уже переведён в `transferring`, а запуск шага GitLab transfer
  сорвался non-retryable-ошибкой ДО самого переноса репозитория
- **WHEN** workflow обрабатывает сбой
- **THEN** запускается идемпотентная компенсация `CatalogAbortTransfer`
  (`transferring→active`), внешние системы не затронуты, workflow завершается
  ошибкой без частичного переноса

#### Scenario: Алерт после точки невозврата

- **GIVEN** репозиторий GitLab уже перенесён (точка невозврата пройдена), а
  миграция Vault/фиксация каталога окончательно недоступна
- **WHEN** workflow обрабатывает сбой
- **THEN** молчаливого отката НЕТ; workflow форвард-only ретраит идемпотентные
  шаги, а при исчерпании фиксирует алерт оператору структурным логом; сервис
  остаётся в `transferring` до ручного довыполнения

#### Scenario: Конфликт guarded-CAS на фиксации каталога

- **GIVEN** инфраструктурные шаги выполнены, но `status` сервиса сменился
  конкурентной операцией (не `transferring`)
- **WHEN** workflow выполняет `CatalogCommitTransfer`
- **THEN** фиксация возвращает `errs.ErrConflict`; поскольку точка невозврата
  пройдена, фиксируется алерт оператору (без отката инфраструктуры)

#### Scenario: Идемпотентный повтор workflow

- **GIVEN** workflow «Перенос» уже выполнялся для `(source, svc)`
- **WHEN** он запускается повторно с тем же детерминированным `WorkflowID`
- **THEN** идемпотентные шаги (transfer/migrate/update/CAS/роли) не приводят к
  повторным побочным эффектам и не ломают итоговое состояние

### Requirement: Тестовое покрытие переноса

Тесты ДОЛЖНЫ (MUST) быть table-driven с `t.Parallel()`; пакеты с горутинами — под
`goleak`. Workflow ДОЛЖЕН (MUST) покрываться Temporal testsuite с замоканными
activities: happy-path, занятое имя в target (отказ до side-effect), ветка
компенсации (сбой до точки невозврата), алерт после точки невозврата и конфликт
guarded-CAS фиксации. Repository переноса ДОЛЖЕН (MUST) тестироваться стабом/
in-memory в дефолтном прогоне и pgx против тест-БД под тегом `integration` (DSN из
`PROJECTS_TEST_DSN`, `Skip` без БД).

#### Scenario: Дефолтный прогон без БД и сети

- **WHEN** выполняется `go test ./...` без тега `integration`
- **THEN** проходят тесты workflow (testsuite с моками: happy, занятое имя,
  компенсация, алерт, конфликт CAS) и доменные тесты переноса (guarded-CAS двух
  фаз, идемпотентность, предусловие, конфликт) на стабе/in-memory без Postgres и
  сети

#### Scenario: Integration-тесты переноса под тегом

- **WHEN** выполняется прогон с тегом `integration` при доступной БД
- **THEN** запускаются тесты repository переноса против реального Postgres
  (guarded-CAS смены `project`, сохранность id/владельцев, проверка свободы
  `(target_project, name)`, транзитный `transferring`)
