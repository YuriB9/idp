# service-contracts Specification (delta)

## ADDED Requirements

### Requirement: Читающий контракт IdentityService в idm.v1

`proto/idm/v1` ДОЛЖЕН (MUST) предоставлять новый ЧИТАЮЩИЙ gRPC-сервис
`IdentityService` для справочника субъектов из каталога Keycloak:
`SearchSubjects(query, cursor, page_size)` → `repeated SubjectIdentity` +
`next_cursor`; `ResolveSubjects(subjects[])` → `repeated SubjectIdentity`. Новое
сообщение `SubjectIdentity` ДОЛЖНО (MUST) нести поля `subject` (канонический ключ =
`sub` UUID Keycloak), `username`, `email`, `display_name`, `enabled` и `found`
(булев признак «найден в каталоге»). Изменение ДОЛЖНО (MUST) быть аддитивным и
wire-совместимым: добавляется новый сервис и новые сообщения, существующие RPC
`AccessService`, `RoleAdminService`, читающего `IamAdminService` (в т.ч.
`ListSubjectsWithRoles`) и мутирующего `IamCatalogService` НЕ изменяются. После
правки `.proto` сгенерированные `*.pb.go` ДОЛЖНЫ (MUST) быть перегенерированы
(`buf generate`, `GOWORK=off`, пин `./tools`), а `gen:check` — зелёным.

#### Scenario: Аддитивное расширение контракта справочником

- **WHEN** в `proto/idm/v1` добавляется `IdentityService` с RPC `SearchSubjects`/
  `ResolveSubjects` и сообщение `SubjectIdentity`
- **THEN** существующие `AccessService`, `RoleAdminService`, `IamAdminService`
  (включая `ListSubjectsWithRoles`) и `IamCatalogService` остаются без изменений
  (wire-совместимо), `buf generate` обновляет `pkg/api/idm/**`, `gen:check` зелёный

#### Scenario: Канонический ключ субъекта — sub (UUID)

- **WHEN** возвращается `SubjectIdentity`
- **THEN** поле `subject` содержит канонический ключ субъекта `sub` (UUID Keycloak) —
  тот же, что в `subject_roles.subject` и `auth.Claims.Subject`; `preferred_username`
  возвращается отдельным полем `username` и ключом НЕ является

#### Scenario: Резолв отсутствующего субъекта помечается found=false

- **GIVEN** субъект `sub` есть в `subject_roles`, но в каталоге Keycloak его уже нет
- **WHEN** вызывается `ResolveSubjects([sub])`
- **THEN** в ответе присутствует `SubjectIdentity{subject:sub, found:false}` (запись
  не опускается), без раскрытия внутренних деталей

#### Scenario: Курсор поиска непрозрачен для клиента

- **WHEN** `SearchSubjects` возвращает `next_cursor`
- **THEN** `next_cursor` — непрозрачная строка (внутри закодирован offset Keycloak);
  пустой `next_cursor` означает отсутствие следующих страниц
