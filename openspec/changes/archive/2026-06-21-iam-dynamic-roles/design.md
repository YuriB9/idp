## Context

RBAC реализован в IDM (ADR-0010): `roles(id,name,created_at)`,
`permissions(id,action,resource — UNIQUE(action,resource))`, `role_permissions`
(каскад при удалении роли/права), `subject_roles(subject,role_id — каскад при
удалении роли)`. `AccessService.CheckAccess` — strict-match, deny-by-default,
fail-closed; кэш решений в DragonflyDB (поколение `idm:cache:gen` через
`InvalidateAll` + точечный `InvalidateSubject`); `RoleAdminService.AssignRole/
RevokeRole` идемпотентны и зовут `InvalidateSubject`.

IAM-админка фазы 1 (ADR-0014) добавила читающий `IamAdminService` (`ListRoles`,
`ListPermissions`, `GetRolePermissions`, `ListSubjectsWithRoles`,
`GetSubjectRoles`), горизонтальный ресурс `iam:global` с раздельными действиями
`read`/`write`, обобщённый gateway-helper `authorizeResource(w,r,resource,action)`
(`authorize(project,action)` — тонкая обёртка) и раздел портала «Роли и доступы»
(read-only по каталогу + assign/revoke). Но каталог статичен: создание/удаление
ролей и прав, правка `role_permissions` — только миграциями.

Этот change даёт из портала/периметра CRUD каталога. Ограничения обязательны:
fail-closed (недоступный/пустой IDM → отказ, не passthrough), без раскрытия
внутренних ошибок клиенту, многошаговые записи в транзакциях, комментарии в коде
только на русском, миграции goose обратимые (пин `./tools`, `GOWORK=off`,
`migrate-idm`). Ключевой инвариант кэша: правка `role_permissions`/удаление роли
затрагивает ВСЕХ носителей роли — нужна ШИРОКАЯ инвалидация (поколение), точечной
по субъекту НЕДОСТАТОЧНО.

## Goals / Non-Goals

**Goals:**
- Структурные мутации каталога IAM (create/delete роли, attach/detach право роли,
  create/delete право) в `proto/idm/v1` (аддитивно) и на периметре (`/iam/*`).
- Отдельное более привилегированное действие `manage` на `iam:global` для всех
  структурных мутаций (fail-closed, `CheckAccess` перед КАЖДОЙ ручкой).
- Признак `system` у ролей/прав и защита сидированных от удаления/правки.
- ШИРОКАЯ инвалидация кэша (поколение) после любой структурной мутации; точечная
  (`InvalidateSubject`) остаётся только у assign/revoke; чтение — без эффектов.
- Каскадная семантика удаления роли «в использовании» + широкая инвалидация.
- Расширение раздела портала «Роли и доступы» (CRUD каталога, read-only системных).
- Локальный seed `(manage, iam:global)` для `iam-admin` и backfill `system`.

**Non-Goals:**
- Иерархии ролей, wildcard/скоупы в `resource`, условные политики (ABAC) — матчинг
  остаётся strict.
- Управление пользователями / реальный OIDC realm / Keycloak admin (субъекты —
  строки `subject` из JWT).
- Аудит-лог изменений ролей/прав (кто/когда) — отдельный change.
- Версионирование/одобрение изменений каталога, импорт/экспорт политик.
- Переименование роли/права (rename) — вне scope (RPC не вводится).

## Decisions

### 1. Модель полномочий: отдельное действие `manage` на `iam:global`

Выбрано: ТРЕТЬЕ действие `manage` на горизонтальном `iam:global` для ВСЕХ
структурных мутаций каталога (create/delete роли, attach/detach право, create/delete
право) — отдельно от `read` (чтение) и `write` (assign/revoke). Укладывается в
существующую модель (action + resource, strict-match, deny-by-default).

