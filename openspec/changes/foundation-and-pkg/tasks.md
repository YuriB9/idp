## 1. Сверка и подготовка

- [ ] 1.1 Сверить scope с `docs/IDP_MVP_plan.md` (Этап 1 + «Инженерные стандарты»: БЛОК 0, общие `pkg/*`) и затронутыми ADR-0002, ADR-0003, ADR-0004, ADR-0006; зафиксировать, что доменная логика — вне scope
- [ ] 1.2 Инициализировать git-репозиторий и базовую структуру каталогов (`pkg`, `services`, `tests/e2e`, `tools`, `web`, `deploy`)

## 2. Монорепо go.work (capability: monorepo-workspace)

- [ ] 2.1 Создать `go.mod` для `./pkg`, `services/{gateway,idm,projects,devinfra-worker}`, `./tests/e2e` (Go 1.26)
- [ ] 2.2 Создать `go.work` с `use` для `pkg`, всех сервисов и `tests/e2e` (без `tools`)
- [ ] 2.3 Создать `./tools` с собственным `go.mod` (вне workspace), пин инструментов кодогена/линтинга; зафиксировать запуск с `GOWORK=off`
- [ ] 2.4 Проверить: `go build ./...` через workspace и запуск инструмента с `GOWORK=off` не меняют графы сервисов

## 3. CI и качество (capability: ci-pipeline)

- [ ] 3.1 GitHub Actions: матрица по модулям `go.work` с `go test -race -shuffle=on`
- [ ] 3.2 Шаг проверки `go mod tidy && git diff --exit-code` на каждом модуле
- [ ] 3.3 `.golangci.yml` с набором: errcheck, govet, staticcheck, revive, gosec, bodyclose, sqlclosecheck, nilerr, errname, paralleltest, package-comments; шаг `golangci-lint` в CI
- [ ] 3.4 Блокирующий шаг `govulncheck`
- [ ] 3.5 Отдельный integration-джоб (build-тег `integration`), изолированный от дефолтного прогона
- [ ] 3.6 `.github/dependabot.yml` (gomod по модулям + github-actions)
- [ ] 3.7 `Dockerfile` на каждый сервис (`services/*`) для Trivy/SBOM

## 4. Общие пакеты pkg/* (capability: platform-libraries)

- [ ] 4.1 `pkg/errs` — канонические sentinel'ы (`ErrNotFound`, `ErrConflict`)
- [ ] 4.2 `pkg/logger` — slog-логгер с единым ключом `"err"`
- [ ] 4.3 `pkg/config` — env-хелперы, корректная обработка легитимного `0`
- [ ] 4.4 `pkg/db` — `NewPool` с обязательной конфигурацией пула `*pgxpool.Pool`
- [ ] 4.5 `pkg/httpserver` — таймауты, graceful shutdown (drain `WithTimeout(WithoutCancel(ctx),30s)`), middleware-стек (Recoverer, RequestID, rate-limit, auth-toggle, content-aware `/readyz`)
- [ ] 4.6 `pkg/httpclient` — тюнингованный Transport, маппинг `404→ErrNotFound`/`409→ErrConflict`
- [ ] 4.7 `pkg/auth` — строгий JWT (audience/issuer/validMethods/expirationRequired) + JWKS (https), fail-closed (`JWKS_URL` пуст → `os.Exit(1)`, `AUTH_DISABLED=true`), admin-key через `subtle.ConstantTimeCompare`
- [ ] 4.8 `pkg/ssrf` — `ValidateURL` (https + блок private/loopback/link-local/ULA) и `GuardedDialContext` (против TOCTOU/DNS-rebinding)
- [ ] 4.9 gRPC interceptor-стек (recovery, request-id, otel, auth) для серверов
- [ ] 4.10 Тесты пакетов: table-driven + `t.Parallel()`, `goleak` в пакетах с горутинами; покрыть fail-closed auth, SSRF-блокировку, маппинг ошибок httpclient

## 5. Контракты и кодоген (capability: service-contracts)

- [ ] 5.1 `.proto`: gateway↔idm (вкл. `CheckAccess(user, resource, action)`) и gateway↔projects (каркас, без доменной реализации)
- [ ] 5.2 Кодоген Go-стабов из `.proto` инструментами из `./tools`; шаг проверки воспроизводимости (`git diff --exit-code`) в CI
- [ ] 5.3 OpenAPI периметра (портал↔gateway)
- [ ] 5.4 Кодоген TS-клиента и zod-схем в `./web`; проверка воспроизводимости
- [ ] 5.5 Зафиксировать процесс пометки BREAKING для изменений `.proto`/OpenAPI

## 6. Скелеты сервисов

- [ ] 6.1 `gateway` — HTTP-роутер периметра (chi) + gRPC-клиенты к idm/projects; `/readyz`
- [ ] 6.2 `idm` — gRPC-сервер (скелет `CheckAccess`), подключение Postgres + DragonflyDB; `/readyz`
- [ ] 6.3 `projects` — gRPC-сервер (скелет) + Temporal Client; слои `transport→usecase→temporal client` заложены; `/readyz`
- [ ] 6.4 `devinfra-worker` — отдельный worker-процесс (скелет, без activities); сигнал живости в `/readyz`
- [ ] 6.5 Graceful shutdown через `context` во всех процессах; `recover` в фоновых горутинах

## 7. Локалка (capability: local-environment)

- [ ] 7.1 `docker-compose`: Keycloak, Oauth2-Proxy, Postgres×2 (projects, idm), DragonflyDB, Temporal Server + UI
- [ ] 7.2 Сервисы-скелеты (gateway, idm, projects, devinfra-worker) в compose
- [ ] 7.3 Mock-серверы GitLab/Vault/Harbor
- [ ] 7.4 Keycloak realm (клиент портала, базовые роли) + Oauth2-Proxy перед gateway; проверить OIDC-поток end-to-end
- [ ] 7.5 Smoke-проверка: `docker compose up` поднимает стенд, health-эндпоинты сервисов доступны

## 8. Завершение

- [ ] 8.1 Прогнать полный CI; убедиться, что pipeline зелёный (race+shuffle, tidy, lint, govulncheck, integration)
- [ ] 8.2 Зафиксировать: доменную логику начинаем только после зелёного CI этого изменения
