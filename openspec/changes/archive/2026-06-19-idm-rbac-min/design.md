## Context

IDM — целевой сервис авторизации прав (ADR-0003): gRPC `AccessService.CheckAccess(subject, resource, action) → {allowed, reason}`. Сейчас это скелет: метод возвращает `codes.Unimplemented`, модели ролей, репозитория и кэша нет, миграций IDM нет. На периметре и в сервисе проектов оставлены заглушки авторизации, которые всегда разрешают:

- `services/gateway/handlers.go:82` — TODO перед `CreateService` (и нет проверки на `list`/`get`); `services/gateway/main.go:56` — клиент `idmv1` создан, но `_ =`.
- `services/projects/internal/grpcapi/server.go:104` — `authorize()` всегда возвращает `nil`.

Инфраструктура локалки готова: в `deploy/compose/docker-compose.yml` подняты `postgres-idm` (PG_DSN) и `dragonfly` (REDIS_ADDR), сервис `idm` получает оба адреса, `AUTH_DISABLED=true` локально. Есть рабочий образец одноразового мигратора (`migrate-projects` + `services/projects/migrate.Dockerfile`, goose из `./tools`, `service_completed_successfully`).

Ограничения (обязательны): комментарии только на русском; fail-closed (пустой/недоступный IDM → отказ, не раскрывать внутренние ошибки); многошаговые записи в транзакции; кэш-инвалидация при изменении ролей; singleflight на чтениях кэша; миграции обратимы и запускаются `GOWORK=off`.

## Goals / Non-Goals

**Goals:**
- Реальная минимальная модель RBAC в Postgres: субъект → роль → право (action) на ресурсе.
- `CheckAccess` принимает решение по модели; deny по умолчанию (нет совпадающей привязки → запрет).
- Кэш решений в DragonflyDB: ключ от `(subject, resource, action)`, TTL, singleflight против stampede, инвалидация при изменении ролей.
- Content-aware `/readyz` IDM: пинг Postgres + DragonflyDB.
- Gateway и projects вызывают `CheckAccess` перед доменными операциями; deny/недоступность → 403 / `PermissionDenied`, fail-closed.
- Сидинг демо-роли (`create`@`project:demo`) и инструкция, чтобы сквозной сценарий портала проходил при включённом RBAC.

**Non-Goals:**
- Богатая модель политик (ABAC, иерархии проектов, делегирование, наследование ролей) — позже.
- UI/эндпоинты управления ролями (выдача через SQL/seed в MVP).
- OIDC-флоу портала, тонкая настройка Keycloak realm — полагаемся на oauth2-proxy/`AUTH_DISABLED`.
- Изменение `.proto`-контракта IDM, доменной логики workflow, реальные GitLab/Vault/Harbor.

## Decisions

### 1. Модель данных (Postgres)

Минимальный нормализованный RBAC. Таблицы (миграция `0001_create_rbac.sql`, обратимая):

- `roles(id uuid pk, name text unique not null, created_at timestamptz default now())` — каталог ролей.
- `permissions(id uuid pk, action text not null, resource text not null, unique(action, resource))` — атомарное право «action над resource». `resource` хранится как строка-паттерн целевого ресурса (например, `project:demo`). В MVP — точное совпадение, без wildcard (заложить колонку, но матчинг строгий).
- `role_permissions(role_id uuid fk roles, permission_id uuid fk permissions, pk(role_id, permission_id))` — связь many-to-many.
- `subject_roles(subject text not null, role_id uuid fk roles, pk(subject, role_id))` — привязка субъекта (sub из JWT) к роли.

Решение разрешено, если существует цепочка `subject_roles → role_permissions → permissions`, где `permissions.action = action AND permissions.resource = resource`. Иначе deny. Одним SQL-запросом с `EXISTS`/`JOIN`.

**Альтернатива (отклонена):** хранить право прямо в `subject_roles` как denormalized строку — проще читать, но дублирование и сложная инвалидация при смене состава роли. Нормализация дешева для MVP-объёма и честно отражает RBAC.

### 2. Слои сервиса IDM

