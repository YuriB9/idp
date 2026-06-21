# IDM — сервис прав/ролей (RBAC)

Минимальный RBAC: gRPC `AccessService.CheckAccess(subject, resource, action) →
{allowed, reason}` поверх модели ролей в Postgres с кэшем решений в DragonflyDB.
Решения fail-closed (нет права / недоступна БД → отказ). См. ADR-0003, ADR-0010,
docs/IDP_MVP_plan.md (Этап 1, БЛОК 2, БЛОК 5).

## Модель ролей

Нормализованный RBAC (миграции `migrations/0001_create_rbac.sql`):

- `roles(id, name)` — каталог ролей.
- `permissions(id, action, resource)` — атомарное право «action над resource».
  `resource` — строка целевого ресурса (например, `project:demo`); сравнение
  **строгое** (без wildcard в MVP).
- `role_permissions(role_id, permission_id)` — какие права у роли (M:N).
- `subject_roles(subject, role_id)` — какие роли у субъекта (`subject` = `sub`
  из JWT).

`CheckAccess` возвращает `allowed=true` ⇔ существует цепочка
`subject_roles → role_permissions → permissions` с совпадением `(action,
resource)`. Иначе — `allowed=false` (**deny-by-default**).

### Кэш решений (DragonflyDB)

Решение кэшируется по `(subject, resource, action)` с TTL (`IDM_CACHE_TTL` для
allow, `IDM_CACHE_TTL_DENY` для deny — negative caching). Промахи кэша
схлопываются `singleflight` (один запрос в БД на N конкурентных одинаковых
промахов). Инвалидация — инкремент поколения (`InvalidateAll`) либо точечное
удаление по субъекту (`InvalidateSubject`). При недоступном кэше сервис читает
БД напрямую (деградация в корректность, не в обход); при недоступной БД —
отказывает.

## Как выдать право

В MVP роли выдаются через SQL/миграции (UI управления — отдельный change).
Пример: дать субъекту `alice` право создавать сервисы в проекте `acme`.

```sql
-- роль и право (идемпотентно)
INSERT INTO roles (name) VALUES ('project-creator')
  ON CONFLICT (name) DO NOTHING;
INSERT INTO permissions (action, resource) VALUES ('create', 'project:acme')
  ON CONFLICT (action, resource) DO NOTHING;

-- связать право с ролью
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
WHERE r.name = 'project-creator'
  AND p.action = 'create' AND p.resource = 'project:acme'
ON CONFLICT DO NOTHING;

-- назначить роль субъекту
INSERT INTO subject_roles (subject, role_id)
SELECT 'alice', r.id FROM roles r WHERE r.name = 'project-creator'
ON CONFLICT DO NOTHING;
```

После изменения ролей кэш решений следует инвалидировать (в локалке проще
перезапустить `idm` или дождаться истечения TTL).

Локальный стенд автоматически засевает демо-роль (`migrations/0002_seed_demo.sql`):
`demo-user` получает `(create, project:demo)`. Субъект `demo-user` совпадает с
`AUTH_DISABLED_SUBJECT` в docker-compose, поэтому сквозной сценарий портала
«Создание сервиса» в проекте `demo` проходит при включённом RBAC.

## Управляющие RPC ролей (RoleAdminService)

Помимо `CheckAccess`, IDM предоставляет управляющий контракт ролей
`RoleAdminService` для **программной** синхронизации привязок субъект↔роль из
доменных потоков (например, workflow «Изменение владельцев»):

- `AssignRole(subject, role)` — выдать субъекту роль. Идемпотентно (повторная
  выдача — успех). Несуществующая роль → `NotFound`; пустые поля →
  `InvalidArgument`.
- `RevokeRole(subject, role)` — отозвать роль. Идемпотентно (отзыв отсутствующей
  привязки/роли — успех).

После любого изменения привязок IDM **инвалидирует кэш решений по затронутому
субъекту** (`InvalidateSubject`), поэтому устаревшие allow/deny не «залипают».
Путь не публичный (не периметр) — вызывается доменными сервисами/worker'ом.

### Роль владельца (`owner:project:<project>`)

Сценарий «Изменение владельцев» выдаёт/отзывает per-project роль
`owner:project:<project>` (права `read`/`list`/`change_owners` над
`project:<project>`). Локальный стенд засевает её для `project:demo`
(`migrations/0003_seed_owners_demo.sql`) и право `(change_owners, project:demo)`
субъекту `demo-user`, чтобы сквозной сценарий смены владельцев проходил при
включённом RBAC. Роль должна существовать до выдачи — иначе `AssignRole` вернёт
`NotFound`.

## Как проверить отказ/разрешение

