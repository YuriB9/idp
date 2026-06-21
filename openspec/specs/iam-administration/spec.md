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

### Requirement: Создание и удаление ролей каталога

IDM ДОЛЖЕН (MUST) поддерживать создание и удаление ролей каталога через
`IamCatalogService`: `CreateRole(name)` создаёт ПОЛЬЗОВАТЕЛЬСКУЮ роль
(`system=false`); `DeleteRole(name)` удаляет роль. Создание роли с уже
существующим именем ДОЛЖНО (MUST) приводить к `AlreadyExists` (UNIQUE-конфликт
имени; НЕ идемпотентно — повторное создание есть конфликт). Удаление
несуществующей роли ДОЛЖНО (MUST) приводить к `NotFound`. Удаление СИСТЕМНОЙ роли
ДОЛЖНО (MUST) приводить к `FailedPrecondition` (защита сидированных). Пустое имя
роли → `InvalidArgument`. Многошаговые записи выполняются в транзакции. После
успешного создания/удаления роли IDM ДОЛЖЕН (MUST) выполнить ШИРОКУЮ инвалидацию
кэша решений (поколение `idm:cache:gen` через `InvalidateAll`), а не точечную по
субъекту. При недоступности БД/кэша метод ДОЛЖЕН (MUST) возвращать ошибку
(fail-closed), деталь — в лог по ключу slog `err`, наружу не раскрывается.

#### Scenario: Создание пользовательской роли

- **WHEN** вызывается `CreateRole("reviewers")` для отсутствующего имени
- **THEN** роль создаётся с `system=false`, возвращается `Role{name:"reviewers",
  system:false}`, кэш решений инвалидируется широко (поколение)

#### Scenario: Повторное создание роли → AlreadyExists

- **GIVEN** роль `reviewers` уже существует
- **WHEN** вызывается `CreateRole("reviewers")`
- **THEN** возвращается `AlreadyExists` (UNIQUE-конфликт имени), новая роль не
  создаётся

#### Scenario: Удаление пользовательской роли

- **GIVEN** пользовательская роль `reviewers` существует
- **WHEN** вызывается `DeleteRole("reviewers")`
- **THEN** роль удаляется, кэш решений инвалидируется широко (поколение)

#### Scenario: Удаление системной роли запрещено → FailedPrecondition

- **GIVEN** роль `iam-admin` помечена `system=true`
- **WHEN** вызывается `DeleteRole("iam-admin")`
- **THEN** возвращается `FailedPrecondition`, роль не удаляется, кэш не трогается

#### Scenario: Удаление несуществующей роли → NotFound

- **WHEN** вызывается `DeleteRole("no-such-role")`
- **THEN** возвращается `NotFound`, без раскрытия внутренних деталей

#### Scenario: Пустое имя роли → InvalidArgument

- **WHEN** вызывается `CreateRole("")` или `DeleteRole("")`
- **THEN** возвращается `InvalidArgument`, обращения к БД не происходит

### Requirement: Каскадное удаление роли «в использовании»

IDM ДОЛЖЕН (MUST) при удалении ПОЛЬЗОВАТЕЛЬСКОЙ роли, назначенной субъектам (есть
строки `subject_roles`) и/или имеющей права (`role_permissions`), каскадно
снимать роль у ВСЕХ носителей и убирать её связки прав (FK `ON DELETE CASCADE`), а
затем выполнять ШИРОКУЮ инвалидацию кэша решений (затронуты все носители роли —
точечной инвалидации по субъекту НЕДОСТАТОЧНО). Системная роль при этом всё равно
защищена (см. удаление системной роли → `FailedPrecondition`), поэтому каскад
применяется только к пользовательским ролям.

#### Scenario: Удаление роли снимает её у всех носителей

- **GIVEN** пользовательская роль `reviewers` назначена `alice` и `bob` и имеет
  права
- **WHEN** вызывается `DeleteRole("reviewers")`
- **THEN** роль удаляется, привязки `alice`/`bob` и связки прав снимаются каскадно,
  кэш решений инвалидируется широко (поколение), у `alice`/`bob` не остаётся
  устаревший `allow`

### Requirement: Создание и удаление прав каталога

