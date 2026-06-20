# perimeter-rest Specification (delta)

## ADDED Requirements

### Requirement: REST-ручка переноса сервиса

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять ручку переноса сервиса
`POST /projects/{project}/services/{name}/transfer` с телом, несущим
`target_project`. Метод POST на под-ресурсе выбран (по образцу `decommission`),
т.к. перенос — доменное действие, а не CRUD-replace ресурса. Ручка ДОЛЖНА (MUST)
быть идемпотентной: повторный вызов на уже перенесённом сервисе возвращает успех с
итоговым состоянием. gateway ДОЛЖЕН (MUST) вызывать IDM `CheckAccess` для ОБОИХ
проектов (`transfer` на `project:<source>` И `transfer_in` на `project:<target>`)
ПЕРЕД проксированием (fail-closed) и НЕ ДОЛЖЕН (MUST NOT) раскрывать внутренние
ошибки клиенту. OpenAPI остаётся единственным источником правды; TS-клиент/zod
регенерируются (`gen:check`).

#### Scenario: Успешный запуск переноса

- **GIVEN** субъект с правами `transfer` на `project:demo` и `transfer_in` на
  `project:demo2`, активный сервис `(demo, svc)`, пара `(demo2, svc)` свободна
- **WHEN** выполняется `POST /projects/demo/services/svc/transfer` с
  `{target_project:"demo2"}`
- **THEN** gateway проксирует в `projects`, запускается перенос, возвращается
  успешный ответ без раскрытия внутренних деталей

#### Scenario: Идемпотентный повтор

- **GIVEN** сервис уже перенесён в `(demo2, svc)`
- **WHEN** повторно выполняется `POST /projects/demo/services/svc/transfer` с
  `{target_project:"demo2"}`
- **THEN** возвращается успех с итоговым состоянием (без ошибки)

### Requirement: Двусторонняя авторизация и маппинг кодов переноса на периметре

gateway ДОЛЖЕН (MUST) при переносе проверять права на ОБА проекта (`transfer` на
source И `transfer_in` на target) перед проксированием; отказ по любому из них →
`403` (fail-closed), запрос в `projects` не проксируется. gateway ДОЛЖЕН (MUST)
маппить gRPC-коды в HTTP для операции переноса: `PermissionDenied→403`,
`NotFound→404`, конкурентный конфликт/занятое имя в target (`Aborted`/
`AlreadyExists`)→409, невыполненное предусловие (`FailedPrecondition`)→422,
`InvalidArgument→400`, прочее→500 (без деталей). Новых правил `httpFromGRPC` не
требуется — используется разведение `Aborted→409`/`FailedPrecondition→422` из
ADR-0012. Ответы `GET`/`LIST` ДОЛЖНЫ (MUST) корректно отражать транзитный статус
`transferring`.

#### Scenario: Занятое имя в target → 409

- **GIVEN** в `project:demo2` уже есть сервис `svc`
- **WHEN** выполняется `POST /projects/demo/services/svc/transfer` с
  `{target_project:"demo2"}`
- **THEN** `projects` возвращает `Aborted`, gateway отвечает `409`

#### Scenario: Недопустимый исходный статус → 422

- **GIVEN** сервис `(demo, svc)` в статусе `creating` (или `transferring`)
- **WHEN** выполняется `POST .../transfer`
- **THEN** `projects` возвращает `FailedPrecondition`, gateway отвечает `422`, без
  раскрытия внутренних деталей

#### Scenario: Нет права на source или target → 403 (fail-closed)

- **GIVEN** субъект без права `transfer` на source (или без `transfer_in` на
  target, или IDM недоступен)
- **WHEN** выполняется `POST .../transfer`
- **THEN** gateway отвечает `403`, запрос в `projects` не проксируется, деталь
  ошибки — только в лог по ключу slog `err`

#### Scenario: Транзитный статус в чтении

- **GIVEN** сервис `(demo, svc)` находится в процессе переноса
- **WHEN** выполняется `GET /projects/demo/services/svc`
- **THEN** ответ содержит `status=transferring`
