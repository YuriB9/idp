# iam-administration Specification

## Purpose
TBD - created by archiving change iam-roles-portal. Update Purpose after archive.
## Requirements
### Requirement: Читающий каталог ролей и прав в IDM

IDM ДОЛЖЕН (MUST) предоставлять read-only методы каталога RBAC через новый
gRPC-сервис `IamAdminService` в `proto/idm/v1`: `ListRoles` (все роли по `name`),
`ListPermissions` (все права как пары `action`/`resource`),
`GetRolePermissions(role)` (права конкретной роли). Методы ДОЛЖНЫ (MUST) читать
модель строго из Postgres БЕЗ побочных эффектов на кэш решений (никаких записей в
DragonflyDB). `id` роли наружу НЕ отдаётся — стабильный идентификатор роли есть её
`name`. Несуществующая роль в `GetRolePermissions` ДОЛЖНА (MUST) приводить к
`NotFound`; пустое имя роли — к `InvalidArgument`. При недоступности БД метод
ДОЛЖЕН (MUST) возвращать ошибку (fail-closed), а не пустой успешный ответ;
внутренние детали наружу НЕ раскрываются (деталь — в лог по ключу slog `err`).

#### Scenario: Листинг ролей и прав

- **GIVEN** в модели засеяны роли (`project-creator`, `owner:project:demo`,
  `iam-admin`) и права
- **WHEN** вызываются `ListRoles` и `ListPermissions`
- **THEN** возвращаются полные наборы ролей (по `name`) и прав (`action`,
  `resource`) без `id` и без обращения к кэшу решений

#### Scenario: Права конкретной роли

- **GIVEN** роль `iam-admin` имеет права `(read, iam:global)` и `(write, iam:global)`
- **WHEN** вызывается `GetRolePermissions("iam-admin")`
- **THEN** возвращаются ровно эти два права

#### Scenario: Несуществующая роль → NotFound

- **GIVEN** роли `no-such-role` нет в модели
- **WHEN** вызывается `GetRolePermissions("no-such-role")`
- **THEN** возвращается `NotFound`, без раскрытия внутренних деталей

#### Scenario: Недоступность БД при чтении → ошибка (fail-closed)

- **GIVEN** Postgres недоступен
- **WHEN** вызывается `ListRoles`
- **THEN** возвращается ошибка (а не пустой успешный список), деталь — только в лог

### Requirement: Листинг субъектов с ролями и ролей субъекта

IDM ДОЛЖЕН (MUST) предоставлять `ListSubjectsWithRoles` (субъекты с их ролями) и
`GetSubjectRoles(subject)` (роли одного субъекта). Перечисление субъектов ДОЛЖНО
(MUST) формироваться как `DISTINCT subject` из `subject_roles`: субъект известен
админке тогда и только тогда, когда у него есть хотя бы одна роль (реестра
пользователей нет; субъекты без ролей НЕ видны — это допустимо).
`ListSubjectsWithRoles` ДОЛЖЕН (MUST) использовать keyset-пагинацию по `subject`
(стабильный порядок ASC, `page_size` с серверным клампом, непрозрачный
`page_token`), а роли страницы собирать одним запросом (агрегирование по
субъекту), без N+1. `GetSubjectRoles` для субъекта без ролей ДОЛЖЕН (MUST)
возвращать пустой набор (НЕ `NotFound`); пустой `subject` — `InvalidArgument`.
Методы read-only, без побочных эффектов на кэш.

#### Scenario: Листинг субъектов с их ролями

- **GIVEN** `demo-user` имеет роли `project-creator` и `iam-admin`
- **WHEN** вызывается `ListSubjectsWithRoles` (первая страница)
- **THEN** возвращается запись `{subject: "demo-user", roles: [project-creator, iam-admin]}`
  и, при необходимости, непрозрачный `next_page_token`

#### Scenario: Keyset-страницы стабильны

- **GIVEN** субъектов больше, чем `page_size`
- **WHEN** запрашивается следующая страница по `next_page_token`
- **THEN** возвращаются субъекты строго после курсора (ASC по `subject`), без
  пропусков и дублей при конкурентных вставках

