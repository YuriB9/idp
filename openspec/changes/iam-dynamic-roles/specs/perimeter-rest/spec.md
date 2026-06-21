# perimeter-rest Specification (delta)

## ADDED Requirements

### Requirement: REST-ручки создания и удаления роли

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять `POST /iam/roles` (создать роль,
тело `{name}`) и `DELETE /iam/roles/{role}` (удалить роль), проксируемые в
`IamCatalogService.CreateRole`/`DeleteRole`. gateway ДОЛЖЕН (MUST) ПЕРЕД
проксированием вызывать `CheckAccess` с `resource="iam:global"`, `action="manage"`
(fail-closed → 403). Создание возвращает `201` с `{name, system:false}`; дубль
имени → `409` (из `AlreadyExists`); пустое имя → `400`. Удаление пользовательской
роли возвращает `200` (каскадно снимает роль у носителей); несуществующая роль →
`404`; СИСТЕМНАЯ роль → `422` (из `FailedPrecondition`). Все возвращаемые коды
(200/201/400/403/404/409/422) ДОЛЖНЫ (MUST) быть документированы в OpenAPI
(summary + description + operationId + ВСЕ коды) — иначе Spectral и
Schemathesis-конформанс красные. Внутренние ошибки наружу НЕ раскрываются. TS-клиент
и zod, а также `web/public/openapi.yaml` регенерируются (`gen:check`).

#### Scenario: Успешное создание роли

- **GIVEN** субъект с правом `(manage, iam:global)`, роли `reviewers` нет
- **WHEN** выполняется `POST /iam/roles` с телом `{"name":"reviewers"}`
- **THEN** возвращается `201` с `{name:"reviewers", system:false}`

#### Scenario: Дубль имени роли → 409

- **GIVEN** роль `reviewers` уже существует
- **WHEN** выполняется `POST /iam/roles` с `{"name":"reviewers"}`
- **THEN** возвращается `409` (из `AlreadyExists`), без раскрытия внутренних деталей

#### Scenario: Удаление системной роли → 422

- **GIVEN** роль `iam-admin` системная
- **WHEN** выполняется `DELETE /iam/roles/iam-admin`
- **THEN** возвращается `422` (из `FailedPrecondition`), роль не удаляется

#### Scenario: Удаление несуществующей роли → 404

- **WHEN** выполняется `DELETE /iam/roles/no-such-role`
- **THEN** возвращается `404` (из `NotFound`)

#### Scenario: Нет права manage → 403 (fail-closed)

- **GIVEN** субъект без `(manage, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется `POST`/`DELETE /iam/roles*`
- **THEN** gateway отвечает `403`, мутация не проксируется, деталь — только в лог

### Requirement: REST-ручки правки набора прав роли (attach/detach)

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/roles/{role}/permissions` (attach,
тело `{action,resource}`) и `DELETE /iam/roles/{role}/permissions?action=&resource=`
(detach, пара в query-параметрах), проксируемые в
`IamCatalogService.AttachPermission`/`DetachPermission` под `CheckAccess(manage,
iam:global)` (fail-closed → 403). Обе ручки ДОЛЖНЫ (MUST) быть ИДЕМПОТЕНТНЫМИ:
повторный attach уже привязанного и detach непривязанного → `200`. Ответ ДОЛЖЕН
(MUST) нести актуальный набор прав роли (`{role, permissions[]}`) для рантайм-
валидации zod и обновления UI. Несуществующая роль или (для attach) несуществующее
право → `404`; СИСТЕМНАЯ роль → `422`; пустые/битые поля → `400`. Все коды
(200/400/403/404/422) документируются в OpenAPI. Внутренние ошибки наружу НЕ
раскрываются.

#### Scenario: Успешный attach права к роли

- **GIVEN** субъект с `(manage, iam:global)`, пользовательская роль `reviewers` и
  право `(read, iam:global)` существуют
- **WHEN** выполняется `POST /iam/roles/reviewers/permissions` с
  `{"action":"read","resource":"iam:global"}`
- **THEN** возвращается `200` с актуальным набором прав роли `reviewers`

#### Scenario: Идемпотентный повтор attach/detach

- **GIVEN** право уже привязано к роли `reviewers`
- **WHEN** повторно `POST .../permissions`, затем дважды `DELETE
  .../permissions?action=read&resource=iam:global`
- **THEN** каждый вызов возвращает `200` с актуальным набором прав (без ошибки)

#### Scenario: Attach/detach на системную роль → 422

- **GIVEN** роль `iam-admin` системная
- **WHEN** выполняется `POST`/`DELETE /iam/roles/iam-admin/permissions`
- **THEN** возвращается `422` (из `FailedPrecondition`)

#### Scenario: Attach несуществующего права → 404

- **GIVEN** роль `reviewers` существует, права `(x,y)` нет
- **WHEN** выполняется `POST /iam/roles/reviewers/permissions` с `{"action":"x",
  "resource":"y"}`
- **THEN** возвращается `404` (из `NotFound`), право не создаётся неявно

#### Scenario: Невалидное тело attach → 400

- **WHEN** выполняется `POST /iam/roles/reviewers/permissions` с пустыми полями
- **THEN** возвращается `400` (из `InvalidArgument`)

### Requirement: REST-ручки создания и удаления права

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/permissions` (создать право, тело
`{action,resource}`) и `DELETE /iam/permissions?action=&resource=` (удалить),
проксируемые в `IamCatalogService.CreatePermission`/`DeletePermission` под
`CheckAccess(manage, iam:global)` (fail-closed → 403). Создание возвращает `201`
с `{action, resource, system:false}`; дубль пары → `409`; пустые/битые поля →
`400`. Удаление пользовательского права → `200` (каскадно снимает связки с ролями);
несуществующее → `404`; СИСТЕМНОЕ → `422`. Все коды (200/201/400/403/404/409/422)
документируются в OpenAPI. Внутренние ошибки наружу НЕ раскрываются.

#### Scenario: Успешное создание права

- **GIVEN** субъект с `(manage, iam:global)`, пары `(deploy, project:demo)` нет
- **WHEN** выполняется `POST /iam/permissions` с
  `{"action":"deploy","resource":"project:demo"}`
- **THEN** возвращается `201` с `{action, resource, system:false}`

#### Scenario: Дубль пары права → 409

- **GIVEN** право `(read, iam:global)` уже существует
- **WHEN** выполняется `POST /iam/permissions` с `{"action":"read",
  "resource":"iam:global"}`
- **THEN** возвращается `409` (из `AlreadyExists`)

#### Scenario: Удаление системного права → 422

- **GIVEN** право `(read, iam:global)` системное
- **WHEN** выполняется `DELETE /iam/permissions?action=read&resource=iam:global`
- **THEN** возвращается `422` (из `FailedPrecondition`)

#### Scenario: Удаление несуществующего права → 404

- **WHEN** выполняется `DELETE /iam/permissions?action=x&resource=y`
- **THEN** возвращается `404` (из `NotFound`)
