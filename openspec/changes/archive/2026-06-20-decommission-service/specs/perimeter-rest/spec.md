# perimeter-rest Specification (delta)

## ADDED Requirements

### Requirement: REST-ручка вывода из эксплуатации

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять ручку вывода сервиса из
эксплуатации `POST /projects/{project}/services/{name}/decommission` с телом,
несущим явное предусловие снятой нагрузки (`load_drained`). Метод POST на
под-ресурсе выбран вместо `DELETE`, т.к. это soft-delete (данные каталога
сохраняются), а `DELETE` подразумевал бы удаление/purge. Ручка ДОЛЖНА (MUST) быть
идемпотентной: повторный вызов на уже выведенном сервисе возвращает успех с
итоговым состоянием. gateway ДОЛЖЕН (MUST) вызывать IDM `CheckAccess`
(`decommission`, `project:<project>`) ПЕРЕД проксированием (fail-closed) и НЕ
ДОЛЖЕН (MUST NOT) раскрывать внутренние ошибки клиенту. OpenAPI остаётся
единственным источником правды; TS-клиент/zod регенерируются (`gen:check`).

#### Scenario: Успешный запуск вывода из эксплуатации

- **GIVEN** субъект с правом `decommission` на `project:demo` и активный сервис
  `(demo, svc)` со снятой нагрузкой
- **WHEN** выполняется `POST /projects/demo/services/svc/decommission` с
  `{load_drained:true}`
- **THEN** gateway проксирует в `projects`, запускается вывод из эксплуатации,
  возвращается успешный ответ без раскрытия внутренних деталей

#### Scenario: Идемпотентный повтор

- **GIVEN** сервис `(demo, svc)` уже в статусе `decommissioned`
- **WHEN** повторно выполняется `POST .../decommission`
- **THEN** возвращается успех с итоговым состоянием (без ошибки)

### Requirement: Маппинг кодов и статус decommissioned в ответах периметра

gateway ДОЛЖЕН (MUST) маппить gRPC-коды в HTTP для операции вывода из эксплуатации:
`PermissionDenied→403`, `NotFound→404`, конкурентный конфликт (`Aborted`)→409,
невыполненное предусловие (`FailedPrecondition`)→422, `InvalidArgument→400`,
прочее→500 (без деталей). Чтобы развести конкурентный конфликт (409) и
невыполненное предусловие (422), `httpFromGRPC` ДОЛЖЕН (MUST) трактовать
`codes.Aborted` как 409, а `codes.FailedPrecondition` как 422. Ответы `GET`/`LIST`
ДОЛЖНЫ (MUST) включать статус `decommissioned` и `decommissioned_at` для выведенных
сервисов. Существующий маппинг конфликта владельцев (`change-owners`) ДОЛЖЕН (MUST)
сохранять итоговый HTTP-код 409 (через `Aborted`).

#### Scenario: Предусловие не выполнено → 422

- **GIVEN** активный сервис `(demo, svc)` и `{load_drained:false}`
- **WHEN** выполняется `POST .../decommission`
- **THEN** `projects` возвращает `FailedPrecondition`, gateway отвечает `422`, без
  раскрытия внутренних деталей

#### Scenario: Конкурентный конфликт → 409

- **GIVEN** статус сервиса сменился конкурентной операцией
- **WHEN** выполняется `POST .../decommission`
- **THEN** `projects` возвращает `Aborted`, gateway отвечает `409`

#### Scenario: Нет права → 403 (fail-closed)

- **GIVEN** субъект без права `decommission` (или IDM недоступен)
- **WHEN** выполняется `POST .../decommission`
- **THEN** gateway отвечает `403`, запрос в `projects` не проксируется при отказе
  на периметре, деталь ошибки — только в лог по ключу slog `err`

#### Scenario: Статус decommissioned в чтении

- **GIVEN** сервис `(demo, svc)` выведен из эксплуатации
- **WHEN** выполняется `GET /projects/demo/services/svc`
- **THEN** ответ содержит `status=decommissioned` и `decommissioned_at`