`repository` (pgx, запрос решения + операции управления ролями в транзакции) → `usecase` (`CheckAccess`: сначала кэш, при промахе — БД через singleflight, запись в кэш; deny по умолчанию) → `grpcapi`/`accessServer` (валидация запроса, маппинг в `{allowed, reason}`, никаких внутренних ошибок наружу). Зависимости внедряются через интерфейсы (как `Catalog` в projects) для table-driven тестов с in-memory/стаб-репозиторием.

### 3. Стратегия кэша (DragonflyDB, БЛОК 5)

- Клиент — `github.com/redis/go-redis/v9` (DragonflyDB совместим по протоколу).
- Ключ: `idm:decision:v1:{subject}:{resource}:{action}` (компоненты экранируются/хэшируются для безопасной длины). Значение: `1`/`0` (allow/deny). TTL положительных и отрицательных решений конфигурируем (`IDM_CACHE_TTL`, дефолт ~30s); кэшируем и deny (negative caching) с тем же/меньшим TTL — иначе отказ под нагрузкой бьёт в БД.
- **Singleflight**: чтение БД при промахе кэша оборачивается `singleflight.Group` по ключу решения — одновременные одинаковые промахи дают один запрос в Postgres (anti-stampede).
- **Инвалидация**: при изменении ролей/прав/привязок (seed/админ-операции) удаляются затронутые ключи. В MVP, поскольку набор операций записи — это seed/миграции, применяем грубую, но корректную инвалидацию: версионный префикс ключа (`v1`) бампается через служебный ключ `idm:cache:gen`, делая старые решения недостижимыми; плюс точечное удаление по субъекту при адресном изменении. Это удовлетворяет «инвалидация при изменении ролей» без сканов.
- **Вытеснение протухших**: TTL отвечает за устаревание; Dragonfly сам вытесняет по истечении.

**Альтернатива (отклонена):** кэшировать роли субъекта, а решение считать в памяти. Сложнее инвалидация и больше логики; кэш готового решения проще и точнее ложится на `CheckAccess`.

### 4. Fail-closed

- Пустое/невалидное решение, ошибка БД, ошибка кэша на пути, где нельзя установить allow — трактуются как **deny**. Кэш-промах при недоступной БД → deny (не «разрешить на всякий случай»).
- Недоступность кэша НЕ должна разрешать молча: при ошибке кэша читаем БД напрямую (деградация в корректность, а не в обход); при ошибке БД — deny.
- `AUTH_DISABLED=true` (локалка) — единственный bypass, по образцу существующих сервисов; в проде запрещён.
- Наружу — только `allowed=false` + машинная `reason` (без `err.Error()`), детали в лог по ключу slog `err`.

### 5. Точки вызова и маппинг deny

- **Gateway**: в `create`/`list`/`get` перед вызовом `projects` извлекается `auth.ClaimsFromContext` (subject), формируется `resource = "project:" + project`, `action ∈ {create, read, list}`. Вызов `idm.CheckAccess`. `allowed=false` ИЛИ ошибка вызова IDM → HTTP **403** (стабильное тело, без деталей). Добавляется кейс `codes.PermissionDenied → http.StatusForbidden` в `httpFromGRPC`; ошибка соединения с IDM маппится в 403 явно (fail-closed), а не в 500.
- **Projects**: `authorize(ctx, project)` вызывает `idm.CheckAccess(subject, "project:"+project, "create")` перед `CreateService`. deny/ошибка → `status.Error(codes.PermissionDenied, ...)`. Клиент IDM внедряется в `Server` (новая зависимость).
- Defense-in-depth: проверка и на периметре, и в projects (даже если gateway обойдён).

### 6. Поток вызова (последовательность)

```
Портал → Gateway.create
  Gateway: subject ← ClaimsFromContext
  Gateway → IDM.CheckAccess(subject, project:<p>, create)
    IDM: cache GET decision-key
      hit  → return allowed
      miss → singleflight(key):
               Postgres: EXISTS(subject_roles⋈role_permissions⋈permissions)
               cache SET decision-key (TTL)
             return allowed
    allowed=false / IDM unreachable → Gateway → 403 (fail-closed)
    allowed=true → Gateway → Projects.CreateService
                     Projects.authorize → IDM.CheckAccess(...) (defense-in-depth)
                       deny → PermissionDenied
                       allow → catalog.CreateService
```

