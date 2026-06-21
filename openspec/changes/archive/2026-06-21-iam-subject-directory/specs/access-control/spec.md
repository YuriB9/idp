# access-control Specification (delta)

## ADDED Requirements

### Requirement: Канонический ключ субъекта — sub (UUID Keycloak)

Каноническим ключом субъекта RBAC ДОЛЖЕН (MUST) быть `sub` из JWT (UUID
пользователя Keycloak) — то самое значение, которое `auth.Claims.Subject` кладёт в
контекст и которое хранится в `subject_roles.subject`. `preferred_username` НЕ
ДОЛЖЕН (MUST NOT) использоваться как ключ авторизации (он изменяем и не гарантирует
уникальности во времени). Резолв `sub` → человекочитаемое имя выполняется ТОЛЬКО
для отображения и НЕ ДОЛЖЕН (MUST NOT) влиять на решение `CheckAccess` (решение
остаётся по `sub`, strict-match, deny-by-default). JWT-правила pkg НЕ ДОЛЖНЫ
(MUST NOT) ослабляться (без protocol-mapper, подменяющего `sub`).

#### Scenario: Решение доступа по sub, не по имени

- **GIVEN** субъект с `sub=<UUID>` привязан к роли с правом `(read, iam:global)`
- **WHEN** вызывается `CheckAccess(<UUID>, iam:global, read)`
- **THEN** ответ `allowed=true`; резолв имени для этого `sub` на решение не влияет

#### Scenario: Сведение рассогласования demo-user/dev

- **GIVEN** реальный пользователь Keycloak `dev` имеет детерминированный `sub`
  (UUID), а локальный disabled-режим выставляет `AUTH_DISABLED_SUBJECT`=этот UUID
- **WHEN** сиды RBAC мигрированы со строки `demo-user` на UUID `dev`
- **THEN** и реальный вход через Keycloak, и локальный disabled-режим дают один и тот
  же канонический `sub`, под который засеяны роли

### Requirement: Ресурс iam:directory с действием read для просмотра PII

Просмотр реальных идентичностей пользователей ДОЛЖЕН (MUST) авторизоваться
действием `read` на НОВОМ горизонтальном ресурсе `iam:directory` — отдельном от
`read`/`write`/`manage` на `iam:global` (наименьшие привилегии). Под идентичностью
здесь понимается PII: `username`/`email`/`display_name`. Действие ДОЛЖНО (MUST) укладываться в
существующую модель (action + resource, strict-match, deny-by-default) без неявного
наследования. Без права `(read, iam:directory)` обогащение списка субъектов
идентичностями выполняться НЕ ДОЛЖНО (MUST NOT) — PII не раскрывается носителю лишь
`(read, iam:global)`.

#### Scenario: Просмотр PII требует iam:directory

- **GIVEN** субъект держит `(read, iam:global)`, но НЕ держит `(read, iam:directory)`
- **WHEN** запрашивается справочник/обогащение PII
- **THEN** PII не возвращается (для справочника — отказ авторизации; для списка
  субъектов — «сырой» ответ без идентичностей)

#### Scenario: Раздача права directory отдельно от global

- **GIVEN** роль `iam-admin` засеяна правами `(read, iam:global)` и
  `(read, iam:directory)`
- **WHEN** проверяется `CheckAccess(iam-admin-субъект, iam:directory, read)`
- **THEN** ответ `allowed=true`; право на каталог ролей и право на PII независимы

### Requirement: Отдельный кэш идентичностей, не влияющий на decision-cache

Кэш идентичностей справочника ДОЛЖЕН (MUST) располагаться в ОТДЕЛЬНОМ namespace
DragonflyDB (`idm:identity:*`) с TTL и НЕ ДОЛЖЕН (MUST NOT) затрагивать decision-cache
RBAC: операции справочника (поиск/резолв) НЕ ДОЛЖНЫ (MUST NOT) читать или менять
поколение `idm:cache:gen` и НЕ ДОЛЖНЫ (MUST NOT) вызывать `InvalidateAll`/
`InvalidateSubject`. Инвалидация кэша идентичностей ДОЛЖНА (MUST) происходить по
TTL. Записи RBAC (assign/revoke, структурные мутации) НЕ ДОЛЖНЫ (MUST NOT) трогать
кэш идентичностей.

#### Scenario: Поиск/резолв не затрагивают decision-cache

- **GIVEN** в decision-cache есть закэшированные решения (поколение `idm:cache:gen`)
- **WHEN** выполняются `SearchSubjects`/`ResolveSubjects`
- **THEN** поколение `idm:cache:gen` не меняется, точечная инвалидация по субъекту не
  вызывается, решения остаются валидными

#### Scenario: Истечение TTL кэша идентичностей

- **GIVEN** идентичность субъекта закэширована в `idm:identity:resolve:<sub>`
- **WHEN** истекает TTL
- **THEN** следующий резолв обращается к Keycloak заново и обновляет запись, не
  затрагивая decision-cache
