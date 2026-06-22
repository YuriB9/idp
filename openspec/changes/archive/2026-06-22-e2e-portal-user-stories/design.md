## Context

Воркфлоу (create/change-owners/decommission/transfer), периметр REST (ADR-0009) и портал уже реализованы и заархивированы в master. Незакрыт сквозной тест-слой: `tests/e2e` содержит только `TestPerimeterHealthz`. План (БЛОК 7, «Тестирование сквозное») требует прогон 4 user stories через портал-периметр на docker-compose с РЕАЛЬНЫМИ Keycloak + Oauth2-Proxy. Прогон задуман ЛОКАЛЬНЫМ, ручным (`docker compose` + Makefile-цель); подъём стенда в CI вне границ этого change — CI-`integration`-джоб остаётся как есть (компиляция e2e + repository-тесты).

Фактическое состояние стенда (`deploy/compose/docker-compose.yml`):
- `keycloak` (:8088→8080, realm `idp` импортируется), `oauth2-proxy` (:4180, provider `keycloak-oidc`, upstream `http://gateway:8080`, `pass_authorization_header`/`set_authorization_header`, `skip_provider_button`).
- `gateway` публикует :8081, локально `AUTH_DISABLED=true`, `AUTH_DISABLED_SUBJECT=11111111-...-111` (это `sub` пользователя `dev`).
- `projects` (API+wfstarter), `devinfra-worker` (Temporal-activity к мокам), `idm` (role-sync, читает каталог субъектов из Keycloak), `temporal` (health-gate есть), 3× WireMock (`mock-gitlab/vault/harbor` монтируют один и тот же `deploy/mocks/mappings`).
- Realm: клиент `idp-portal` (secret `idp-portal-secret`, `directAccessGrantsEnabled=true`), пользователи `dev/alice/bob` (пароль = логин).

Детерминированный `WorkflowID` уже есть (`provisioning.WorkflowID(project,name)` и аналоги), политика `WORKFLOW_ID_CONFLICT_POLICY_FAIL` + `REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY` — повторный конкурентный create отклоняется (→ 409 на периметре).

Матрица фактических HTTP-вызовов activity (`services/devinfra-worker/internal/integrations/http.go`):
- GitLab: `POST /api/v4/projects`, `POST/DELETE …/variables`, `POST/DELETE …/members[/:user]`, `POST …/archive`, `POST …/unarchive`, `POST …/transfer`, `DELETE …/projects/:id`.
- Harbor: `POST /api/v2.0/projects`, `POST /api/v2.0/robots`, `PUT /api/v2.0/projects/:name` (read-only), `DELETE /api/v2.0/projects/:name`, `DELETE /api/v2.0/robots/:name`.
- Vault: `PUT/DELETE /v1/sys/policies/acl/:role`, `POST /v1/auth/approle/role/:role`, `GET …/role-id`, `POST …/secret-id`, `POST …/secret-id/destroy`, `PUT/DELETE /v1/identity/entity/name/:role-:user`, `GET/PUT/DELETE /v1/secret/data/:role`.

Текущие маппинги (`devinfra.json`) покрывают create-путь и часть delete; `health.json` содержит catch-all `ANY /.*` → 200 `{mock:true}` (это спасает неперечисленные ручки от 404, но не отдаёт нужные тела, например `data.role_id`).

## Goals / Non-Goals

**Goals:**
- Сквозной набор Go-тестов (`integration`), по одному happy-path на каждую из 4 user stories + ключевые ошибки (идемпотентность/конфликт create, soft-delete decommission + 422, transfer-идемпотентность/PONR, конфликт версии owners).
- Реальный OIDC-путь: токен от Keycloak (password-grant) → запросы через oauth2-proxy (:4180), не обход `AUTH_DISABLED`.
- Детерминизм: расширенные моки доводят все activity до конца; ожидание статусов через ретраи-поллинг с таймаут-бюджетом, без `sleep`.
- Локальный прогон через Makefile-цель (ручной `docker compose up` стенда + health-gate + прогон набора) — единственный способ запуска.

**Non-Goals:**
- Любые изменения контракта/поведения: `openapi/openapi.yaml`, `proto`, сгенерированный код, RBAC, миграции, бизнес-логика воркфлоу/activity (`gen:check` зелёный).
- Kubernetes-деплой (Этап 5) — отдельный change.
- Реальные GitLab/Vault/Harbor; замена поллинга на SSE/WebSocket; редизайн портала/`web/src`.
- Поломка существующей AUTH_DISABLED-локалки и `conformance`-таргета.

## Decisions

### Решение 1: Аутентификация E2E — реальный OIDC через oauth2-proxy (ADR-0018)

E2E ходит на `oauth2-proxy` (:4180), а не на gateway (:8081). Токен получаем программно: `POST http://keycloak:8088/realms/idp/protocol/openid-connect/token` с `grant_type=password`, `client_id=idp-portal`, `client_secret=idp-portal-secret`, `username/password` из `dev/alice/bob`. oauth2-proxy при `pass_authorization_header=true` принимает `Authorization: Bearer <access_token>` и проксирует на gateway; gateway в E2E-профиле работает с **включённой** проверкой JWT (`AUTH_DISABLED` снят, `JWKS_URL=http://keycloak:8080/realms/idp/protocol/openid-connect/certs`).

