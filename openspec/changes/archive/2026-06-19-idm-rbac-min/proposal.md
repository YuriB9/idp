## Why

Сервис IDM сейчас — скелет: `AccessService.CheckAccess` возвращает `codes.Unimplemented`, а точки авторизации на периметре (gateway) и в сервисе проектов — заглушки, которые всегда разрешают. Это нарушает требование fail-closed: доменные операции (создание сервиса) выполняются без проверки прав. Этот change наполняет IDM реальной моделью RBAC (Postgres + кэш DragonflyDB) и подключает её к оставленным границам, закрывая Этап 1 плана MVP («IDM минимум: gRPC CheckAccess, модель ролей, Postgres + DragonflyDB-кэш»).

Соответствие: ADR-0003 (RBAC — отдельный сервис IDM, gRPC `CheckAccess`, Postgres + DragonflyDB-кэш), docs/IDP_MVP_plan.md БЛОК 2 (безопасность fail-closed), БЛОК 5 (singleflight на кэше IDM), Этап 1. При необходимости вводится новый ADR-0010 (модель RBAC и стратегия кэширования IDM).

## What Changes

- **IDM-домен**: модель ролей/прав в Postgres (схема + обратимые миграции goose, пин в `./tools`, запуск `GOWORK=off`); repository-слой (pgx); usecase с логикой решения; реальная реализация gRPC `CheckAccess` поверх модели. Контракт `proto/idm/v1/idm.proto` **НЕ меняется** (полей `subject/resource/action` → `allowed/reason` достаточно).
- **Кэш решений в DragonflyDB**: кэширование результата `CheckAccess` с TTL, защита от stampede через `singleflight` (БЛОК 5), инвалидация ключей при изменении ролей.
- **Content-aware `/readyz` IDM**: реальный пинг Postgres + DragonflyDB.
- **Подключение gateway**: перед доменными REST-операциями (`POST/GET/LIST /projects/{project}/services`) вызывается IDM `CheckAccess` (subject из `auth.ClaimsFromContext`, resource `project:<project>`, action `create`/`read`/`list`). Deny → HTTP 403; добавляется маппинг `codes.PermissionDenied → 403` в `httpFromGRPC`.
- **Подключение projects**: `authorize()` в `grpcapi` перед `CreateService` вызывает IDM `CheckAccess`; deny → `codes.PermissionDenied`.
- **Fail-closed**: недоступность IDM или пустое/невалидное решение → ОТКАЗ. Внутренние ошибки наружу не раскрываются. `AUTH_DISABLED`/локальный bypass — только локалка, как в остальных сервисах.
- **Локалка**: `services/idm/migrate.Dockerfile` + сервис `migrate-idm` в compose (по образцу `migrate-projects`, `service_completed_successfully`); сидинг базовых ролей для демо (право `create` в проекте `demo`), чтобы сквозной сценарий портала «Создание сервиса» проходил при включённом RBAC.
- **README/инструкция**: устройство ролей, как выдать право, как проверить отказ/разрешение.

## Capabilities

### New Capabilities
- `access-control`: модель RBAC сервиса IDM (роли, права, привязки субъектов), логика принятия решения `CheckAccess`, стратегия кэширования в DragonflyDB (TTL, singleflight, инвалидация), поведение fail-closed и content-aware `/readyz`.

### Modified Capabilities
- `perimeter-rest`: gateway ОБЯЗАН вызывать IDM `CheckAccess` перед доменными REST-операциями и маппить deny/недоступность IDM в HTTP 403 без раскрытия деталей.
- `service-provisioning`: `CreateService` в сервисе проектов ОБЯЗАН авторизовать вызов через IDM `CheckAccess`; deny/недоступность IDM → `codes.PermissionDenied`.
- `local-environment`: добавляется одноразовый мигратор `migrate-idm` и сидинг базовых демо-ролей; `idm` зависит от мигратора через `service_completed_successfully`.

## Impact

- **Затронутые сервисы и границы**: `services/idm` (наполнение домена), `services/gateway` (вызов gRPC IDM перед доменными ручками), `services/projects` (вызов gRPC IDM в `authorize`). Граница контракта `proto/idm/v1` не меняется — кодоген не требуется.
- **Impact-анализ по графу знаний** (`.understand-anything`), слои/узлы и транзитивные зависимости:
  - Слой IDM: `services/idm/main.go` (узел `accessServer.CheckAccess`) → новые узлы `internal/repository`, `internal/usecase`, `internal/cache`; новый каталог `services/idm/migrations` (ребро `defines_schema`).
  - Стык контракта: `proto/idm/v1/idm.proto` → `pkg/api/idm/v1/*.pb.go` → потребители `idmv1.NewAccessServiceClient` в `services/gateway/main.go:56` (сейчас `_ =`) и новый клиент в `services/projects`. Контракт стабилен → согласованных правок proto/TS-клиента не требуется.
  - Периметр: `services/gateway/handlers.go` (`create`/`list`/`get`, `httpFromGRPC`) — ребро `calls` к IDM; добавление кейса `PermissionDenied → 403`. TS-клиент портала не меняется (тело 403 — стандартный `errorBody`).
  - Проекты: `services/projects/internal/grpcapi/server.go` (`authorize`, `CreateService`) — ребро `calls` к IDM; новая зависимость `idmv1` в go.mod модуля projects.
  - Локальное окружение: `deploy/compose/docker-compose.yml` (узлы `idm`, `migrate-idm`, `postgres-idm`, `dragonfly`) — рёбра `depends_on`/`deploys`.
- **Зависимости**: новый клиент DragonflyDB (Redis-протокол) в модуле `services/idm`; `golang.org/x/sync/singleflight`; `idmv1` в `services/projects/go.mod`. `goose` уже пин в `./tools`.
- **План отката/компенсаций**: change не затрагивает провизию ресурсов (нет GitLab/Vault/Harbor), Saga не задействована. Обратимость на уровне БД — `down`-миграции goose; на уровне поведения — `AUTH_DISABLED` снимает проверку только локально.
- **Риск-радиус**: включение RBAC может заблокировать сквозной сценарий портала, если демо-роль не засидена — поэтому сидинг роли `create@demo` входит в scope и проверяется e2e.