Через портал/периметр (локалка, `AUTH_DISABLED=true`, `AUTH_DISABLED_SUBJECT=demo-user`):

- `POST /api/projects/demo/services {"name":"svc"}` → **201** (demo-user имеет
  право `create` в `project:demo`).
- Тот же запрос для проекта без выданного права (например, `project/other`) →
  **403** (нет совпадающего права; gateway маппит deny/недоступность IDM в 403,
  projects — в `PermissionDenied`).

Проверить решение напрямую (gRPC `CheckAccess`) можно интеграционным тестом
IDM-уровня:

```bash
# поднять postgres-idm + применить миграции (goose), затем:
IDM_TEST_DSN="postgres://idm:idm@localhost:5433/idm?sslmode=disable" \
  go test -tags=integration ./services/idm/...
```

## Читающий каталог IAM-админки (IamAdminService) и раздел портала

Поверх `CheckAccess`/`RoleAdminService` IDM предоставляет **читающий** контракт
`IamAdminService` для раздела портала «Роли и доступы» (ADR-0014):

- `ListRoles` — все роли каталога (по `name`).
- `ListPermissions` — все права (`action`, `resource`).
- `GetRolePermissions(role)` — права роли (несуществующая роль → `NotFound`).
- `ListSubjectsWithRoles(page_size, page_token)` — субъекты с их ролями,
  keyset-пагинация по `subject` (DISTINCT из `subject_roles`).
- `GetSubjectRoles(subject)` — роли субъекта (пусто, если ролей нет).

Чтение каталога **не имеет побочных эффектов на кэш решений** (только SELECT).
Назначение/снятие ролей из портала переиспользует идемпотентные
`RoleAdminService.AssignRole`/`RevokeRole` (после мутации — `InvalidateSubject`
по затронутому субъекту). Перечисление субъектов основано на `DISTINCT subject`:
**субъекты без ролей системе неизвестны и в списке не видны** (реестра
пользователей нет; назначить роль можно любому subject по строке).

### Авторизация админки (fail-closed)

IAM-админка — привилегированный периметр. Полномочия — отдельные горизонтальные
действия на ресурсе `iam:global`:

- `(read, iam:global)` — все читающие ручки каталога;
- `(write, iam:global)` — назначение/снятие ролей субъектам.

gateway вызывает `CheckAccess` **перед каждой** IAM-ручкой (GET → `read`,
POST/DELETE → `write`); отказ ИЛИ недоступность IDM → **403** (fail-closed),
запрос в IDM не проксируется, внутренние детали наружу не уходят (деталь — в лог
по ключу slog `err`). Ни `read`, ни `write` не наследуются неявно из
project-действий и наоборот.

### REST-ручки периметра

| Метод/путь | Действие RBAC | Семантика |
| --- | --- | --- |
| `GET /api/iam/roles` | `read@iam:global` | список ролей |
| `GET /api/iam/permissions` | `read@iam:global` | все права |
| `GET /api/iam/roles/{role}/permissions` | `read@iam:global` | права роли (нет роли → 404) |
| `GET /api/iam/subjects` | `read@iam:global` | субъекты с ролями (keyset) |
| `GET /api/iam/subjects/{subject}/roles` | `read@iam:global` | роли субъекта |
| `POST /api/iam/subjects/{subject}/roles/{role}` | `write@iam:global` | назначить (идемпотентно → 200) |
| `DELETE /api/iam/subjects/{subject}/roles/{role}` | `write@iam:global` | снять (идемпотентно → 200) |

Assign/revoke **идемпотентны**: повторное назначение и снятие отсутствующей связки
возвращают `200` с актуальным набором ролей субъекта. Несуществующая роль при
назначении → `404`; пустые `subject`/`role` → `400`.

### Локальный стенд

Миграция `migrations/0006_seed_iam_admin_demo.sql` засевает роль `iam-admin` с
правами `(read, iam:global)` и `(write, iam:global)` и выдаёт её `demo-user`,
поэтому раздел «Роли и доступы» работает при включённом RBAC. Применить миграции
IDM: `make migrate-idm` (откат: `make migrate-idm GOOSE_CMD=down`).

### Как проверить отказ/успех

- В портале раздел «Роли и доступы» (`/iam`): таблица ролей/прав, список субъектов
  и форма назначения. У `demo-user` есть права → чтение и assign/revoke проходят.
- Снять у `demo-user` роль `iam-admin` (или убрать grant `read@iam:global`) →
  раздел отвечает **403** и скрывает содержимое (fail-closed на UI).
- После назначения/снятия роли кэш решений по субъекту инвалидируется
  (`InvalidateSubject`), поэтому новый `CheckAccess` сразу отражает изменение.

## Динамическое управление каталогом (IamCatalogService, ADR-0015)

