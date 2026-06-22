## 1. Подготовка и сверка

- [x] 1.1 Свериться с docs/IDP_MVP_plan.md (БЛОК 7, «Тестирование сквозное», «Порядок реализации») и ADR-0001/0004/0005/0009/0011/0012/0013/0016/0017/0018; зафиксировать границы (контракт/proto/кодоген/поведение НЕ трогаем)
- [x] 1.2 Создать ветку `change/e2e-portal-user-stories` от master (прямые коммиты в master запрещены)
- [x] 1.3 Перепроверить инвентаризацию матрицы activity→mock-эндпоинт по `services/devinfra-worker/internal/integrations/http.go` (GitLab/Vault/Harbor) и текущие `deploy/mocks/mappings/*`

## 2. Расширение моков под детерминированный прогон

- [x] 2.1 Дополнить `deploy/mocks/mappings/` маппингами GitLab: `POST/DELETE …/members[/:user]`, `POST …/archive`, `POST …/unarchive`, `POST …/transfer` (коды, как ожидает worker)
- [x] 2.2 Дополнить маппинги Harbor: `PUT /api/v2.0/projects/:name` (read-only), `DELETE /api/v2.0/robots/:name`
- [x] 2.3 Дополнить маппинги Vault: `POST …/secret-id/destroy`, `PUT/DELETE /v1/identity/entity/name/:role-:user`, `GET/PUT/DELETE /v1/secret/data/:role` с корректными телами (`data.role_id`, `data.secret_id`, `secret/data` wrapper)
- [x] 2.4 Локально проверить через :8081 (AUTH_DISABLED) что все 4 воркфлоу доходят до терминального статуса, не опираясь на catch-all `{mock:true}`
- [x] 2.5 Подготовить мок-маппинг сценария Saga-отказа (non-retryable ошибка Vault для маркированного имени сервиса) — отдельным файлом/приоритетом, не ломая happy-path

## 3. Compose-оркестрация для E2E (без поломки локалки)

- [x] 3.1 Добавить `deploy/compose/docker-compose.e2e.yml` (override): gateway без `AUTH_DISABLED`, JWKS по **https** (pkg/auth форсирует https — out of scope менять) → Keycloak слушает 8443 с тест-сертификатом (`make e2e-certs`, gitignored), gateway доверяет CA через `SSL_CERT_FILE`, `AUTH_ISSUER`/`AUTH_AUDIENCE=idp-portal`; oauth2-proxy E2E-cfg с `skip_jwt_bearer_tokens=true`; комментарии на русском
- [x] 3.2 Убедиться, что базовый `docker compose up` и `conformance`-таргет (GET-only на :8081) не затронуты
- [x] 3.3 Заложить health-gate готовности (Keycloak token-эндпоинт, gateway `/healthz`, Temporal) — через скрипт/ожидание, учитывая медленный старт Keycloak/Temporal

## 4. E2E-хелперы (tests/e2e)

- [x] 4.1 Хелпер OIDC: password-grant к Keycloak (`idp-portal`, scope=openid) → `id_token` (aud=idp-portal принимается и oauth2-proxy, и gateway); без новых секретов сверх realm-фикстуры
- [x] 4.2 HTTP-клиент периметра: базовый URL oauth2-proxy (:4180) + `Authorization: Bearer`, вызовы 6 операций по форме REST (ADR-0009)
- [x] 4.3 `waitForStatus(ctx, project, name, want, budget)`: ретраи-поллинг `getService`, интервал ~500ms, бюджет из env (`E2E_STATUS_TIMEOUT`); терминальный `failed` при ожидании `active` → немедленный fatal с диагностикой
- [x] 4.4 `TestMain`/`waitReady`: health-gate стенда перед сценариями; skip всего набора без `GATEWAY_BASE_URL`/`E2E_PROXY_URL`
- [x] 4.5 Утилита уникальных имён сервисов (суффикс) для изоляции и `t.Parallel()`

## 5. Сквозные сценарии 4 user stories (happy-path)

- [x] 5.1 «Создание»: `createService` → `creating→active`; ассерт успешных Saga-activity (GitLab/Vault/Harbor)
- [x] 5.2 «Изменение владельцев»: `setServiceOwners` (полный набор + `owners_version`) → отражение в каталоге и инкремент версии; проверка синхронизации ролей (ADR-0011)
- [x] 5.3 «Decommission»: `decommissionService` с `load_drained=true` → `decommissioned` (soft delete, не purge)
- [x] 5.4 «Перенос»: субъект с правами `transfer`+`transfer_in` → `active→transferring→active`, смена проекта, переезд владельцев (ADR-0013)

## 6. Ключевые ошибки и компенсации

- [x] 6.1 Идемпотентность/конфликт create: повторный `createService` для того же `(project,name)` → 409
- [x] 6.2 Конфликт версии владельцев: `setServiceOwners` с устаревшей `owners_version` → 409, набор не меняется
- [x] 6.3 Decommission предусловие: `load_drained=false`/неподходящий статус → 422, статус не меняется (ADR-0012)
- [x] 6.4 Идемпотентный повтор decommission/transfer на уже изменённом сервисе → 200 без повторных побочных эффектов
- [x] 6.5 (опционально, env-gated) Saga-откат создания: маркированный сервис → `failed` + alert в логах, без молчаливого `active` (ADR-0005)
- [x] 6.6 Заменить заглушку `tests/e2e/smoke_test.go` итоговым набором; сохранить build-тег `integration`; `go mod tidy` (GOWORK=off) зелёный

## 7. Makefile (локальный ручной прогон — единственный способ)

- [x] 7.1 Цели `e2e-up`/`e2e-test`/`e2e-down` (по образцу `conformance`): подъём стенда с E2E-override + health-gate готовности, прогон набора, очистка (`docker compose down -v`); комментарии на русском
- [x] 7.2 Локальный ручной прогон Makefile-цели на чистом стенде до зелёного набора
- [x] 7.3 `.github/workflows/ci.yml` НЕ трогать: стенд в CI не поднимается, E2E там не гоняется

## 8. Финальная проверка

- [x] 8.1 `gen:check` (proto + OpenAPI) без диффа; `openapi/openapi.yaml`, `web/src/api/*`, `web/public/openapi.yaml`, proto и сгенерированный код НЕ менялись
- [x] 8.2 `go test -race -shuffle=on` всех модулей, `golangci-lint`, `govulncheck`, openapi-lint, web-test — зелёные; CI остаётся как был (без подъёма стенда)
- [x] 8.3 Базовая локалка (`docker compose up`) и `conformance`-таргет по-прежнему работают
- [x] 8.4 Локально вручную прогнать `make e2e-*` и убедиться, что весь набор 4 user stories зелёный
- [ ] 8.5 Открыть PR с зелёным CI; после merge — отдельный PR sync+archive через `/opsx:archive`