Обоснование (security): менять САМ каталог прав привилегированнее, чем назначить
существующую роль. Раздельность даёт наименьшие привилегии и три уровня доверия:
аудитор — `read`; оператор назначений — `write`; администратор каталога — `manage`.
`manage` НЕ подразумевает `write`/`read` на уровне модели (никакого неявного
наследования — как `write` не подразумевает `read` в ADR-0014). Гранты выдаются
роли админки ЯВНО через сидирование (`iam-admin` получает `read`+`write`+`manage`).

Выбор имени `manage` (а не `admin`): `manage` — глагол действия, согласован с
`read`/`write` (тоже глаголы); `admin` смешивался бы с понятием «администратор» и
ресурсом. 

Альтернативы (отклонены): переиспользовать `write` для структурных мутаций
(стирает границу «назначить роль» vs «переписать каталог прав» — оператор
назначений неожиданно смог бы создавать/удалять роли; нарушает наименьшие
привилегии); неявное наследование `manage ⊃ write ⊃ read` в модели (привилегия
утекла бы скрыто, против strict-match deny-by-default ADR-0010); per-endpoint
действия (избыточная гранулярность для MVP).

### 2. Точки CheckAccess (fail-closed)

gateway вызывает `CheckAccess` ПЕРЕД проксированием КАЖДОЙ структурной ручки:
`POST/DELETE /iam/roles*`, `POST/DELETE /iam/roles/{role}/permissions`,
`POST/DELETE /iam/permissions` → `CheckAccess(subject, "iam:global", "manage")`.
Читающие `/iam/*` остаются под `read`, assign/revoke — под `write` (без
изменений, ADR-0014). Отказ ИЛИ недоступность/ошибка IDM → `403` (fail-closed),
запрос в `IamCatalogService` НЕ проксируется; деталь — только в лог по ключу slog
`err`. `subject` берётся из claims (`auth.ClaimsFromContext`). Helper —
существующий `authorizeResource(w,r,"iam:global","manage")` (новых helper'ов нет).

### 3. Форма мутирующего контракта (proto/idm/v1, аддитивно)

Новый сервис `IamCatalogService` (отдельно от читающего `IamAdminService` и от
`RoleAdminService` мутаций привязок):

```
service IamCatalogService {
  rpc CreateRole(CreateRoleRequest) returns (CreateRoleResponse);
  rpc DeleteRole(DeleteRoleRequest) returns (DeleteRoleResponse);
  rpc CreatePermission(CreatePermissionRequest) returns (CreatePermissionResponse);
  rpc DeletePermission(DeletePermissionRequest) returns (DeletePermissionResponse);
  rpc AttachPermission(AttachPermissionRequest) returns (AttachPermissionResponse);
  rpc DetachPermission(DetachPermissionRequest) returns (DetachPermissionResponse);
}
// сообщения IamAdminService РАСШИРЯЮТСЯ (аддитивно, новый номер поля):
message Role        { string name = 1; bool system = 2; }
message Permission  { string action = 1; string resource = 2; bool system = 3; }
message RolePermissions { string role = 1; repeated Permission permissions = 2; }
// CreateRole(name) → Role; дубль имени → AlreadyExists
// DeleteRole(name) → пусто; system → FailedPrecondition; нет → NotFound; каскад
// CreatePermission(action,resource) → Permission; дубль пары → AlreadyExists
// DeletePermission(action,resource) → пусто; system → FailedPrecondition; нет → NotFound
// AttachPermission(role,action,resource) → RolePermissions; идемпотентно;
//   роль/право нет → NotFound; system-роль → FailedPrecondition
// DetachPermission(role,action,resource) → RolePermissions; идемпотентно;
//   роль нет → NotFound; system-роль → FailedPrecondition
```

Выбор НОВОГО сервиса (а не добавления RPC в `IamAdminService`): структурные
мутации несут иную авторитетность (`manage`) и иной blast-radius (широкая
инвалидация), чем чтение (`read`) — разделение на уровне контракта делает границу
полномочий явной и упрощает раздачу прав. Читающий `IamAdminService` и
assign/revoke `RoleAdminService` переиспользуются без изменений.

Расширение `Role`/`Permission` полем `system` — аддитивно (новый номер поля),
wire-совместимо; читающие RPC (`ListRoles`/`ListPermissions`/`GetRolePermissions`)
начинают отдавать `system`, что нужно UI для read-only бейджа. Идентификатор роли —
`name` (как в ADR-0014), право — пара `action`/`resource`; `id` (UUID) наружу не
отдаётся.

### 4. Признак `system` и защита сидированных ролей/прав

Выбрано: колонки `roles.system` и `permissions.system` (`boolean NOT NULL DEFAULT
false`) — а не deny-list имён в коде. Обоснование: deny-list хрупок (дрейфует от
сидирования, не виден в данных, не отдаётся UI); колонка — единый источник правды,
отдаётся в контракт, проверяется в репозитории в той же транзакции.

Backfill (обратимая миграция): ВСЕ роли и права, существующие на момент миграции,
помечаются `system=true` — в текущем состоянии каталог сидирован только миграциями
(`project-creator`, `owner:project:*`, `iam-admin` и их права), поэтому «существует
до этого change → системное» — корректное и простое правило. Создаваемые далее
через API — `system=false`.

Что запрещено для системных (через API → `ErrPrecondition` → `422`):
- `DeleteRole` системной роли;
- `DeletePermission` системного права;
- `AttachPermission`/`DetachPermission` на системную РОЛЬ (правка набора прав
  системной роли) — её состав фиксирован сидированием.
Прикрепление СИСТЕМНОГО права к ПОЛЬЗОВАТЕЛЬСКОЙ роли — разрешено (защищается роль,
а не право: пользовательскую роль можно собирать из любых существующих прав).
Rename — вне scope (RPC нет).

Код `422` (`FailedPrecondition`), а не `403`: вызывающий АВТОРИЗОВАН (имеет
`manage`) — отказывает не авторизация, а предусловие ресурса «не системный».
Согласуется с ADR-0012/0013 (предусловие → `FailedPrecondition` → 422), отделяя
«нет права» (403) от «ресурс защищён» (422).

### 5. Широкая инвалидация кэша при структурной мутации

Инвариант: правка `role_permissions` (attach/detach), удаление роли (каскад
`subject_roles`) и удаление права (каскад `role_permissions`) меняют решения для
ВСЕХ носителей затронутой роли — поимённый список носителей при правке прав даже
не запрашивается. Поэтому точечной `InvalidateSubject` НЕДОСТАТОЧНО.

Решение: КАЖДАЯ структурная мутация после успешного commit вызывает
`InvalidateAll` (инкремент поколения `idm:cache:gen`) — все закэшированные решения
становятся устаревшими разом, без устаревшего `allow`/`deny`. Это дешёвая
O(1)-операция (бамп счётчика), цена — холодный кэш после структурной правки
(структурные правки редки). assign/revoke остаются точечными
(`InvalidateSubject`), чтение — без эффектов на кэш.

Порядок: инвалидация ПОСЛЕ commit транзакции (как события после commit, ADR-0004) —
если запись откатилась, кэш не трогаем; если бамп поколения упал, ошибка
возвращается вызывающему (структурная мутация идемпотентна на повтор create→409
не приведёт только если уже создано; повтор бампа поколения безопасен).

### 6. Семантика удаления роли «в использовании»

Выбрано: КАСКАД. FK `subject_roles.role_id` и `role_permissions.role_id` уже
`ON DELETE CASCADE` — удаление роли снимает её у всех носителей и убирает её
связки прав; затем ШИРОКАЯ инвалидация (решение 5). Системная роль при этом всё
равно защищена (решение 4 → 422), так что каскад применим только к
пользовательским ролям.

Обоснование (vs guard «запретить удаление роли в использовании» → 422): админская
интенция удалить роль явная; FK уже реализует каскад без доп. кода; широкая
инвалидация и так покрывает всех затронутых; guard потребовал бы доп. запрос
подсчёта носителей и оставил бы «залипшие» роли, которые нельзя удалить, не
отозвав вручную у каждого. Каскад проще и предсказуемее. Документируется явно
(удаление роли снимает её у всех субъектов).

### 7. Идемпотентность и коды мутаций

- `CreateRole(name)`: успех → `201`/`Role{name,system:false}`; дубль имени →
  `AlreadyExists` → `409` (НЕ идемпотентно — создание явный акт, повтор = конфликт).
- `DeleteRole(name)`: успех → `200` (каскад); отсутствует → `NotFound` → `404`;
  системная → `FailedPrecondition` → `422`. Выбор `404` (а не идемпотентного 200,
  как у revoke): структурное удаление каталога — явный администраторский акт,
  удаление несуществующей сущности достойно сигнала клиенту (в отличие от
  идемпотентной привязки revoke).
- `CreatePermission(action,resource)`: успех → `201`; дубль пары → `409`.
  Произвольные `action`/`resource` допустимы (free-form строки, валидация
  непустоты/utf8/без NUL → иначе `400`) — каталог прав открытый по дизайну.
- `DeletePermission(action,resource)`: успех → `200` (каскад detach из всех
  ролей); отсутствует → `404`; системное → `422`.
- `AttachPermission(role,action,resource)`: ИДЕМПОТЕНТНО — повтор уже привязанного
  → `200` (no-op, `ON CONFLICT DO NOTHING`); роль/право нет → `404`; системная
  роль → `422`. Возвращает `RolePermissions` (актуальный набор прав роли) для UI.
- `DetachPermission(role,action,resource)`: ИДЕМПОТЕНТНО — detach непривязанного
  → `200`; роль нет → `404`; системная роль → `422`. Возвращает `RolePermissions`.

Различие create (409 на дубль) vs attach (200 на повтор): create вводит НОВУЮ
сущность (конфликт уникальности значим), attach устанавливает СВЯЗЬ (желаемое
конечное состояние «связь есть» достигнуто — идемпотентность удобнее UI и
безопаснее на ретрае). Все коды документируются в OpenAPI для Spectral/конформанса.

### 8. Периметр (REST) и маппинг ошибок

Ручки (ADR-0009), все под `manage`:
- `POST /iam/roles` тело `{name}` → `201` `{name, system}`; `409`/`400`/`403`.
- `DELETE /iam/roles/{role}` → `200` `{name}`; `404`/`422`/`403`.
- `POST /iam/roles/{role}/permissions` тело `{action,resource}` → `200`
  `RolePermissions`; `404`/`422`/`400`/`403` (attach идемпотентно).
- `DELETE /iam/roles/{role}/permissions?action=&resource=` → `200`
  `RolePermissions`; `404`/`422`/`400`/`403` (detach идемпотентно). Пара в query
  (а не в теле DELETE): `action`/`resource` — идентификатор связи, query-параметры
  конформны и проще для Schemathesis, чем тело у DELETE.
- `POST /iam/permissions` тело `{action,resource}` → `201` `Permission`;
  `409`/`400`/`403`.
- `DELETE /iam/permissions?action=&resource=` → `200` `{action,resource}`;
  `404`/`422`/`400`/`403`.

Тела ответов несут актуальное состояние (роль/право/набор прав роли) для рантайм-
валидации zod `.parse` и обновления TanStack-кэша (как `setOwners`/assign в
ADR-0014). Маппинг через существующий `httpFromGRPC` (reuse ADR-0012/0013):
`PermissionDenied→403`, `NotFound→404`, `Aborted`/`AlreadyExists→409`,
`FailedPrecondition→422`, `InvalidArgument→400`, прочее→500 (без деталей). Новых
правил `httpFromGRPC` не требуется. Внутренние ошибки наружу НЕ раскрываются.

### 9. IDM repository/usecase: транзакции и слои

- **repository (pgx):** `CreateRole`/`DeleteRole`/`CreatePermission`/
  `DeletePermission`/`AttachPermission`/`DetachPermission`. Каждый многошаговый
  метод — в транзакции. UNIQUE-violation (имя роли, пара `action`/`resource`) →
  `errs.ErrConflict`; проверка `system` в той же транзакции (`SELECT ... FOR
  обновление не нужен — флаг неизменяем`) → `errs.ErrPrecondition`; цель не
  найдена → `errs.ErrNotFound`; пустые/битые входы — отсекаются на уровне
  usecase/grpc → `ErrValidation`. Attach/detach — `INSERT ... ON CONFLICT DO
  NOTHING` / `DELETE` (идемпотентно).
- **usecase (catalog-manager):** обёртка, вызывающая repo-мутацию и ПОСЛЕ commit —
  широкую инвалидацию `cache.InvalidateAll()` (поколение). Reuse существующего
  `RoleManager` для assign/revoke не трогаем (он точечный). Чтение каталога
  (`IamAdminService`) — read-фасад без эффектов на кэш (ADR-0014), теперь отдаёт
  `system`.
- **grpc (`iamCatalogServer`):** валидация входов (пустые поля → `InvalidArgument`),
  маппинг `ErrConflict→AlreadyExists`, `ErrPrecondition→FailedPrecondition`,
  `ErrNotFound→NotFound`, `ErrValidation→InvalidArgument`, ошибка БД/кэша →
  `Unavailable` (fail-closed), деталь в лог по ключу slog `err`.

### 10. Локальный seed и миграции

Обратимые goose-миграции IDM (пин `./tools`, `GOWORK=off`, `migrate-idm`):
1. `0007_roles_permissions_system_flag.sql`: `ALTER TABLE roles/permissions ADD
   COLUMN system boolean NOT NULL DEFAULT false`; `UPDATE ... SET system=true`
   для всех существующих строк (сидированные). `Down`: `DROP COLUMN system`.
2. `0008_seed_iam_manage_demo.sql`: право `(manage, iam:global)` (`system=true`),
   привязка к роли `iam-admin` (`ON CONFLICT DO NOTHING`). `demo-user` уже имеет
   `iam-admin` (0006), поэтому получает `manage` транзитивно. `Down`: снять право
   с роли и удалить право. Только стенд docker-compose, не прод.

## Поток вызовов (структурная мутация)

Распределённого workflow/Temporal нет (нет провизии внешних ресурсов) — синхронные
gRPC-вызовы периметр↔IDM.

Пример — attach права к роли:
```
Портал → gateway: POST /iam/roles/{role}/permissions { action, resource }
gateway → IDM(Access): CheckAccess(caller, "iam:global", "manage")
  deny/недоступен → 403 (fail-closed), наружу не проксируем
  allow → gateway → IDM(IamCatalog): AttachPermission(role, action, resource)
            usecase: tx { проверка system-роли (ErrPrecondition→422);
                          роль/право существуют (ErrNotFound→404);
                          INSERT role_permissions ON CONFLICT DO NOTHING }  // commit
                     cache.InvalidateAll()   // ШИРОКО: бамп idm:cache:gen
            возвращает RolePermissions(role, permissions[])
gateway → Портал: 200 RolePermissions  (zod .parse)
```

Пример — удаление роли «в использовании»:
```
Портал → gateway: DELETE /iam/roles/{role}
gateway → IDM(Access): CheckAccess(caller, "iam:global", "manage") → allow
gateway → IDM(IamCatalog): DeleteRole(role)
            usecase: tx { роль системная? → ErrPrecondition→422;
                          роль есть? нет → ErrNotFound→404;
                          DELETE roles → FK CASCADE снимает subject_roles +
                          role_permissions у всех носителей }  // commit
                     cache.InvalidateAll()   // ШИРОКО (затронуты все носители)
gateway → Портал: 200 { name }  (zod .parse)
```

## Risks / Trade-offs

- **[Структурная мутация без права утечёт]** → КАЖДАЯ ручка под
  `CheckAccess(manage, iam:global)` (fail-closed); недоступность IDM → 403, не
  passthrough; тесты gateway покрывают deny→403 и IDM-недоступен→403.
- **[Удаление системной роли сломало бы платформу]** → колонка `system` +
  guard в транзакции (422); backfill помечает сидированные; тесты repository
  (integration) и usecase покрывают защиту.
- **[Устаревший `allow` после правки `role_permissions`]** → ШИРОКАЯ инвалидация
  поколением после КАЖДОЙ структурной мутации; тест usecase/cache (miniredis)
  проверяет, что структурная мутация бампит поколение, а assign/revoke — точечно.
- **[Каскадное удаление роли тихо снимает её у всех]** → задокументировано (UI
  предупреждает; роль read-only, если системная); широкая инвалидация исключает
  залипший `allow`.
- **[Дрейф контракта proto↔OpenAPI↔TS]** → `gen:check` (buf + OpenAPI + TS +
  public-копия) в CI; Spectral (description/operationId/коды) + Schemathesis-
  конформанс; рантайм zod `.parse` ловит расхождение на границе.
- **[Регресс существующих /iam-ручек read/write]** → `authorizeResource`
  переиспользуется (новых helper'ов нет); read/write-ручки не трогаются; старые
  тесты gateway/web зелёные.
- **[Открытый каталог прав (произвольный resource)]** → допустимо по дизайну
  (нет реестра ресурсов); валидация непустоты/utf8/без NUL → 400; матчинг strict
  (нет wildcard), поэтому «мусорное» право безвредно (никому не назначено).

## Migration Plan

1. Ветка `change/iam-dynamic-roles` от `master` (прямые коммиты в master
   запрещены).
2. `proto/idm/v1`: `IamCatalogService` + поле `system` в `Role`/`Permission`;
   `make proto` (`buf`, `GOWORK=off`) → `pkg/api/idm/**`.
3. БД: `0007_roles_permissions_system_flag.sql` (колонки + backfill, обратимо),
   `0008_seed_iam_manage_demo.sql` (seed `manage`, обратимо); `migrate-idm`.
4. IDM: repository (мутации в транзакциях, guard `system`, UNIQUE→ErrConflict),
   usecase (catalog-manager с `InvalidateAll`), `main.go` (`iamCatalogServer`).
5. gateway: структурные `/iam`-ручки под `authorizeResource(iam:global, manage)`,
   gRPC-клиент `IamCatalogServiceClient`, маппинг (reuse `httpFromGRPC`).
6. OpenAPI: новые `/iam/*` мутации (summary+description+operationId+ВСЕ коды
   200/201/400/403/404/409/422); `web npm run gen` (вкл. gen:spec + public-копия);
   `gen:check` + Spectral зелёные.
7. web: расширение раздела «Роли и доступы» (create/delete роли, attach/detach,
   create права; read-only системных); vitest.
8. README services/idm; `GOWORK=off go mod tidy` в затронутых модулях при новых
   общих зависимостях; `git checkout -- services/gateway/gateway` после сборки.
9. Опубликовать ADR-0015 (`docs/adr/0015-*.md`, вне openspec/).
10. PR с зелёным CI (go test всех модулей, golangci-lint [errname/paralleltest],
    govulncheck, gen:check, openapi-lint [Spectral], web-test [tsc+vitest],
    integration, conformance); merge → отдельный PR sync+archive (образец #39/#37).

Откат: миграции обратимы (`goose down` — снять seed `manage`, затем `DROP COLUMN
system`); контрактные изменения аддитивны (старые клиенты не ломаются); UI-формы
изолированы в существующем разделе.

## Open Questions

Ключевые вопросы закрыты в решениях 1–10 и фиксируются ADR-0015 (действие `manage`
на `iam:global` без неявного наследования; защита системных ролей/прав колонкой
`system` + backfill, код 422; широкая инвалидация поколением после структурных
мутаций; каскадное удаление роли «в использовании»; форма мутирующего контракта,
идемпотентность attach/detach и коды create-дубль→409/delete-нет→404; границы
scope). Открытых вопросов на момент дизайна нет.