Поверх читающего `IamAdminService` IDM предоставляет **мутирующий** контракт
`IamCatalogService` для структурных изменений самого каталога ролей/прав из
портала «Роли и доступы»:

- `CreateRole(name)` / `DeleteRole(name)` — создать/удалить роль.
- `CreatePermission(action,resource)` / `DeletePermission(action,resource)` —
  создать/удалить право (произвольная пара; матчинг строгий).
- `AttachPermission(role,action,resource)` / `DetachPermission(...)` — править
  набор прав роли (идемпотентно).

Многошаговые записи идут в транзакции. Создаваемые роли/права —
**пользовательские** (`system=false`).

### Привилегированное действие `manage`

Структурная правка каталога опаснее назначения существующей роли, поэтому для неё
введено **отдельное** действие `(manage, iam:global)` — сверх `read` (чтение) и
`write` (assign/revoke). Три уровня доверия: аудитор `read`; оператор назначений
`write`; администратор каталога `manage`. Наследования в модели нет (`manage` не
подразумевает `write`/`read`); роли админки гранты выдаются явно сидированием.
gateway вызывает `CheckAccess(manage, iam:global)` **перед каждой** структурной
ручкой; отказ/недоступность IDM → **403** (fail-closed).

### Защита системных ролей/прав

Колонки `roles.system` и `permissions.system` (`boolean`, миграция
`0007_roles_permissions_system_flag.sql` помечает все сидированные `system=true`).
Системные роли/права **нельзя** удалить, а набор прав системной роли —
менять (attach/detach) → **422** (`FailedPrecondition`). Прикрепить системное
право к пользовательской роли можно (защищается роль, не право). Признак `system`
отдаётся в контракте, и портал показывает такие роли/права read-only (бейдж
«системная», кнопки удаления/правки скрыты).

### Широкая инвалидация кэша

Правка `role_permissions`/удаление роли/права затрагивает решения **всех**
носителей роли, поэтому точечной инвалидации по субъекту НЕДОСТАТОЧНО. Каждая
структурная мутация после успешного commit вызывает `InvalidateAll` (инкремент
поколения `idm:cache:gen`) — все ранее закэшированные решения становятся
устаревшими разом. `assign`/`revoke` остаются точечными (`InvalidateSubject`),
чтение — без эффектов на кэш. Удаление роли «в использовании» каскадно (FK
`ON DELETE CASCADE`) снимает её у всех носителей + широкая инвалидация.

### REST-ручки периметра (под `manage@iam:global`)

| Метод/путь | Семантика |
| --- | --- |
| `POST /api/iam/roles` | создать роль (дубль → 409); `201` |
| `DELETE /api/iam/roles/{role}` | удалить роль (системная → 422, нет → 404) |
| `POST /api/iam/roles/{role}/permissions` | attach право (тело `{action,resource}`; идемпотентно → 200; роль/право нет → 404; системная роль → 422) |
| `DELETE /api/iam/roles/{role}/permissions?action=&resource=` | detach право (идемпотентно → 200; системная роль → 422) |
| `POST /api/iam/permissions` | создать право (дубль → 409); `201` |
| `DELETE /api/iam/permissions?action=&resource=` | удалить право (системное → 422, нет → 404) |

Коды (reuse ADR-0012/0013): `AlreadyExists→409`, `FailedPrecondition→422`,
`NotFound→404`, `InvalidArgument→400`, `PermissionDenied→403`. Внутренние ошибки
наружу не раскрываются.

### Локальный стенд (manage)

Миграция `0008_seed_iam_manage_demo.sql` засевает право `(manage, iam:global)` и
привязывает его к роли `iam-admin` (её уже имеет `demo-user`), поэтому структурные
мутации каталога работают при включённом RBAC. Применить:
`make migrate-idm` (откат: `make migrate-idm GOOSE_CMD=down`).

### Как проверить отказ/успех

- С правом `manage` портал позволяет создать роль, прикрепить к ней права из
  каталога, создать право и удалить пользовательские роли/права.
- Удаление системной роли/права или правка набора прав системной роли → **422**;
  попытка без `manage` → **403** (структурные формы в портале скрыты).
- После любой структурной правки поколение `idm:cache:gen` инкрементируется —
  новый `CheckAccess` всех носителей отражает изменение.

## Справочник субъектов из каталога Keycloak (IdentityService, ADR-0016)

Субъект RBAC — это канонический ключ `sub` из JWT (UUID пользователя Keycloak),
он же `subject_roles.subject` и `auth.Claims.Subject`. `preferred_username` ключом
авторизации **не** является (изменяем). `IdentityService` даёт IAM-админке реальный
справочник пользователей realm `idp`:

