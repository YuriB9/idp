## Why

Тест-слой плана (docs/IDP_MVP_plan.md, БЛОК 7 и раздел «Тестирование (сквозное, со 2-го этапа)») требует прогонять все 4 user stories end-to-end через портал-периметр на docker-compose-стенде с РЕАЛЬНЫМИ Keycloak + Oauth2-Proxy — до начала инфраструктурных работ (K8s). Сейчас `tests/e2e` — заглушка: единственный `TestPerimeterHealthz` бьёт `/healthz`. Воркфлоу, периметр и портал уже реализованы и заархивированы; не покрыт именно сквозной слой. Закрываем разрыв до перехода к Этапу 5. Прогон E2E — только ЛОКАЛЬНЫЙ, ручным запуском `docker compose` (Makefile-цель); в CI стенд не поднимается.

## What Changes

- **Заменяем smoke-заглушку** `tests/e2e/smoke_test.go` полноценным сквозным набором (build-тег `integration`), покрывающим 4 user stories через периметр по форме REST (ADR-0009), плюс ключевые ошибки:
  - «Создание сервиса»: `createService` → ожидание `creating→active` (поллинг `getService`/`listServices` с таймаутом и ретраями, без `sleep`); идемпотентность по детерминированному `WorkflowID` (повторный create → 409); Saga-успех против моков GitLab/Vault/Harbor.
  - «Изменение владельцев»: `setServiceOwners` (декларативный diff + `owners_version` optimistic-concurrency) → отражение в каталоге и синхронизация ролей (ADR-0011); конфликт версии → 409.
  - «Удаление/decommission»: `decommissionService` с `load_drained` → `decommissioned` (soft delete, не purge), отзыв доступов; повтор → 200 (идемпотентность); невыполненное предусловие → 422.
  - «Перенос сервиса»: `transferService` (двойная авторизация `transfer`+`transfer_in`) → проверка «точки невозврата» (ADR-0013); идемпотентный повтор → 200.
- **Аутентификация через реальный OIDC**: e2e получает токен у Keycloak (password-grant клиента `idp-portal`, `directAccessGrantsEnabled=true`, пользователи `dev/alice/bob`) и ходит через `oauth2-proxy` (:4180), а НЕ напрямую в gateway с `AUTH_DISABLED`. Выбор обоснован в design (новый ADR-0018).
- **Детерминизм прогона**: расширяем мок-маппинги (`deploy/mocks/mappings/*`) под полный путь activity 4 воркфлоу (GitLab members/archive/unarchive/transfer; Vault identity/KV/secret-id destroy; Harbor robots/read-only update), чтобы все шаги доходили предсказуемо; при необходимости — сценарный мок отказа (Saga-откат создания, ADR-0005).
- **Compose-оркестрация для E2E**: отдельный override/профиль для прогона через oauth2-proxy, НЕ ломая текущую AUTH_DISABLED-локалку и `conformance`-таргет.
- **Makefile**: цель(и) подъёма стенда и прогона E2E локально, ручным запуском (по образцу `conformance`) — единственный способ прогона набора. CI не трогаем: стенд в CI не поднимается, E2E там не гоняется.

Контракты НЕ меняются: `openapi/openapi.yaml`, `proto`, сгенерированный код, RBAC, миграции, бизнес-логика воркфлоу/activity остаются как есть — `gen:check` обязан остаться зелёным. План отката/компенсаций провизии НЕ меняется: e2e лишь наблюдает существующее поведение Saga (ADR-0005) и PONR (ADR-0013).

## Capabilities

### New Capabilities
- `e2e-portal-testing`: сквозное E2E-тестирование портала — требования к покрытию всех 4 user stories через периметр, реальному OIDC-пути (Keycloak + Oauth2-Proxy), детерминизму прогона воркфлоу (ретраи ожидания статуса вместо sleep, таймаут-бюджеты), изоляции/очистке между сценариями и наблюдению Saga/PONR без изменения контракта.

### Modified Capabilities
<!-- Нет: CI не меняется. E2E прогоняется только локально ручным запуском docker compose; требования к pipeline не затрагиваются. -->

## Impact

- **Код/тесты**: `tests/e2e/` (новый набор, хелперы OIDC-аутентификации и HTTP-клиента периметра, ожидание статусов); возможно `tests/e2e/go.mod` (добавление зависимостей для тестов — без правок `pkg`).
- **Деплой**: `deploy/mocks/mappings/*` (расширение), `deploy/compose/` (override/профиль для E2E через oauth2-proxy); `deploy/keycloak`, `deploy/oauth2-proxy` — переиспользуются, без новых секретов сверх realm-фикстуры.
- **Сборка**: `Makefile` (цели подъёма стенда/прогона E2E локально). `.github/workflows/ci.yml` НЕ меняется.
- **Затрагиваемые сервисы и границы**: только наблюдаемое поведение — периметр gateway (REST), Temporal-воркфлоу `services/projects` + `services/devinfra-worker` (activity к мокам), `idm` (role-sync). gRPC/Temporal-границы и контракты НЕ меняются.
- **Документация**: новый ADR-0018 (путь аутентификации E2E + модель детерминизма); сверка с ADR-0001/0004/0005/0009/0011/0012/0013/0016/0017.