- **Почему**: план явно требует «реальный Keycloak/Oauth2-Proxy»; обход через `AUTH_DISABLED` не проверяет OIDC-цепочку, аудиторию/issuer JWT и связку `sub`→RBAC. Разные пользователи (`dev`/`alice`/`bob`) дают разные `sub` → проверяемые сценарии прав (403).
- **Совместимость с локалкой**: дефолтная локалка (`docker compose up`) остаётся с `AUTH_DISABLED=true` и портал ходит напрямую в gateway. E2E-режим включается **отдельным compose-override** (`docker-compose.e2e.yml`), который снимает `AUTH_DISABLED` у gateway, задаёт `JWKS_URL` и направляет тесты на :4180. Базовый файл и `conformance` (GET-only на :8081) не трогаем.
- **Альтернативы**: (а) обход `AUTH_DISABLED` — отвергнут (не выполняет требование плана, не тестирует периметр аутентификации); (б) cookie-сессия oauth2-proxy через эмуляцию browser-login — отвергнут (хрупко, требует HTML-парсинга; password-grant + Bearer проще и детерминированнее, `skip_provider_button` уже включён).

### Решение 2: Модель детерминизма воркфлоу и таймаут-бюджеты

Мутации периметра асинхронны (запускают Temporal-workflow и сразу отвечают 201/200). E2E **не** спит, а поллит `getService` до целевого статуса:
- helper `waitForStatus(ctx, client, project, name, want, budget)` с интервалом ~500ms и общим бюджетом: create→active и transfer ~60s, decommission ~30s, change-owners ~30s. Бюджет настраивается env (`E2E_STATUS_TIMEOUT`).
- Терминальные статусы (`active`/`decommissioned`/`failed`) детектируются явно; `failed` при ожидании `active` → немедленный `t.Fatalf` с диагностикой (не ждать весь бюджет).
- Готовность стенда: health-gate перед тестами — `oauth2-proxy`/gateway `/healthz` + успешный токен-эндпоинт Keycloak + `temporal health`; реализуется коротким `waitReady` в `TestMain` (плюс ожидание в Makefile-цели перед запуском набора).

### Решение 3: Изоляция и очистка между сценариями

- Уникальные имена: каждый тест генерирует `name` с суффиксом (timestamp/rand) → разные `WorkflowID`, нет межтестовых гонок. `t.Parallel()` безопасен на уровне story (разные сервисы), но шаги внутри одного сервиса последовательны.
- Очистка не требует purge (soft-delete семантика): тесты не зависят от пустого каталога, ассертят конкретную свою запись. Стенд поднимается чистым на каждый локальный прогон (`docker compose down -v` в Makefile-цели).
- Идемпотентность проверяется намеренно (повторный create/decommission/transfer), а не как побочный эффект очистки.

### Решение 4: Расширение моков под детерминированный прогон

Добавляем недостающие маппинги в `deploy/mocks/mappings/` (GitLab `members`/`archive`/`unarchive`/`transfer`; Harbor `robots` DELETE и `projects` PUT read-only; Vault `identity/entity`, `secret/data` GET/PUT/DELETE, `secret-id/destroy`) с корректными телами там, где activity читает ответ (`role-id`, `secret-id`, `secret/data`). Catch-all из `health.json` оставляем как страховку, но happy-path не должен на него опираться.

Матрица «user story → activity → mock-эндпоинт → ассерт» (детально — в tasks). Сценарий компенсации (Saga-откат создания, ADR-0005) моделируется отдельным мок-профилем/маппингом, заставляющим Vault-шаг вернуть non-retryable ошибку для специального имени сервиса (например, `name` с префиксом-маркером) → ассерт `failed` без молчаливого отката. Решение о включении провального сценария в обязательный набор vs отдельный опциональный тест — см. Open Questions.

### Решение 5: decommission-предусловие снятой нагрузки (ADR-0012)

K8s-worker в MVP нет, поэтому предусловие моделируется явным чек-флагом `load_drained` в теле `decommissionService` (контракт уже это содержит). E2E:
- happy-path: `load_drained=true` → `decommissioned`;
- негатив: `load_drained=false` (или статус не `active`) → 422 (PreconditionFailed), статус не меняется.
Это согласуется с открытым вопросом ADR-0012 (чек-флаг, а не прямой запрос к кластеру) — фиксируем в ADR-0018 ссылкой.

### Решение 6: Место спеки — только новая capability e2e-portal-testing

- Новая capability `e2e-portal-testing`: требования к покрытию user stories, реальному OIDC, детерминизму, изоляции, наблюдению Saga/PONR (поведенческий контракт самого тест-слоя), а также к ЛОКАЛЬНОМУ ручному прогону через Makefile-цель.
- **Почему не трогаем `ci-pipeline`**: подъём стенда в CI явно вне границ — прогон только локальный, ручной. Требование «Integration-джоб» остаётся без изменений. Менять capability `ci-pipeline` не нужно.

