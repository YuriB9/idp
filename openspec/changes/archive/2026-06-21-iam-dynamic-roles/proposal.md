## Why

IAM-админка (фаза 1, ADR-0014) дала портал, который ПОКАЗЫВАЕТ каталог ролей/прав
и НАЗНАЧАЕТ существующие роли субъектам (assign/revoke на `(write, iam:global)`).
Но сам каталог статичен: роли, права и связки `role_permissions` заводятся ТОЛЬКО
миграциями. Платформенному администратору негде создать новую роль, собрать её
набор прав или завести новое право без ручной миграции и редеплоя. См.
docs/IDP_MVP_plan.md (Этап 3, RBAC/IAM) и ADR-0003/0009/0010/0011/0014.

Структурная правка каталога опаснее назначения существующей роли: меняется САМ
набор прав, и правка `role_permissions` затрагивает ВСЕХ носителей роли разом,
поэтому точечной инвалидации кэша по субъекту НЕДОСТАТОЧНО — нужна широкая
(поколение `idm:cache:gen`). Кроме того, удаление сидированных ролей
(`iam-admin`, `owner:project:*`, `project-creator`) или их прав сломало бы
платформу. Поэтому change ОБЯЗАН закрыть и обосновать (новый ADR-0015) модель
более привилегированного действия `manage`, защиту системных ролей/прав, широкую
инвалидацию поколением и семантику удаления роли «в использовании».

## What Changes

- **Контракт `proto/idm/v1` (аддитивно):** новый сервис `IamCatalogService` со
  СТРУКТУРНЫМИ мутациями каталога: `CreateRole(name)`, `DeleteRole(name)`,
  `CreatePermission(action,resource)`, `DeletePermission(action,resource)`,
  `AttachPermission(role,action,resource)`, `DetachPermission(role,action,resource)`.
  Сообщения `Role`/`Permission` РАСШИРЯЮТСЯ полем `bool system` (аддитивно,
  wire-совместимо). Читающий `IamAdminService` и `RoleAdminService` (assign/revoke)
  ПЕРЕИСПОЛЬЗУЮТСЯ. `buf generate`; `gen:check` зелёный.
- **IDM repository/usecase (мутации каталога):** методы
  `CreateRole`/`DeleteRole`/`CreatePermission`/`DeletePermission`/
  `AttachPermission`/`DetachPermission` в транзакциях. UNIQUE-конфликты (имя роли,
  пара `action`/`resource`) → `ErrConflict`. Системная роль/право → `ErrPrecondition`.
  Несуществующая цель → `ErrNotFound`. ЛЮБАЯ структурная мутация → ШИРОКАЯ
  инвалидация кэша (`InvalidateAll`/поколение `idm:cache:gen`), т.к. затронуты все
  носители роли; assign/revoke остаются точечными (`InvalidateSubject`); чтение —
  без эффектов на кэш.
- **Защита системных ролей/прав:** колонки `roles.system` и `permissions.system`
  (`boolean NOT NULL DEFAULT false`, обратимая goose-миграция). Все существующие
  (сидированные) роли/права помечаются `system=true`; через API их НЕЛЬЗЯ удалить,
  нельзя менять набор прав системной роли (attach/detach) — попытка →
  `FailedPrecondition` → `422`. Создаваемые через API — пользовательские
  (`system=false`).
- **Авторизация (НОВОЕ привилегированное действие):** действие `manage` на
  горизонтальном ресурсе `iam:global` для ВСЕХ структурных мутаций каталога —
  отдельно от `write` (assign/revoke) и `read`. БЕЗ неявного наследования в модели
  (`manage` не подразумевает `write`/`read`); роли админки гранты выдаются явно.
  gateway вызывает `CheckAccess(manage, iam:global)` ПЕРЕД каждой структурной
  ручкой (fail-closed → 403).
- **Семантика удаления роли «в использовании»:** каскад (FK `subject_roles`/
  `role_permissions` уже `ON DELETE CASCADE`) — роль снимается у всех носителей,
  затем ШИРОКАЯ инвалидация. Системная роль не удаляется (422).
- **Периметр (REST, ADR-0009) — структурные ручки каталога:**
  `POST /iam/roles` (создать роль), `DELETE /iam/roles/{role}` (удалить),
  `POST /iam/roles/{role}/permissions` (attach, тело `{action,resource}`),
  `DELETE /iam/roles/{role}/permissions` (detach, query `action`/`resource`),
  `POST /iam/permissions` (создать право), `DELETE /iam/permissions` (удалить,
  query). Маппинг кодов как в ADR-0012/0013: deny→403, NotFound→404, дубль→409,
  системная/предусловие→422, валидация→400. OpenAPI + TS регенерируются; Spectral
  и Schemathesis-конформанс зелёные.