IDM ДОЛЖЕН (MUST) поддерживать `CreatePermission(action,resource)` (создаёт
ПОЛЬЗОВАТЕЛЬСКОЕ право, `system=false`) и `DeletePermission(action,resource)`.
Создание дубля пары `action`/`resource` ДОЛЖНО (MUST) приводить к `AlreadyExists`
(UNIQUE-конфликт пары). Удаление несуществующего права → `NotFound`; удаление
СИСТЕМНОГО права → `FailedPrecondition`. Пустые `action`/`resource` либо строки с
NUL/не-utf8 → `InvalidArgument`. `action`/`resource` — произвольные непустые строки
(каталог прав открытый; матчинг strict). Удаление права каскадно снимает его связки
`role_permissions` (FK `ON DELETE CASCADE`), затем IDM ДОЛЖЕН (MUST) выполнить
ШИРОКУЮ инвалидацию кэша. Записи — в транзакции; fail-closed при недоступности.

#### Scenario: Создание пользовательского права

- **WHEN** вызывается `CreatePermission("deploy","project:demo")` для отсутствующей
  пары
- **THEN** право создаётся с `system=false`, кэш инвалидируется широко

#### Scenario: Дубль пары action/resource → AlreadyExists

- **GIVEN** право `(read, iam:global)` уже существует
- **WHEN** вызывается `CreatePermission("read","iam:global")`
- **THEN** возвращается `AlreadyExists`, новое право не создаётся

#### Scenario: Удаление системного права запрещено → FailedPrecondition

- **GIVEN** право `(read, iam:global)` помечено `system=true`
- **WHEN** вызывается `DeletePermission("read","iam:global")`
- **THEN** возвращается `FailedPrecondition`, право не удаляется

#### Scenario: Невалидная пара права → InvalidArgument

- **WHEN** вызывается `CreatePermission("", "")` или со строкой, содержащей NUL
- **THEN** возвращается `InvalidArgument`, обращения к БД не происходит

### Requirement: Управление набором прав роли (attach/detach) с защитой системных

IDM ДОЛЖЕН (MUST) поддерживать `AttachPermission(role,action,resource)` и
`DetachPermission(role,action,resource)` для правки набора прав ПОЛЬЗОВАТЕЛЬСКОЙ
роли. Обе операции ДОЛЖНЫ (MUST) быть ИДЕМПОТЕНТНЫМИ: повторный attach уже
привязанного права и detach непривязанного завершаются успехом (`ON CONFLICT DO
NOTHING`/`DELETE`). Несуществующая роль или (для attach) несуществующее право
ДОЛЖНЫ (MUST) приводить к `NotFound` (право НЕ создаётся неявно). Attach/detach на
СИСТЕМНУЮ роль ДОЛЖНЫ (MUST) приводить к `FailedPrecondition` (состав прав
системной роли фиксирован сидированием); прикрепление СИСТЕМНОГО права к
ПОЛЬЗОВАТЕЛЬСКОЙ роли РАЗРЕШЕНО (защищается роль, не право). Пустые поля →
`InvalidArgument`. После успешной правки `role_permissions` IDM ДОЛЖЕН (MUST)
выполнить ШИРОКУЮ инвалидацию кэша (затронуты ВСЕ носители роли) и вернуть
актуальный набор прав роли (`RolePermissions`). Записи — в транзакции.

#### Scenario: Attach права к пользовательской роли

- **GIVEN** пользовательская роль `reviewers` и право `(read, iam:global)`
  существуют
- **WHEN** вызывается `AttachPermission("reviewers","read","iam:global")`
- **THEN** связь создаётся, возвращается `RolePermissions` с этим правом, кэш
  инвалидируется широко (поколение)

#### Scenario: Идемпотентный повтор attach и detach

- **GIVEN** право уже привязано к роли `reviewers`
- **WHEN** `AttachPermission` вызывается повторно, затем `DetachPermission` дважды
- **THEN** каждая операция завершается успехом (no-op при отсутствии изменений),
  кэш инвалидируется широко

#### Scenario: Attach/detach на системную роль запрещён → FailedPrecondition

- **GIVEN** роль `iam-admin` помечена `system=true`
- **WHEN** вызывается `AttachPermission("iam-admin",...)` или `DetachPermission(...)`
- **THEN** возвращается `FailedPrecondition`, набор прав системной роли не меняется