## Sequence-диаграмма (создание сервиса, happy-path)

```
E2E-тест        oauth2-proxy(:4180)   gateway      projects(API)   Temporal   devinfra-worker   GitLab/Vault/Harbor(mocks)   projects(DB)
   |  token (password-grant) ----------------------> Keycloak                                                                    |
   |  POST /api/projects/{p}/services  (Bearer) ---->|                                                                           |
   |                          --proxy-->  gateway --> projects.CreateService                                                     |
   |                                                  | guarded-CAS insert status=creating ----------------------------------->  |
   |                                                  | StartCreateService (WorkflowID=p/name, CONFLICT=FAIL) --> Temporal       |
   |  <-- 201 {id,status:creating} ------------------|                                                                           |
   |                                                  |                         Temporal --> worker: activity GitLab repo ------> 201
   |                                                  |                                         activity Vault policy/AppRole --> 204/200
   |                                                  |                                         activity Harbor project/robot --> 201
   |                                                  |                         worker: guarded-CAS creating->active ----------------------------> active
   |  poll GET /api/.../services/{name} (ретраи до active, бюджет 60s) ------------------------------------------------------>  active
   |  повторный POST createService --> projects: WorkflowID-конфликт / занятое имя --> 409                                       |
```

При окончательной недоступности Vault (мок-профиль отказа): worker запускает компенсации (ADR-0005), финальный guarded-CAS creating→failed → поллинг видит `failed` + alert в логах; молчаливого отката (active без ресурсов) нет.

Идемпотентность: `WorkflowID` детерминирован по `(project,name)`; `WORKFLOW_ID_CONFLICT_POLICY_FAIL` отвергает второй конкурентный запуск, `REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY` разрешает рестарт только после терминального `failed`. RetryPolicy/таймауты activity — как в существующих воркфлоу (E2E их не меняет, только наблюдает исход).

## Risks / Trade-offs

- **Флакелы из-за времени старта стенда (Keycloak ~40s, Temporal auto-setup ~30-60s)** → health-gate перед тестами (Keycloak token-эндпоинт + temporal health + gateway/healthz), щедрые, но конечные бюджеты, ретраи вместо sleep; Makefile-цель ждёт готовности перед запуском набора.
- **Catch-all мок маскирует реальные пробелы маппингов** → happy-path-ассерты не должны зависеть от `{mock:true}`; добавляем конкретные маппинги с реальными телами; при возможности — проверка через WireMock journal не в первой итерации.
- **Двойной режим gateway (AUTH_DISABLED vs JWKS)** усложняет compose → изолируем в override-файл; базовый compose и conformance не трогаем; документируем в комментариях (на русском).
- **Password-grant хардкодит креды** → используем только уже существующие в realm-фикстуре (`idp-portal-secret`, `dev/alice/bob`), новых секретов не вводим; сырые внутренние ошибки в ассертах как контракт не светим.
- **Сценарий отказа Vault может стать хрупким** → если включаем, изолируем спец-именем сервиса и отдельным маппингом; при риске флака выносим в опциональный (env-gated) тест — см. Open Questions.

## Migration Plan

1. Ветка `change/e2e-portal-user-stories` от master.
2. Инкрементально (каждый шаг — зелёный):
   a. Расширить мок-маппинги; проверить локально, что 4 воркфлоу доходят (через :8081 AUTH_DISABLED для быстрой проверки).
   b. Добавить `docker-compose.e2e.yml` override (gateway с JWKS, без AUTH_DISABLED) + Makefile-цели (`e2e-up`, `e2e-test`, `e2e-down`).
   c. Написать e2e-хелперы (OIDC-токен, HTTP-клиент периметра, waitForStatus, waitReady в TestMain) + 4 happy-path.
   d. Добавить ключевые ошибки (409 create, 409 owners-version, 422 decommission, идемпотентность transfer/decommission).
3. PR с зелёным CI (все существующие проверки без изменений — стенд в CI не поднимается; E2E проверяется только локально ручным `make e2e-*`). Merge → отдельный PR sync+archive (`/opsx:archive`).

**Rollback**: тест-слой и оркестрация аддитивны; откат = revert ветки. Контракт/поведение не менялись, миграции/данные не затронуты.

## Open Questions

- **Сценарий отказа Vault (Saga-откат) — обязательный или опциональный?** Предлагается: включить как отдельный тест с спец-именем сервиса и выделенным маппингом; при флакелости — пометить `t.Skip` под env-флагом, оставив happy-path обязательными. Финал — в ходе apply по факту стабильности.
- **PONR-частичный сбой переноса**: достаточно ли ассертить «алерт в логах + отсутствие молчаливого отката», или нужен явный наблюдаемый признак (статус `transferring`, не вернувшийся в `active`)? Базово ассертим happy-path + идемпотентный повтор; провальный PONR — наблюдение статуса/логов, без изменения контракта.
- **Гранулярность health-gate локально**: `waitReady` в `TestMain` vs ожидание в Makefile-цели (`docker compose ... up --wait`/скрипт), учитывая что у oauth2-proxy сейчас нет healthcheck. Решаем при реализации Makefile-цели.
