# service-contracts Specification (delta)

## ADDED Requirements

### Requirement: Мутирующий контракт IamCatalogService в idm.v1

`proto/idm/v1` ДОЛЖЕН (MUST) предоставлять новый gRPC-сервис `IamCatalogService`
со СТРУКТУРНЫМИ мутациями каталога RBAC: `CreateRole(name)`, `DeleteRole(name)`,
`CreatePermission(action,resource)`, `DeletePermission(action,resource)`,
`AttachPermission(role,action,resource)`, `DetachPermission(role,action,resource)`.
Сообщение ответа правки прав роли ДОЛЖНО (MUST) нести актуальный набор прав роли
(`RolePermissions{role, permissions[]}`). Изменение ДОЛЖНО (MUST) быть аддитивным
и wire-совместимым: добавляется новый сервис и новые сообщения, существующие RPC
читающего `IamAdminService`, `AccessService` и `RoleAdminService` НЕ изменяются.
Мутации привязок субъект↔роль переиспользуют `RoleAdminService.AssignRole/
RevokeRole` (новых RPC привязок НЕ вводится). После правки `.proto`
сгенерированные `*.pb.go` ДОЛЖНЫ (MUST) быть перегенерированы (`buf generate`,
`GOWORK=off`, пин `./tools`), а `gen:check` — зелёным.

#### Scenario: Аддитивное расширение контракта мутациями каталога

- **WHEN** в `proto/idm/v1` добавляется `IamCatalogService` с RPC create/delete
  роли и права, attach/detach права роли
- **THEN** существующие `AccessService.CheckAccess`, `RoleAdminService.Assign/
  RevokeRole` и читающий `IamAdminService` остаются без изменений (wire-совместимо),
  `buf generate` обновляет `pkg/api/idm/**`, `gen:check` зелёный

#### Scenario: Идентификатор роли — name, право — пара action/resource

- **WHEN** клиент вызывает `DeleteRole`/`AttachPermission`
- **THEN** роль идентифицируется строкой `name`, право — парой `action`/`resource`
  (теми же, что в `RoleAdminService` и `IamAdminService`); `id` (UUID) наружу не
  отдаётся

#### Scenario: Ответ attach/detach несёт актуальный набор прав роли

- **WHEN** вызывается `AttachPermission`/`DetachPermission`
- **THEN** ответ содержит `RolePermissions{role, permissions[]}` с актуальным
  набором прав роли после операции (для рантайм-валидации и обновления UI)

### Requirement: Признак system в сообщениях Role и Permission

Сообщения `Role` и `Permission` в `proto/idm/v1` ДОЛЖНЫ (MUST) быть аддитивно
расширены полем `bool system` (новый номер поля → wire-совместимо), отражающим,
является ли роль/право системным (сидированным, защищённым от удаления и правки).
Читающие RPC `IamAdminService` (`ListRoles`, `ListPermissions`,
`GetRolePermissions`) ДОЛЖНЫ (MUST) отдавать это поле, чтобы клиент мог показывать
системные роли/права как read-only. Существующие потребители, не знающие о поле,
НЕ ДОЛЖНЫ (MUST NOT) ломаться (значение по умолчанию `false`).

#### Scenario: Поле system аддитивно и wire-совместимо

- **WHEN** в `Role`/`Permission` добавляется поле `bool system` с новым номером
- **THEN** старые клиенты продолжают читать прежние поля без ошибок, `gen:check`
  зелёный

#### Scenario: Чтение каталога отдаёт признак system

- **GIVEN** роль `iam-admin` помечена `system=true`, пользовательская роль —
  `system=false`
- **WHEN** вызывается `ListRoles`
- **THEN** каждая роль возвращается с корректным значением `system`
