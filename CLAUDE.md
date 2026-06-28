# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project Overview

**IDP — Internal Developer Platform (MVP).** Платформа самообслуживания для команды
DevInfra. MVP реализует сквозной сценарий **«Создание сервиса»** и его жизненный цикл
(передача владения, вывод из эксплуатации, RBAC-админка):

```
портал (SPA) → API-шлюз (REST/OpenAPI) → projects (gRPC) → Temporal workflow → devinfra-worker → GitLab/Vault/Harbor
```

Ключевой архитектурный принцип: **gRPC/protobuf внутри платформы, OpenAPI/JSON на
периметре** (портал ↔ шлюз). Аутентификация fail-closed: Oauth2-Proxy + Keycloak +
per-service JWT (ADR-0002, ADR-0003, ADR-0009).

Go module prefix: `github.com/YuriB9/idp/...`. Go **1.26.4**.

## Repository Layout

| Путь | Назначение |
|------|-----------|
| `services/gateway` | API-шлюз: REST-периметр поверх gRPC, проверка JWT + RBAC (`CheckAccess`) |
| `services/projects` | Сервис каталога сервисов (gRPC), запуск Temporal-workflow провизии |
| `services/idm` | RBAC/IAM: роли, права, субъекты, справочник из Keycloak |
| `services/devinfra-worker` | Temporal worker: провизия GitLab/Vault/Harbor (Saga) |
| `pkg/` | Общие библиотеки (httpserver, grpcx, db, auth, logger, ssrf, errs, config, …) |
| `web/` | Портал (React + Vite + TS), генерируемый TS-клиент из OpenAPI |
| `proto/` | `.proto` контракты внутренних вызовов (buf codegen) |
| `openapi/openapi.yaml` | Источник правды периметра (TS-клиент + zod) |
| `tests/e2e` | E2E-тесты (отдельный go.work-модуль) |
| `tools/` | Изолированный модуль кодогена/линтинга (НЕ в go.work, запускается с `GOWORK=off`) |
| `deploy/` | compose-стенд, Helm-чарты, Keycloak/oauth2-proxy/mocks/TLS |
| `docs/adr/` | Architecture Decision Records (MADR-lite) |
| `openspec/` | OpenSpec change-driven спеки (specs/ + changes/) |

Каждый сервис — **отдельный go-модуль** в `go.work` (ADR-0006). `pkg`, четыре
сервиса и `tests/e2e` входят в workspace; `tools` намеренно изолирован.

## Build / Test / Lint Commands

Запускаются из **корня репозитория** через Makefile (итерирует по модулям go.work):

```bash
make test          # go test -race -shuffle=on ./... по всем модулям
make lint          # golangci-lint run по всем модулям
make tidy-all      # go mod tidy (GOWORK=off, в правильном порядке)
make tidy-check    # tidy + проверка отсутствия diff (как в CI)
make gen           # proto + openapi codegen
make proto         # buf lint + buf generate (через tools, GOWORK=off)
make lint-openapi  # spectral lint спеки периметра
make conformance   # Schemathesis «доки vs реальность» против поднятого gateway
```

Для одного модуля: `cd services/<svc> && go test ./...` (go.work подхватит локальные `pkg`).

**Портал** (`cd web`):
```bash
npm run dev        # http://localhost:3000, прокси /api → :8081 (GATEWAY_URL)
npm test           # vitest
npm run gen        # перегенерация src/api из openapi.yaml
npm run gen:check  # проверка синхронности (как в CI)
```

## Local Stack

```bash
docker compose -f deploy/compose/docker-compose.yml up --build
```
Поднимает Keycloak, Oauth2-Proxy, Postgres ×2, DragonflyDB, Temporal + UI, все
сервисы, портал и моки GitLab/Vault/Harbor. Портал — http://localhost:3000,
шлюз — http://localhost:8081/api, Temporal UI — :8080, Keycloak — :8088.

Локально периметр работает с `AUTH_DISABLED=true` (единственный разрешённый способ
отключения JWT; в проде — реальный JWKS, fail-closed, ADR-0003).

**E2E / интеграции** (по отдельным таргетам, каждый поднимает свой compose):
`make e2e`, `make gitlab`, `make vault`, `make harbor` (up → test → down).