#### Scenario: Роли конкретного субъекта

- **WHEN** вызывается `GetSubjectRoles("demo-user")`
- **THEN** возвращается набор имён ролей субъекта

#### Scenario: Субъект без ролей → пусто, не NotFound

- **GIVEN** субъект `ghost` не имеет ни одной роли (его нет в `subject_roles`)
- **WHEN** вызывается `GetSubjectRoles("ghost")`
- **THEN** возвращается пустой набор ролей, без ошибки `NotFound`

### Requirement: Модель полномочий IAM-админки (read/write на iam:global)

Доступ к IAM-админке ДОЛЖЕН (MUST) контролироваться отдельными RBAC-действиями на
горизонтальном ресурсе `iam:global`: `read` — для всех читающих операций каталога
(роли, права, субъекты, роли субъекта) и `write` — для назначения/снятия ролей
субъектам. Действия проверяются через существующий `CheckAccess` (strict-match,
deny-by-default, fail-closed). Ни `read`, ни `write` НЕ ДОЛЖНЫ (MUST NOT) неявно
наследоваться из project-действий (`create`/`read`/`list`/`transfer`/… на
`project:<p>` права на `iam:global` не дают) и наоборот. Отсутствие явного гранта
ДОЛЖНО (MUST) приводить к `allowed=false`. Право `write` НЕ подразумевает `read`
автоматически на уровне модели — гранты выдаются явно роли админки.

#### Scenario: Грант read разрешает чтение каталога

- **GIVEN** субъект имеет грант `(read, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "read")`
- **THEN** возвращается `allowed=true`

#### Scenario: Нет гранта write — мутация запрещена (deny-by-default)

- **GIVEN** субъект имеет только `(read, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "write")`
- **THEN** возвращается `allowed=false`, без неявного наследования из `read` или
  project-прав

#### Scenario: Project-права не дают доступа к админке

- **GIVEN** субъект имеет полные права на `project:demo`, но без грантов на
  `iam:global`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "read")`
- **THEN** возвращается `allowed=false`

### Requirement: Управление назначением ролей субъектам с инвалидацией кэша

IDM ДОЛЖЕН (MUST) обслуживать выдачу и снятие ролей субъектам через существующие
идемпотентные `RoleAdminService.AssignRole`/`RevokeRole` (переиспользование, без
дублирования контракта). После КАЖДОЙ успешной мутации привязки IDM ДОЛЖЕН (MUST)
инвалидировать кэш решений по затронутому субъекту (`InvalidateSubject`), не
оставляя устаревший `allow`/`deny`. Повторная выдача уже имеющейся роли и снятие
отсутствующей связки ДОЛЖНЫ (MUST) завершаться успехом (идемпотентность);
несуществующая роль при выдаче ДОЛЖНА (MUST) приводить к `NotFound`; пустые
`subject`/`role` — к `InvalidArgument`. Читающие методы каталога НЕ ДОЛЖНЫ (MUST
NOT) иметь побочных эффектов на кэш.

#### Scenario: Назначение роли инвалидирует кэш субъекта

- **GIVEN** решение по субъекту закэшировано в DragonflyDB
- **WHEN** субъекту выдаётся роль через `AssignRole`
- **THEN** связка создаётся (идемпотентно) и кэш субъекта инвалидируется
  (`InvalidateSubject`), последующий `CheckAccess` отражает новый грант

#### Scenario: Идемпотентный повтор выдачи и снятия

- **GIVEN** субъект уже имеет роль `iam-admin`
- **WHEN** `AssignRole` вызывается повторно, затем `RevokeRole` дважды
- **THEN** каждая операция завершается успехом без ошибки (no-op при отсутствии
  изменений), кэш субъекта инвалидируется

#### Scenario: Несуществующая роль при выдаче → NotFound

- **WHEN** `AssignRole(subject, "no-such-role")`
- **THEN** возвращается `NotFound`, привязка не создаётся