#### Scenario: Attach несуществующего права → NotFound

- **GIVEN** роль `reviewers` существует, права `(x, y)` нет
- **WHEN** вызывается `AttachPermission("reviewers","x","y")`
- **THEN** возвращается `NotFound`, право не создаётся неявно, связь не создаётся

#### Scenario: Attach к несуществующей роли → NotFound

- **WHEN** вызывается `AttachPermission("no-such-role","read","iam:global")`
- **THEN** возвращается `NotFound`

### Requirement: Широкая инвалидация кэша при структурных мутациях каталога

IDM ДОЛЖЕН (MUST) после любой структурной мутации каталога (`CreateRole`,
`DeleteRole`, `CreatePermission`, `DeletePermission`, `AttachPermission`,
`DetachPermission`) после успешного commit транзакции выполнять ШИРОКУЮ инвалидацию кэша
решений — инкремент поколения `idm:cache:gen` (`InvalidateAll`), т.к. правка
`role_permissions`/удаление роли/права затрагивает ВСЕХ носителей роли. Точечной
инвалидации по субъекту (`InvalidateSubject`) для структурных мутаций НЕДОСТАТОЧНО
и она НЕ ДОЛЖНА (MUST NOT) применяться вместо широкой. Точечная инвалидация
остаётся только у `AssignRole`/`RevokeRole`. Читающие методы каталога НЕ ДОЛЖНЫ
(MUST NOT) иметь побочных эффектов на кэш. Если структурная мутация откатилась
(ошибка транзакции), кэш НЕ трогается.

#### Scenario: Структурная мутация бампит поколение

- **WHEN** выполняется любая структурная мутация каталога (например `AttachPermission`)
- **THEN** поколение `idm:cache:gen` инкрементируется (широкая инвалидация), все
  ранее закэшированные решения становятся устаревшими

#### Scenario: Assign/revoke остаются точечными

- **WHEN** субъекту назначается/снимается роль через `RoleAdminService`
- **THEN** инвалидируется только кэш затронутого субъекта (`InvalidateSubject`),
  поколение НЕ бампится

#### Scenario: Откат мутации не трогает кэш

- **GIVEN** транзакция структурной мутации завершается ошибкой (откат)
- **THEN** инвалидация кэша не выполняется (ни поколение, ни субъект)

### Requirement: Исходящая интеграция IDM с Keycloak Admin REST (чтение) под SSRF-guard

Сервис IDM ДОЛЖЕН (MUST) реализовать слой `internal/identity` — клиент Keycloak
Admin REST для ЧТЕНИЯ каталога пользователей realm `idp`: поиск по строке
(username/email/имя) с offset-пагинацией и резолв набора `sub` → идентичность
(`{subject, username, email, display_name, enabled, found}`). Доступ ДОЛЖЕН (MUST)
выполняться от сервис-аккаунта (confidential-client с realm-management ролями
`view-users`/`query-users`), токен — по `client_credentials`. КАЖДЫЙ исходящий вызов
(получение токена и Admin REST) ДОЛЖЕН (MUST) проходить через `pkg/ssrf` (ValidateURL
на конфигурации + GuardedDialContext в транспорте) и `pkg/httpclient`; `SSRF_DISABLED`
ДОЛЖЕН (MUST) включаться ТОЛЬКО локально. Секрет сервис-аккаунта НЕ ДОЛЖЕН
(MUST NOT) логироваться или раскрываться наружу; сырые ошибки Keycloak НЕ ДОЛЖНЫ
(MUST NOT) попадать клиенту (деталь — в лог по ключу slog `err`). При недоступности/
таймауте/5xx Keycloak или ошибке выдачи токена операция ДОЛЖНА (MUST) завершаться
`codes.Unavailable` (fail-closed для ручки).

#### Scenario: Поиск пользователей по строке

- **GIVEN** сервис-аккаунт настроен, Keycloak доступен
- **WHEN** вызывается `SearchSubjects(query="iv", page_size=20)`
- **THEN** возвращается страница `SubjectIdentity[]` совпадений и `next_cursor` для
  следующей страницы (offset Keycloak инкапсулирован в курсоре)

#### Scenario: Резолв набора sub