- **Портал — расширение раздела «Роли и доступы»:** создание/удаление роли,
  редактирование прав роли (attach/detach из списка прав), создание права
  (react-hook-form + zod, TanStack-мутации + invalidate). Системные роли/права —
  read-only (бейдж «системная», кнопки удаления скрыты). Обработка 403/404/409/422,
  рантайм-валидация ответов zod, без сырых внутренних ошибок.
- **Локалка:** обратимые goose-миграции IDM: backfill `system=true` существующим
  ролям/правам; seed права `(manage, iam:global)` роли `iam-admin` (у `demo-user`
  она уже есть). Через `migrate-idm`.
- **Документация:** README services/idm — что теперь можно создавать/удалять роли
  и права, как устроена защита системных ролей, какое право нужно
  (`read`/`write`/`manage`), как широко инвалидируется кэш после структурной
  правки, как проверить отказ/успех.
- **ADR-0015:** динамический каталог IAM — полномочие `manage`, защита системных
  ролей (модель + миграция), инвалидация поколением, каскадное удаление роли,
  форма мутирующего контракта и коды, границы scope.

## Capabilities

### New Capabilities
<!-- Новых capability-спеков нет: всё расширяет существующие. -->

### Modified Capabilities
- `service-contracts`: новый сервис `IamCatalogService` (структурные мутации) в
  `proto/idm/v1`; поле `system` в сообщениях `Role`/`Permission` (аддитивно,
  wire-совместимо).
- `iam-administration`: мутации каталога (create/delete роли, attach/detach,
  create/delete права) в транзакциях; признак `system` и защита системных
  ролей/прав; ШИРОКАЯ инвалидация кэша при структурной правке; семантика
  каскадного удаления роли «в использовании».
- `access-control`: новое привилегированное действие `manage` на `iam:global`
  (отдельно от `read`/`write`, без неявного наследования); структурные мутации →
  широкая инвалидация поколением (`InvalidateAll`); чтение — без эффектов на кэш.
- `perimeter-rest`: структурные `/iam`-ручки каталога (роли, права роли, права);
  маппинг deny→403, NotFound→404, дубль→409, системная/предусловие→422,
  валидация→400; идемпотентность attach/detach.
- `portal-ui`: расширение раздела «Роли и доступы» — создание/удаление роли,
  attach/detach прав, создание права; read-only для системных; обработка
  403/404/409/422.
- `local-environment`: backfill `system=true` сидированным ролям/правам; seed
  права `(manage, iam:global)` роли `iam-admin` (обратимые миграции goose).

## Impact

- **Контракты/кодоген:** `proto/idm/v1` (новый `IamCatalogService`, поле `system`),
  `pkg/api/idm/**` (`buf generate`), `openapi/openapi.yaml` (новые `/iam/*`
  мутации), TS-клиент `web/src/api` + `web/public/openapi.yaml` (`gen:check`).
- **services/idm:** `repository` (мутации каталога в транзакциях, guard системных,
  UNIQUE→ErrConflict), `usecase` (catalog-manager с широкой инвалидацией),
  `main.go` (новый `iamCatalogServer`).
- **services/gateway:** новый обработчик структурных `/iam`-ручек, gRPC-клиент
  `IamCatalogServiceClient`, `CheckAccess(manage, iam:global)` перед каждой
  мутацией (reuse `authorizeResource`), маппинг кодов (reuse `httpFromGRPC`).
- **web:** расширение страницы «Роли и доступы» — формы create/delete/attach/detach
  (zod + react-hook-form + TanStack), read-only системных, обработка
  403/404/409/422; vitest.
- **БД:** обратимые миграции `services/idm/migrations` — колонки `system` +
  backfill; seed `(manage, iam:global)` для `iam-admin`. FK уже каскадные (новых
  таблиц нет).
- **Откат/компенсации:** не затрагивает провизию ресурсов (нет Saga/workflow);
  миграции обратимы; контракт аддитивен; UI-раздел изолирован.
- **Зависимости:** при новых общих зависимостях — `GOWORK=off go mod tidy` во всех
  затронутых модулях. NB: `services/gateway/gateway` — закоммиченный бинарь, после
  сборки не коммитить (`git checkout -- services/gateway/gateway`).
