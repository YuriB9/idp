# service-contracts Specification (delta)

## ADDED Requirements

### Requirement: Контракт TransferService в projects.v1

Контракт `proto/projects/v1` ДОЛЖЕН (MUST) расширяться новым RPC
`TransferService(TransferServiceRequest) returns (TransferServiceResponse)` для
переноса сервиса в другой проект: запрос несёт `project` (source), `name` и
`target_project`; ответ — итоговое состояние сервиса (`project=target`,
`status=ACTIVE`). В перечисление `ServiceStatus` ДОЛЖНО (MUST) аддитивно
добавляться значение транзитного статуса `SERVICE_STATUS_TRANSFERRING = 5` (без
переиспользования занятых номеров и без смены смысла существующих значений).
Добавление нового RPC в существующий сервис помечается как BREAKING в комментарии
контракта. Кодоген (`buf generate` → `*.pb.go`) и TS-клиент портала ДОЛЖНЫ (MUST)
регенерироваться, `gen:check` зелёный. Все комментарии в `.proto` — на русском
языке.

#### Scenario: Регенерация Go и TS из контракта

- **GIVEN** обновлённый `proto/projects/v1` с RPC `TransferService` и значением
  enum `SERVICE_STATUS_TRANSFERRING`
- **WHEN** выполняется `buf generate` и регенерация TS-клиента
- **THEN** появляются типы/метод в `pkg/api/projects/v1` и в TS-клиенте `web`, а
  `gen:check` (proto+OpenAPI+TS) проходит без расхождений

#### Scenario: Аддитивность значения enum

- **GIVEN** существующие значения `ServiceStatus` (`UNSPECIFIED=0`..`FAILED=4`)
- **WHEN** добавляется `SERVICE_STATUS_TRANSFERRING`
- **THEN** ему присваивается новый номер `5`, существующие значения и их номера не
  изменяются (обратная совместимость по wire)

#### Scenario: Идемпотентная семантика контракта

- **GIVEN** контракт `TransferService`
- **WHEN** описывается семантика повторного вызова на уже перенесённом сервисе
- **THEN** контракт (комментарий `.proto`) фиксирует идемпотентность: повторный
  вызов (когда `project` уже равен `target`) возвращает итоговое состояние без
  ошибки
