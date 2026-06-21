# service-contracts Specification (delta)

## ADDED Requirements

### Requirement: Читающий контракт IamAdminService в idm.v1

`proto/idm/v1` ДОЛЖЕН (MUST) предоставлять новый gRPC-сервис `IamAdminService` с
читающими RPC для IAM-админки: `ListRoles`, `ListPermissions`,
`GetRolePermissions(role)`, `ListSubjectsWithRoles(page_size, page_token)`,
`GetSubjectRoles(subject)`. Сообщения ДОЛЖНЫ (MUST) включать `Role{name}`,
`Permission{action, resource}`, `SubjectRoles{subject, roles[]}`;
`ListSubjectsWithRolesResponse` ДОЛЖЕН (MUST) нести `next_page_token` (keyset).
Изменение ДОЛЖНО (MUST) быть аддитивным и wire-совместимым: добавляется новый
сервис и новые сообщения, существующие RPC/сообщения (`AccessService`,
`RoleAdminService`, их запросы/ответы) НЕ изменяются. Мутации ролей переиспользуют
`RoleAdminService.AssignRole`/`RevokeRole` (новых RPC мутации НЕ вводится). После
правки `.proto` сгенерированные `*.pb.go` ДОЛЖНЫ (MUST) быть перегенерированы
(`buf generate`), а `gen:check` — зелёным.

#### Scenario: Аддитивное расширение контракта idm

- **WHEN** в `proto/idm/v1` добавляется `IamAdminService` с читающими RPC и
  сообщениями `Role`/`Permission`/`SubjectRoles`
- **THEN** существующие `AccessService.CheckAccess` и
  `RoleAdminService.Assign/RevokeRole` остаются без изменений (wire-совместимо),
  `buf generate` обновляет `pkg/api/idm/**`, `gen:check` зелёный

#### Scenario: Идентификатор роли — name, не id

- **WHEN** клиент получает роли через `ListRoles` или `GetSubjectRoles`
- **THEN** роль идентифицируется строкой `name` (тем же, что принимают
  `AssignRole`/`RevokeRole`); `id` (UUID) наружу не отдаётся

#### Scenario: Keyset-пагинация субъектов в контракте

- **WHEN** вызывается `ListSubjectsWithRoles` с `page_size`/`page_token`
- **THEN** ответ содержит страницу `SubjectRoles` и непрозрачный `next_page_token`
  для следующей страницы (пустой — если страниц больше нет)
