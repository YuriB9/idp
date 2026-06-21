# iam-administration Specification (delta)

## ADDED Requirements

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
