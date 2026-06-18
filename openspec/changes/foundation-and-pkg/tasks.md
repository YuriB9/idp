## 1. Сверка и подготовка

- [x] 1.1 Сверить scope с `docs/IDP_MVP_plan.md` (Этап 1 + «Инженерные стандарты»: БЛОК 0, общие `pkg/*`) и затронутыми ADR-0002, ADR-0003, ADR-0004, ADR-0006; зафиксировать, что доменная логика — вне scope
- [x] 1.2 Инициализировать git-репозиторий и базовую структуру каталогов (`pkg`, `services`, `tests/e2e`, `tools`, `web`, `deploy`)

## 2. Монорепо go.work (capability: monorepo-workspace)

- [x] 2.1 Создать `go.mod` для `./pkg`, `services/{gateway,idm,projects,devinfra-worker}`, `./tests/e2e` (Go 1.26)
- [x] 2.2 Создать `go.work` с `use` для `pkg`, всех сервисов и `tests/e2e` (без `tools`)
- [x] 2.3 Создать `./tools` с собственным `go.mod` (вне workspace), пин инструментов кодогена/линтинга; зафиксировать запуск с `GOWORK=off`
- [x] 2.4 Проверить: `go build ./...` через workspace и запуск инструмента с `GOWORK=off` не меняют графы сервисов

## 3. CI и качество (capability: ci-pipeline)

- [x] 3.1 GitHub Actions: матрица по модулям `go.work` с `go test -race -shuffle=on`
- [x] 3.2 Шаг проверки `go mod tidy && git diff --exit-code` на каждом модуле
- [x] 3.3 `.golangci.yml` с набором: errcheck, govet, staticcheck, revive, gosec, bodyclose, sqlclosecheck, nilerr, errname, paralleltest, package-comments; шаг `golangci-lint` в CI
- [x] 3.4 Блокирующий шаг `govulncheck`
- [x] 3.5 Отдельный integration-джоб (build-тег `integration`), изолированный от дефолтного прогона
- [x] 3.6 `.github/dependabot.yml` (gomod по модулям + github-actions)
- [x] 3.7 `Dockerfile` на каждый сервис (`services/*`) для Trivy/SBOM

## 4. Общие пакеты pkg/* (capability: platform-libraries)

- [x] 4.1 `pkg/errs` — канонические sentinel'ы (`ErrNotFound`, `ErrConflict`)
- [x] 4.2 `pkg/logger` — slog-логгер с единым ключом `"err"`
- [x] 4.3 `pkg/config` — env-хелперы, корректная обработка легитимного `0`
- [x] 4.4 `pkg/db` — `NewPool` с обязательной конфигурацией пула `*pgxpool.Pool`
- [x] 4.5 `pkg/httpserver` — таймауты, graceful shutdown (drain `WithTimeout(WithoutCancel(ctx),30s)`), middleware-стек (Recoverer, RequestID, rate-limit, auth-toggle, content-aware `/readyz`)
- [x] 4.6 `pkg/httpclient` — тюнингованный Transport, маппинг `404→ErrNotFound`/`409→ErrConflict`
- [x] 4.7 `pkg/auth` — строгий JWT (audience/issuer/validMethods/expirationRequired) + JWKS (https), fail-closed (`JWKS_URL` пуст → `os.Exit(1)`, `AUTH_DISABLED=true`), admin-key через `subtle.ConstantTimeCompare`
- [x] 4.8 `pkg/ssrf` — `ValidateURL` (https + блок private/loopback/link-local/ULA) и `GuardedDialContext` (против TOCTOU/DNS-rebinding)
- [x] 4.9 gRPC interceptor-стек (recovery, request-id, otel, auth) для серверов
- [x] 4.10 Тесты пакетов: table-driven + `t.Parallel()`, `goleak` в пакетах с горутинами; покрыть fail-closed auth, SSRF-блокировку, маппинг ошибок httpclient

## 5. Контракты и кодоген (capability: service-contracts)

- [x] 5.1 `.proto`: gateway↔idm (вкл. `CheckAccess(user, resource, action)`) и gateway↔projects (каркас, без доменной реализации)
- [x] 5.2 Кодоген Go-стабов из `.proto` инструментами из `./tools`; шаг проверки воспроизводимости (`git diff --exit-code`) в CI
- [x] 5.3 OpenAPI периметра (портал↔gateway)
- [x] 5.4 Кодоген TS-клиента и zod-схем в `./web`; проверка воспроизводимости
- [x] 5.5 Зафиксировать процесс пометки BREAKING для изменений `.proto`/OpenAPI

## 6. Скелеты сервисов

- [x] 6.1 `gateway` — HTTP-роутер периметра (chi) + gRPC-клиенты к idm/projects; `/readyz`
- [x] 6.2 `idm` — gRPC-сервер (скелет `CheckAccess`), подключение Postgres + DragonflyDB; `/readyz`
- [x] 6.3 `projects` — gRPC-сервер (скелет) + Temporal Client; слои `transport→usecase→temporal client` заложены; `/readyz`
- [x] 6.4 `devinfra-worker` — отдельный worker-процесс (скелет, без activities); сигнал живости в `/readyz`
- [x] 6.5 Graceful shutdown через `context` во всех процессах; `recover` в фоновых горутинах

## 7. Локалка (capability: local-environment)

- [x] 7.1 `docker-compose`: Keycloak, Oauth2-Proxy, Postgres×2 (projects, idm), DragonflyDB, Temporal Server + UI
- [x] 7.2 Сервисы-скелеты (gateway, idm, projects, devinfra-worker) в compose
- [x] 7.3 Mock-серверы GitLab/Vault/Harbor
- [x] 7.4 Keycloak realm (клиент портала, базовые роли) + Oauth2-Proxy перед gateway; проверить OIDC-поток end-to-end
- [x] 7.5 Smoke-проверка: `docker compose up` поднимает стенд, health-эндпоинты сервисов доступны

## 8. Завершение

- [x] 8.1 Прогнать полный CI; убедиться, что pipeline зелёный (race+shuffle, tidy, lint, govulncheck, integration)
- [x] 8.2 Зафиксировать: доменную логику начинаем только после зелёного CI этого изменения
