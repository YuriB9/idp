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

## Конфигурация (env)

| Переменная | Назначение | Дефолт |
|---|---|---|
| `PG_DSN` | DSN Postgres | `postgres://idm:idm@postgres-idm:5432/idm?sslmode=disable` |
| `REDIS_ADDR` | адрес DragonflyDB | `dragonfly:6379` |
| `IDM_CACHE_TTL` | TTL allow-решений | `30s` |
| `IDM_CACHE_TTL_DENY` | TTL deny-решений | `10s` |
| `GRPC_ADDR` / `HTTP_ADDR` | адреса gRPC / HTTP (`/healthz`,`/readyz`) | `:9090` / `:8081` |
| `AUTH_DISABLED` | локальный bypass JWT (только локалка) | `false` |