- `SearchSubjects(query, cursor, page_size)` — поиск по username/email/имени
  (постраничный, непрозрачный курсор поверх offset Keycloak);
- `ResolveSubjects(subjects[])` — батч-резолв `sub` → идентичность
  (`{subject, username, email, display_name, enabled, found}`); отсутствующие в
  каталоге → `found=false` («осиротевшие»: роль есть, в каталоге нет).

**Источник правды** — живой запрос в Keycloak Admin REST + **отдельный** кэш
идентичностей в DragonflyDB (namespace `idm:identity:*`, TTL). Этот кэш **не**
трогает decision-cache RBAC (`idm:cache:gen`/`InvalidateSubject`): у решений и
идентичностей разные жизненные циклы (решения инвалидируются мутациями RBAC,
идентичности — по TTL из внешнего источника).

### Сервис-аккаунт Keycloak

Чтение каталога идёт от confidential-клиента `idm-service-account` (realm-management
роли `view-users`/`query-users`), токен — по `client_credentials`. Секрет берётся из
env (локалка) / Vault (прод) и **не логируется и наружу не отдаётся**. Все исходящие
вызовы (токен + Admin REST) проходят через SSRF-guard (`pkg/ssrf`: ValidateURL +
GuardedDialContext) и `pkg/httpclient`; `SSRF_DISABLED=true` — **только локалка**
(адрес keycloak приватный, http). Реализация — слой `internal/identity` (образец
outbound — devinfra-worker).

### Авторизация просмотра PII

Листинг/резолв реальных идентичностей (PII) авторизуется **отдельным** правом
`(read, iam:directory)` — наименьшие привилегии, отдельно от `read`/`write`/`manage`
на `iam:global`. Обогащение списка субъектов (`GET /iam/subjects`) идентичностями
выполняется **только** при наличии этого права; иначе ответ «сырой» (PII не
раскрывается). Роли `iam-admin` право засеяно миграцией `0010`.

### Поведение при недоступном Keycloak (деградация)

Справочник **не критичен** для `CheckAccess` (решение по `sub` не зависит от имён):

- ручки `/iam/directory/*` → **503** (retryable), UI показывает «каталог недоступен»;
- обогащение `GET /iam/subjects` опускает идентичности (роли отдаются `200`);
- управление ролями по сырому `sub` **не ломается**.

Листинг всё равно под `CheckAccess` (deny-by-default) **до** запроса в Keycloak.

### Сведение локалки (канонический ключ)

Реальному `dev` в `deploy/keycloak/idp-realm.json` задан детерминированный UUID
`11111111-1111-1111-1111-111111111111`; `AUTH_DISABLED_SUBJECT` в compose равен ему;
миграция `0009` перенесла сиды `subject_roles` с `demo-user` на этот UUID. Поэтому и
реальный вход через Keycloak, и локальный disabled-режим дают один и тот же `sub`.

### Как проверить поиск/назначение

- В разделе портала «Роли и доступы» начните вводить имя/логин/email в «Поиск
  пользователя» — пикер (с debounce) покажет совпадения из каталога; выбор
  подставляет канонический `sub`.
- Рядом с субъектами в списке видны `username`/`email`; «осиротевшие» помечены
  «нет в каталоге».
- Без `(read, iam:directory)` пикер скрыт, назначение по строке subject остаётся
  доступным; при недоступном Keycloak — индикация «каталог недоступен».

## Конфигурация (env)

| Переменная | Назначение | Дефолт |
|---|---|---|
| `PG_DSN` | DSN Postgres | `postgres://idm:idm@postgres-idm:5432/idm?sslmode=disable` |
| `REDIS_ADDR` | адрес DragonflyDB | `dragonfly:6379` |
| `IDM_CACHE_TTL` | TTL allow-решений | `30s` |
| `IDM_CACHE_TTL_DENY` | TTL deny-решений | `10s` |
| `IDM_IDENTITY_CACHE_TTL` | TTL кэша идентичностей (справочник, ADR-0016) | `5m` |
| `KEYCLOAK_BASE_URL` | базовый адрес Keycloak | `http://keycloak:8080` |
| `KEYCLOAK_REALM` | realm каталога пользователей | `idp` |
| `KEYCLOAK_SA_CLIENT_ID` | client-id сервис-аккаунта | `idm-service-account` |
| `KEYCLOAK_SA_CLIENT_SECRET` | секрет сервис-аккаунта (не логируется) | — |
| `SSRF_DISABLED` | отключить SSRF-guard (**только локалка**) | `false` |
| `GRPC_ADDR` / `HTTP_ADDR` | адреса gRPC / HTTP (`/healthz`,`/readyz`) | `:9090` / `:8081` |
| `AUTH_DISABLED` | локальный bypass JWT (только локалка) | `false` |
