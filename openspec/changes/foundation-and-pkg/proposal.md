## Why

Перед любой доменной логикой нужен прочный, проверяемый фундамент: монорепо-раскладка `go.work`, блокирующий CI с первого коммита, общие `pkg/*` (чтобы безопасность и observability не копипастились по сервисам), контракты (gRPC/OpenAPI) как источник правды и воспроизводимая локалка. Это снимает целый класс ошибок (небезопасный auth-passthrough, гонки статусов, SSRF, дрейф контрактов) до того, как они расползутся по сервисам. Соответствует `docs/IDP_MVP_plan.md` — Этап 1, раздел «Инженерные стандарты» (БЛОК 0 и общие `pkg/*`), и реализует решения ADR-0002 (gRPC внутри), ADR-0003 (auth fail-closed), ADR-0004 (guarded-CAS статусов).

## What Changes

- Монорепо на `go.work`: `./pkg` + `services/{gateway,idm,projects,devinfra-worker}` + `./tests/e2e` + изолированный `./tools` (вне `go.work`, запуск с `GOWORK=off`).
- БЛОК 0 — CI с первого коммита (GitHub Actions, матрица по модулям `go.work`): `go test -race -shuffle=on`, `go mod tidy && git diff --exit-code`, `golangci-lint`, **блокирующий `govulncheck`**, отдельный integration-джоб. `.golangci.yml` с заданным набором линтеров (errcheck, govet, staticcheck, revive, gosec, bodyclose, sqlclosecheck, nilerr, errname, paralleltest, package-comments). Dependabot. Dockerfile на каждый сервис.
- Каркас общих `pkg/*`: `httpserver`, `httpclient`, `config`, `errs`, `db` (`NewPool` с конфигом пула), `auth` (JWT fail-closed + JWKS), `ssrf` (`ValidateURL` + `GuardedDialContext`), `logger` (slog, единый ключ `"err"`), gRPC interceptor-стек (recovery / request-id / otel / auth).
- Контракты: каркас `.proto` (gateway↔idm, gateway↔projects) + кодоген Go-стабов; каркас OpenAPI периметра + кодоген TS-клиента и zod-схем.
- `docker-compose` локалки: Keycloak, Oauth2-Proxy, Postgres ×2 (projects, idm), DragonflyDB, Temporal + UI, сервисы-скелеты, моки GitLab/Vault/Harbor.

Out of scope: любая доменная логика user stories (создание/владельцы/перенос/удаление) — отдельные последующие changes. Доменную логику начинаем только после зелёного CI этого изменения.

## Capabilities

### New Capabilities
- `monorepo-workspace`: раскладка `go.work` (модули `pkg`, сервисы, `tests/e2e`) и изолированный `tools` вне workspace с пином инструментов.
- `ci-pipeline`: блокирующий CI (матрица по модулям, race+shuffle, tidy-check, golangci-lint, govulncheck), `.golangci.yml`, Dependabot и Dockerfile на сервис.
- `platform-libraries`: общие `pkg/*` — httpserver, httpclient, config, errs, db, auth (fail-closed JWT/JWKS), ssrf (ValidateURL + GuardedDialContext), logger, gRPC interceptor-стек.
- `service-contracts`: каркас `.proto` для внутренних вызовов и OpenAPI периметра с кодогеном Go-стабов и TS-клиента.
- `local-environment`: `docker-compose` локалка с Keycloak, Oauth2-Proxy, Postgres×2, DragonflyDB, Temporal+UI, скелетами сервисов и моками GitLab/Vault/Harbor.

### Modified Capabilities
<!-- Существующих спеков нет (openspec/specs/ пуст); изменяемых требований нет. -->

## Impact

- **Сервисы и границы:** заводятся скелеты всех четырёх сервисов (`gateway`, `idm`, `projects`, `devinfra-worker`) и их границы — gRPC (gateway↔idm, gateway↔projects), периметр OpenAPI (портал↔gateway через Oauth2-Proxy), постановка задач в Temporal (projects→worker). Реализация границ — контрактная (скелеты), без доменной логики.
- **Код/зависимости:** новый `go.work` и `go.mod` на модуль; общие `pkg/*`; tooling-модуль `./tools`; кодоген (protoc/buf, openapi-typescript/orval).
- **Инфраструктура:** GitHub Actions, Dependabot, Dockerfile×N, `docker-compose` + конфиги Keycloak realm и Oauth2-Proxy, моки внешних систем.
- **План отката/компенсаций:** изменение не провизионит управляемые ресурсы (GitLab/Vault/Harbor реальны только в виде моков), Saga-компенсации не задействуются — соответствующий план не требуется. Saga-границы лишь закладываются контрактно для последующих changes.
