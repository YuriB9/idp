# ADR-0020: Модель аутентификации worker→Vault и раскладка secret-engine

**Status:** Accepted
**Date:** 2026-06-23
**Change:** devinfra-real-vault

## Context

После `devinfra-real-gitlab` (ADR-0019) реальным сделано ТОЛЬКО GitLab-плечо DevInfra; Vault и Harbor
остаются WireMock-моками. HTTP-клиент Vault (`vaultHTTP` в
`services/devinfra-worker/internal/integrations/http.go`) писался под мок и расходится с реальным
HashiCorp Vault API: запросы НЕ несут аутентификацию (`X-Vault-Token`); `RevokeSecretID` шлёт «destroy
all» одним пустым `POST .../secret-id/destroy` (реальному Vault нужен `secret_id`/accessor, а «отозвать
все» одним вызовом нельзя); `SyncOwners` создаёт identity-entity без alias; идемпотентные коды
(`409`/`404`) угаданы под мок.

Это изменение (граница MVP) подключает РЕАЛЬНЫЙ Vault (dev-режим) вместо мока ТОЛЬКО для Vault-плеча
(GitLab — реальный или мок по профилю, Harbor — мок), не меняя контракт периметра, proto и
сгенерированный код. Нужно зафиксировать аутентификацию worker→Vault, раскладку secret-engine
(KV v2/AppRole/identity) и семантику немедленного отзыва. Опирается на ADR-0001 (Temporal/activities),
ADR-0005 (Saga-откат создания — компенсация `TeardownAppRole`), ADR-0011 (owners + Vault `SyncOwners`),
ADR-0012 (decommission → `RevokeSecretID`, необратимо), ADR-0013 (transfer → `MigratePaths`, частично
необратимо), ADR-0018 (E2E-харнесс), ADR-0019 (образец: реальный GitLab, статический токен,
идемпотентность по кодам).

## Decision

- **Аутентификация — `X-Vault-Token` со статическим dev root-токеном.** Vault стартует в dev-режиме с
  `VAULT_DEV_ROOT_TOKEN_ID` (известная фиксированная фикстура стенда). Каждый запрос worker→Vault несёт
  заголовок `X-Vault-Token: <token>` из конфигурации (`integrations.Config.VaultToken`, env
  `VAULT_TOKEN`/`VAULT_TOKEN_FILE`). Токен не логируется; на GitLab/Harbor не распространяется (отдельный
  `doer` с заголовком, как PRIVATE-TOKEN у GitLab). AppRole-логин самого worker отклонён: для одного
  сервис-актора на тест-стенде статический токен проще и детерминированнее, без обработки TTL/refresh
  (тот же аргумент, что для PAT в ADR-0019). Секрет — фикстура стенда, в код сверх неё не хардкодится.

- **Раскладка движков — dev-дефолты + сид только инфраструктуры.** В dev-режиме `secret/` уже
  смонтирован как KV v2, поэтому `MigratePaths` (`/v1/secret/data/...`, обёртка `{"data":{...}}`,
  разбор `data.data`) корректен без изменений. Сид стенда выполняет лишь `vault auth enable approle`
  (без этого `SetupAppRole` → 404) и при необходимости предзасев секрета для transfer-теста. Per-service
  ACL-политики и AppRole-роли создаёт САМ клиент в воркфлоу провизии, а не сид.

- **Немедленный отзыв — перечисление accessors + destroy, не «destroy all».** `RevokeSecretID`
  перечисляет активные secret-id роли (`LIST`/`GET ?list=true`) и уничтожает каждый через
  `secret-id-accessor/destroy`. Роль и role-id сохраняются (контракт ADR-0012 — отозвать ДОСТУП, не
  снести роль). Идемпотентно: пустой список/`404` — успех. Наблюдаемость: после отзыва набор активных
  accessors пуст.

- **Маппинг владелец→Vault-идентичность — entity по фикстуре, safe-skip.** `SyncOwners` создаёт
  identity-entity с политикой роли; полная привязка субъекта к аутентификации (entity-alias с
  `mount_accessor`) вне scope MVP (как owner→login упрощён в GitLab). Незаданный субъект безопасно
  пропускается. Ассерт — существование entity с политикой роли.

- **Идемпотентность — по кодам, не по тексту.** Запись (policy/role/kv/entity) → `200`/`204` (успех);
  чтение/удаление отсутствующего → `404` (успех/no-op через `getFound`). `okExtra` пересмотрен под Vault
  (убраны угаданные под мок `409` — Vault использует upsert/`204`).

- **Выбор реализации по наличию `VAULT_TOKEN`.** Задан токен → клиент против реального Vault; иначе —
  клиент против мока (поведение по умолчанию). In-memory стаб остаётся для дефолтного прогона.
  SSRF-guard для стендового Vault на приватном http-адресе выключается `SSRF_DISABLED=true`; в проде
  guard включён.

## Consequences

- Реальный Vault поднимается отдельным compose-override (`docker-compose.vault.yml`) — только
  локально/ручно, не в CI и не в дефолтной локалке; health-gate на `GET /v1/sys/health` (дев-старт —
  секунды, бюджет ожидания мал в сравнении с GitLab).
- Компенсации наблюдаемы против реального Vault: `TeardownAppRole` (откат создания, ADR-0005),
  `RestoreOwners` (ADR-0011), `RevokeSecretID` необратим (ADR-0012), `MigratePaths` частично необратим
  (ADR-0013).
- Контракт периметра/proto/кодоген не затрагиваются (`gen:check` зелёный).
- Dev-режим — НЕ для прода: продовая auth-модель (AppRole worker-а, auto-unseal/HA/raft) вне scope и
  отмечена как Non-Goal.