- **WHEN** вызывается `ResolveSubjects([sub1, sub2])` для существующих пользователей
- **THEN** возвращаются их идентичности с `found=true`, `username`/`email`/
  `display_name`/`enabled` заполнены

#### Scenario: Keycloak недоступен → Unavailable (без сырых деталей)

- **GIVEN** Keycloak недоступен (таймаут/5xx) ИЛИ выдача токена не удалась
- **WHEN** вызывается `SearchSubjects`/`ResolveSubjects`
- **THEN** возвращается `codes.Unavailable`, деталь — только в лог по ключу `err`,
  секрет сервис-аккаунта в сообщении/логе отсутствует

#### Scenario: Невалидный ввод поиска → InvalidArgument

- **WHEN** вызывается `SearchSubjects` с пустым/слишком коротким `query` или с
  `page_size` сверх допустимого максимума
- **THEN** возвращается `codes.InvalidArgument`, обращения к Keycloak не делается

### Requirement: Кэш идентичностей с TTL в отдельном namespace

IDM ДОЛЖЕН (MUST) кэшировать результаты резолва и поиска идентичностей в DragonflyDB
в отдельном namespace `idm:identity:*` (ключ резолва — от `sub`, ключ поиска — от
нормализованных `query`/offset/`page_size`) с коротким TTL. Кэш ДОЛЖЕН (MUST) быть
независим от decision-cache RBAC (см. capability `access-control`): операции
справочника НЕ трогают `idm:cache:gen`/`InvalidateSubject`. Параллельные одинаковые
запросы к Keycloak ДОЛЖНЫ (MUST) объединяться (singleflight) против стампеда.

#### Scenario: Повторный резолв обслуживается из кэша

- **GIVEN** `ResolveSubjects([sub])` уже выполнен, TTL не истёк
- **WHEN** повторно вызывается `ResolveSubjects([sub])`
- **THEN** ответ берётся из `idm:identity:resolve:<sub>` без обращения к Keycloak

#### Scenario: Кэш идентичностей не затрагивает decision-cache

- **WHEN** выполняются поиск/резолв и запись в `idm:identity:*`
- **THEN** поколение `idm:cache:gen` и точечная инвалидация по субъекту не
  затрагиваются (проверяется тестом на miniredis)

### Requirement: Обогащение списка субъектов-с-ролями идентичностями и пометка осиротевших

Существующая выдача субъектов-с-ролями ДОЛЖНА (MUST) обогащаться идентичностями
(резолв через `IdentityService`, ADR-0014 `ListSubjectsWithRoles`) ТОЛЬКО когда
вызывающий держит `(read, iam:directory)`; иначе ответ остаётся «сырым» (только
`subject` + роли). Субъекты, у которых роль есть, но в каталоге Keycloak их нет
(«осиротевшие»), ДОЛЖНЫ (MUST) помечаться `found=false` и отдаваться как raw `sub`
(не опускаться). При недоступности Keycloak обогащение ДОЛЖНО (MUST) ДЕГРАДИРОВАТЬ:
список субъектов-с-ролями отдаётся без идентичностей, управление ролями по сырому
`sub` НЕ ломается. `ListSubjectsWithRoles` на уровне proto НЕ изменяется —
обогащение выполняется композицией на периметре (см. capability `perimeter-rest`).

#### Scenario: Обогащение при наличии права directory

- **GIVEN** вызывающий держит `(read, iam:global)` и `(read, iam:directory)`,
  Keycloak доступен
- **WHEN** запрашивается список субъектов-с-ролями
- **THEN** каждому субъекту с найденной идентичностью добавлены `username`/`email`/
  `display_name`/`enabled`, `found=true`

#### Scenario: Осиротевший субъект помечен found=false

- **GIVEN** субъект `sub` есть в `subject_roles`, но в каталоге Keycloak его нет
- **WHEN** запрашивается обогащённый список
- **THEN** субъект присутствует как raw `sub` с `found=false` (пометка «нет в
  каталоге»), его роли отдаются как обычно

#### Scenario: Деградация при недоступном Keycloak

- **GIVEN** вызывающий держит `(read, iam:directory)`, но Keycloak недоступен
- **WHEN** запрашивается список субъектов-с-ролями
- **THEN** список ролей отдаётся без идентичностей (деградация), управление ролями по
  сырому `sub` остаётся доступным