## Contracts & Codegen — критично

- **Периметр**: `openapi/openapi.yaml` — единственный источник правды. После правок
  обязательно `cd web && npm run gen`; CI ловит рассинхрон через `npm run gen:check`.
- **Внутренние вызовы**: `.proto` в `proto/`, кодоген через `make proto` (модуль
  `tools`, `GOWORK=off`). Не редактируй сгенерированные `*.pb.go`/`schema.ts`/`client.ts` вручную.

## Conventions

- **Статусы сервиса** меняются только через guarded-CAS (ADR-0004); создание —
  Saga с полным откатом при недоступности Vault (ADR-0005).
- **Auth fail-closed**: отказ или недоступность IDM → `403`. gateway вызывает
  `CheckAccess` перед каждой ручкой.
- **Миграции БД** — goose (ADR-0007), в `services/*/migrations`.
- **Линтер** — `.golangci.yml` в корне. Соблюдай существующий стиль модуля.
- **tools-модуль** изолирован: его зависимости не должны протекать в графы сервисов.
- Изменение общей зависимости в одном go.work-модуле может сломать `tidy`/`govulncheck`
  в зависимом — нужен ручной re-tidy в том же PR.

## Project Documentation Context

При работе над задачей сверяйся с этими источниками (ADR — обязательны для
архитектурных решений):

### Корневая документация
- [README.md](README.md) — обзор, локальный стенд, сценарии создания/вывода/RBAC, конформанс.
- [docs/README.md](docs/README.md) — индекс ADR.
- [docs/IDP_MVP_plan.md](docs/IDP_MVP_plan.md) — план MVP.
- [docs/audits/](docs/audits/) — аудиты (напр. `2026-06-golang-audit.md`).
- [services/idm/README.md](services/idm/README.md), [services/projects/README.md](services/projects/README.md) — детали сервисов и REST-ручек.

### Architecture Decision Records ([docs/adr/](docs/adr/))
| ADR | Решение |
|-----|---------|
| 0001 | Temporal как оркестратор провизии |
| 0002 | gRPC/protobuf для внутренних вызовов |
| 0003 | Модель аутентификации: Oauth2-Proxy + Keycloak + per-service JWT (fail-closed) |
| 0004 | Переходы статусов через guarded-CAS |
| 0005 | Полный Saga-откат при недоступности Vault в «Создании» |
| 0006 | Раскладка монорепо на go.work (модуль на сервис + изолированный tools) |
| 0007 | Инструмент миграций БД — goose |
| 0008 | Разделение определения и исполнения workflow |
| 0009 | Форма REST-ресурсов периметра (проектно-скоупленные пути) |
| 0010 | Модель RBAC и кэш IDM |
| 0011 | Контракт владельцев сервиса и синхронизация ролей |
| 0012 | Семантика decommission (soft delete) и K8s load-check |
| 0013 | Семантика transfer: point-of-no-return и двойная авторизация |
| 0014 | Авторизация IAM-админки и read-контракт |
| 0015 | Динамический каталог: manage и защита системных ролей/прав |
| 0016 | Справочник субъектов IAM из OIDC/Keycloak |
| 0017 | Дизайн-система портала и UI-архитектура |
| 0018 | E2E: путь аутентификации и детерминизм workflow |
| 0019 | GitLab: auth и маппинг namespace/owner |
| 0020 | Vault: auth и раскладка secret engine |
| 0021 | Harbor: auth и раскладка project/robot |
| 0022 | Единый источник прогресса workflow в портале |
| 0023 | Владельцы обязательны при создании сервиса |
| 0024 | Упаковка деплоя в Helm |
| 0025 | Istio service mesh и секреты |

### OpenSpec ([openspec/](openspec/))
Change-driven спецификации. `openspec/specs/` — текущие возможности
(`access-control`, `perimeter-rest`, `service-provisioning`, `service-decommissioning`,
`iam-administration`, `portal-ui`, `kubernetes-deployment` и др.);
`openspec/changes/` — активные предложения. Используй skills `opsx:*` / `openspec-*`
для работы с change-flow. Валидатор `--strict`: SHALL/MUST на первой строке
требования; сценарии ровно с `####`.
