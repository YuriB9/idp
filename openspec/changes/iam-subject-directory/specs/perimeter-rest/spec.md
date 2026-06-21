# perimeter-rest Specification (delta)

## ADDED Requirements

### Requirement: REST-ручка поиска субъектов в справочнике

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять `GET /iam/directory/subjects?search=
&cursor=&page_size=` (поиск/листинг пользователей каталога Keycloak), проксируемую в
`IdentityService.SearchSubjects`. gateway ДОЛЖЕН (MUST) ПЕРЕД проксированием вызывать
`CheckAccess` с `resource="iam:directory"`, `action="read"` (fail-closed → 403).
Успех возвращает `200` с `{subjects:[SubjectIdentity], next}`; пустой/слишком
короткий `search` или некорректный `page_size`/`cursor` → `400`; отказ авторизации
ИЛИ недоступность IDM → `403`; недоступность Keycloak → `503` (деградация, retryable).
Курсор периметра ДОЛЖЕН (MUST) быть непрозрачным (поверх offset Keycloak). Все
возвращаемые коды (200/400/403/503) ДОЛЖНЫ (MUST) быть документированы в OpenAPI
(summary + description + operationId + ВСЕ коды) — иначе Spectral и Schemathesis-
конформанс красные. Внутренние ошибки и секреты наружу НЕ раскрываются. TS-клиент,
zod и `web/public/openapi.yaml` регенерируются (`gen:check`).

#### Scenario: Успешный поиск

- **GIVEN** субъект с `(read, iam:directory)`, Keycloak доступен
- **WHEN** выполняется `GET /iam/directory/subjects?search=iv&page_size=20`
- **THEN** возвращается `200` с `{subjects:[...], next}` (идентичности с `found=true`)

#### Scenario: Нет права directory → 403 (fail-closed)

- **GIVEN** субъект без `(read, iam:directory)` ИЛИ IDM недоступен
- **WHEN** выполняется `GET /iam/directory/subjects?search=iv`
- **THEN** gateway отвечает `403`, запрос к справочнику не проксируется, деталь — в лог

#### Scenario: Пустой/битый поисковый ввод → 400

- **WHEN** выполняется `GET /iam/directory/subjects` без `search` или с пустым/
  слишком коротким `search`, либо с некорректным `page_size`
- **THEN** возвращается `400` (из `InvalidArgument`), обращения к Keycloak нет

#### Scenario: Каталог недоступен → 503 (деградация)

- **GIVEN** субъект с `(read, iam:directory)`, но Keycloak недоступен
- **WHEN** выполняется `GET /iam/directory/subjects?search=iv`
- **THEN** возвращается `503` (из `Unavailable`), без сырых деталей; управление
  ролями по сырому subject остаётся доступным

### Requirement: REST-ручка батч-резолва субъектов

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/directory/subjects/resolve` (тело
`{subjects:[...]}`), проксируемую в `IdentityService.ResolveSubjects` под
`CheckAccess(read, iam:directory)` (fail-closed → 403). Успех возвращает `200` с
`{subjects:[SubjectIdentity]}`, где отсутствующие в каталоге субъекты помечены
`found=false` (не опускаются). Пустой/битый список → `400`; недоступность Keycloak →
`503`. Все коды (200/400/403/503) документируются в OpenAPI. Внутренние ошибки
наружу НЕ раскрываются.

#### Scenario: Успешный батч-резолв с осиротевшим

- **GIVEN** субъект с `(read, iam:directory)`; `sub1` есть в каталоге, `sub2` — нет
- **WHEN** выполняется `POST /iam/directory/subjects/resolve` с `{subjects:[sub1,sub2]}`
- **THEN** возвращается `200`; `sub1` с `found=true` и именами, `sub2` с `found=false`

#### Scenario: Пустой список → 400

- **WHEN** выполняется `POST /iam/directory/subjects/resolve` с пустым `subjects`
- **THEN** возвращается `400` (из `InvalidArgument`)

#### Scenario: Каталог недоступен → 503

- **GIVEN** субъект с `(read, iam:directory)`, Keycloak недоступен
- **WHEN** выполняется `POST /iam/directory/subjects/resolve` с непустым списком
- **THEN** возвращается `503` (из `Unavailable`)

### Requirement: Обогащение GET /iam/subjects идентичностями (композиция в gateway)

gateway ДОЛЖЕН (MUST) обогащать ответ существующей ручки `GET /iam/subjects`
(subjects-with-roles) идентичностями: после получения списка от
`IamAdminService.ListSubjectsWithRoles` (под `(read, iam:global)`, как ADR-0014) он
ДОЛЖЕН (MUST), ЕСЛИ вызывающий ДОПОЛНИТЕЛЬНО держит `(read, iam:directory)`, вызвать
`IdentityService.ResolveSubjects` и слить идентичности в ответ. Без права
`(read, iam:directory)` ответ ДОЛЖЕН (MUST) оставаться «сырым» (текущее поведение
ADR-0014, без PII). «Осиротевшие» субъекты ДОЛЖНЫ (MUST) помечаться `found=false`.
Недоступность Keycloak при резолве НЕ ДОЛЖНА (MUST NOT) ломать ручку: список ролей
отдаётся `200` без идентичностей (деградация). Поля идентичности в схеме ответа
OpenAPI ДОЛЖНЫ (MUST) быть аддитивными/опциональными; zod-поля — optional.

#### Scenario: Обогащённый список при наличии обоих прав

- **GIVEN** субъект держит `(read, iam:global)` и `(read, iam:directory)`, Keycloak
  доступен
- **WHEN** выполняется `GET /iam/subjects`
- **THEN** возвращается `200`; субъекты с ролями обогащены `username`/`email`/
  `display_name`/`enabled`, `found=true`

#### Scenario: Сырой список без права directory

- **GIVEN** субъект держит только `(read, iam:global)`
- **WHEN** выполняется `GET /iam/subjects`
- **THEN** возвращается `200` без полей идентичности (PII не раскрывается)

#### Scenario: Деградация при недоступном Keycloak

- **GIVEN** субъект держит оба права, но Keycloak недоступен
- **WHEN** выполняется `GET /iam/subjects`
- **THEN** возвращается `200` со списком ролей без идентичностей; ручка не падает в 503

#### Scenario: Осиротевший субъект в списке

- **GIVEN** субъект `sub` есть в `subject_roles`, но в каталоге Keycloak его нет
- **WHEN** выполняется `GET /iam/subjects` (с правом directory)
- **THEN** субъект присутствует как raw `sub` с `found=false`