### 7. Локалка и сидинг

- `services/idm/migrate.Dockerfile` — копия образца projects (goose из `./tools`, контекст — корень репо, `IDM_DSN`/`PG_DSN`).
- Сервис `migrate-idm` в compose; `idm` зависит от него через `service_completed_successfully` (как projects от `migrate-projects`).
- Сидинг демо-данных — отдельная idempotent миграция (`0002_seed_demo.sql`) с `ON CONFLICT DO NOTHING`: роль `project-creator`, право `(create, project:demo)`, привязка субъекта демо-пользователя (sub из `AUTH_DISABLED`-claims / демо-токена) к роли. Seed помечен как демо-данные; для прод-профиля исключается (отдельный каталог миграций или goose `-no-versioning` seed — в MVP допускаем единый каталог, seed-миграция явно прокомментирована как локальная).

### 8. Тестирование

- IDM: table-driven + `t.Parallel()` — логика `CheckAccess` (allow/deny/deny-by-default) на стаб/in-memory репозитории; кэш (hit/miss/инвалидация, singleflight — один вызов БД при N конкурентных промахах через счётчик в стабе); репозиторий с pgx — в integration с тест-БД.
- Gateway/projects: стаб IDM-клиента (без сети) — deny маппится в 403 / `PermissionDenied`, недоступность IDM → тоже отказ (fail-closed), тело ответа не содержит внутренних деталей.
- `goleak` в пакетах с горутинами (singleflight).

## Risks / Trade-offs

- **Включение RBAC ломает сквозной сценарий портала** → Mitigation: idempotent seed демо-роли входит в scope; e2e/ручная проверка «Создание сервиса» под `AUTH_DISABLED` с засиженным субъектом.
- **Грубая инвалидация версионным префиксом инвалидирует весь кэш при любом изменении ролей** → Mitigation: для MVP-объёма (редкие seed/админ-правки) приемлемо; точечное удаление по субъекту добавлено для адресных случаев; задокументировано как осознанный trade-off.
- **Negative caching может «залипить» отказ после выдачи права** → Mitigation: короткий TTL для deny + инвалидация при изменении ролей делают окно ограниченным и предсказуемым.
- **Двойной вызов CheckAccess (gateway + projects) — лишняя латентность** → Mitigation: кэш решений делает второй вызов дешёвым; defense-in-depth важнее микро-латентности в MVP.
- **Ошибка кэша трактуется как деградация к БД** → при недоступном Dragonfly нагрузка падает на Postgres; Mitigation: singleflight ограничивает дублирование, `/readyz` сигнализирует деградацию.

## Migration Plan

1. Реализация в ветке `change/idm-rbac-min` от master (прямые коммиты в master запрещены).
2. Применение миграций IDM локально через `migrate-idm` (goose up); `down` обратим.
3. Деплой порядок: миграции → IDM → projects/gateway (потребители). IDM поднимается до потребителей; gateway/projects fail-closed при недоступном IDM, поэтому деградация безопасна.
4. **Rollback**: `goose down` откатывает схему; `AUTH_DISABLED=true` отключает проверку локально; откат кода — revert ветки. Провизия ресурсов не затрагивается, Saga не нужна.

## Open Questions

- ~~Источник `subject` для демо при `AUTH_DISABLED`~~ **РЕШЕНО**: `pkg/auth.Verify`
  в disabled-режиме сейчас возвращает `&Claims{}` (пустой `Subject`). Вводим
  конфиг `AUTH_DISABLED_SUBJECT` (дефолт `""` — поведение и тесты не меняются);
  при `Disabled` возвращаем `&Claims{Subject: cfg.DisabledSubject}`. В локальном
  compose задаём `AUTH_DISABLED_SUBJECT=demo-user` для gateway/projects, seed
  привязывает `demo-user` к демо-роли. Prod остаётся fail-closed (AUTH_DISABLED
  запрещён, токены несут реальный `sub`; пустой subject → deny). JWT-валидация
  не ослабляется — меняется только уже существующая disabled-ветка.
- Нужен ли отдельный прод-профиль миграций без seed-данных, или достаточно явного комментария и условного применения seed только в compose-профиле локалки. Предлагается второе для MVP.
