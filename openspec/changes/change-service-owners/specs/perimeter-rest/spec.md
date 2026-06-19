# perimeter-rest Specification (delta)

## ADDED Requirements

### Requirement: REST-ручка изменения владельцев сервиса

Периметр (gateway, ADR-0009) ДОЛЖЕН (MUST) предоставлять ручку изменения
владельцев `PUT /projects/{project}/services/{name}/owners` с идемпотентной
семантикой «заменить набор владельцев целиком». Тело запроса ДОЛЖНО (MUST)
содержать полный желаемый набор `owners` и версию `owners_version` для
optimistic-concurrency (валидация формы — zod-совместимая: непустые строки, без
дублей). Шлюз НЕ ДОЛЖЕН (MUST NOT) содержать доменной логики — он маппит
REST↔gRPC `SetServiceOwners` и gRPC-коды в HTTP. Внутренние ошибки НЕ ДОЛЖНЫ
(MUST NOT) раскрываться (только стабильное сообщение; детали — в лог по ключу
slog `err`). Маппинг: `NotFound→404`, `FailedPrecondition→409` (конфликт
версии), `InvalidArgument→400`, `PermissionDenied→403`.

#### Scenario: Успешная замена владельцев

- **GIVEN** субъект с правом `change_owners` и существующий сервис `(p1, svc)`
- **WHEN** `PUT /projects/p1/services/svc/owners` с телом
  `{"owners":["alice","bob"],"owners_version":4}`
- **THEN** возвращается `200/202` с подтверждением запуска изменения, без
  раскрытия внутренних деталей

#### Scenario: Невалидное тело — 400

- **WHEN** `PUT .../owners` с телом, содержащим пустые строки или дубли во
  `owners`, либо без `owners_version`
- **THEN** возвращается `400`, gRPC-вызов не выполняется

#### Scenario: Конфликт версии — 409

- **GIVEN** актуальная `owners_version=5`
- **WHEN** `PUT .../owners` с устаревшей `owners_version=4`
- **THEN** gRPC возвращает `FailedPrecondition`, шлюз отдаёт `409` «конфликт
  состояния»

#### Scenario: Отсутствующий сервис — 404

- **WHEN** `PUT /projects/p1/services/missing/owners`
- **THEN** gRPC возвращает `NotFound`, шлюз отдаёт `404`

### Requirement: RBAC change_owners на ручке изменения владельцев (fail-closed)

Шлюз ДОЛЖЕН (MUST) вызывать IDM `CheckAccess` с действием `change_owners` и
ресурсом `project:<project>` ПЕРЕД проксированием ручки изменения владельцев.
Отказ политики, недоступность или ошибка IDM ДОЛЖНЫ (MUST) приводить к HTTP
`403` (fail-closed) без побочных эффектов и без раскрытия деталей; `subject`
берётся из claims контекста.

#### Scenario: Нет права — 403 до проксирования

- **GIVEN** субъект без права `(change_owners, project:p1)`
- **WHEN** `PUT /projects/p1/services/svc/owners`
- **THEN** возвращается `403`, gRPC `SetServiceOwners` не вызывается

#### Scenario: IDM недоступен — 403 (fail-closed)

- **GIVEN** IDM недоступен
- **WHEN** `PUT /projects/p1/services/svc/owners`
- **THEN** возвращается `403`, деталь ошибки только в лог по ключу slog `err`

### Requirement: Владельцы в ответах чтения периметра

Ответы чтения и листинга сервисов ДОЛЖНЫ (MUST) включать поле `owners` (и версию `owners_version`) каждого сервиса, отражая данные из `projects` (`GET .../services/{name}` и `GET .../services`). Контракт
OpenAPI и TS-клиент ДОЛЖНЫ (MUST) обновляться согласованно (`gen:check` зелёный);
рантайм-валидация ответов через zod `.parse` ДОЛЖНА (MUST) принимать новые поля.

#### Scenario: GET одного сервиса отдаёт владельцев

- **GIVEN** сервис `(p1, svc)` с владельцами `{alice, bob}`
- **WHEN** `GET /projects/p1/services/svc`
- **THEN** ответ содержит `owners:["alice","bob"]` и `owners_version`

#### Scenario: LIST отдаёт владельцев по каждой записи

- **WHEN** `GET /projects/p1/services`
- **THEN** каждая запись страницы содержит своё поле `owners`, zod `.parse`
  проходит без ошибок
