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

## Конфигурация (env)

| Переменная | Назначение | Дефолт |
|---|---|---|
| `PG_DSN` | DSN Postgres | `postgres://idm:idm@postgres-idm:5432/idm?sslmode=disable` |
| `REDIS_ADDR` | адрес DragonflyDB | `dragonfly:6379` |
| `IDM_CACHE_TTL` | TTL allow-решений | `30s` |
| `IDM_CACHE_TTL_DENY` | TTL deny-решений | `10s` |
| `GRPC_ADDR` / `HTTP_ADDR` | адреса gRPC / HTTP (`/healthz`,`/readyz`) | `:9090` / `:8081` |
| `AUTH_DISABLED` | локальный bypass JWT (только локалка) | `false` |
