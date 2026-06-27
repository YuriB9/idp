## Why

Сейчас все три управляемые системы DevInfra — моки, кроме GitLab: change `devinfra-real-gitlab`
(ADR-0019, PR #58, sync/archive #59) сделал реальным ТОЛЬКО GitLab-плечо, оставив Vault и
Harbor WireMock-стабами. HTTP-клиент Vault (`vaultHTTP` в
`services/devinfra-worker/internal/integrations/http.go`) писался под мок и расходится с
реальным HashiCorp Vault API: запросы НЕ несут аутентификацию (`X-Vault-Token`), `RevokeSecretID`
шлёт «destroy all» одним пустым POST (реальному Vault нужен `secret_id`/accessor), `SyncOwners`
создаёт identity-entity без alias, идемпотентные коды угаданы под мок. Чтобы интеграционно
проверять воркфлоу провизии/decommission/transfer/смены владельцев против НАСТОЯЩЕГО Vault (как
уже сделано для GitLab) и зафиксировать auth-модель и раскладку secret-engine, нужно подключить
реальный Vault — следующим плечом после GitLab.

Соответствие плану: `docs/IDP_MVP_plan.md` (интеграции DevInfra-worker с GitLab/Vault/Harbor);
прямое продолжение ADR-0019. Опирается на ADR-0001 (Temporal/activities), ADR-0005 (Saga-откат
создания — компенсация `TeardownAppRole`), ADR-0008 (split воркфлоу), ADR-0011 (owners + Vault
`SyncOwners`), ADR-0012 (decommission → `RevokeSecretID`, необратимо), ADR-0013 (transfer →
`MigratePaths`, частично необратимо), ADR-0018 (E2E-харнесс и стенд-override).

## What Changes

- Поднять РЕАЛЬНЫЙ HashiCorp Vault (dev-режим, `VAULT_DEV_ROOT_TOKEN_ID`) в отдельном
  compose-override (`deploy/compose/docker-compose.vault.yml`) по образцу
  `docker-compose.gitlab.yml`, с health-gate. НЕ ломать локалку/e2e/gitlab-профиль/conformance.
- Доработать `vaultHTTP` до семантики РЕАЛЬНОГО Vault API: заголовок `X-Vault-Token` ТОЛЬКО на
  Vault-запросах (отдельный `doer.headers`, как PRIVATE-TOKEN у GitLab); корректные пути/тела/коды
  KV v2 (`data`-обёртка, write→204, read отсутствующего→404), AppRole, identity; идемпотентность
  через `getFound`/коды, не по тексту. Флаг `real` включает реальную семантику по наличию токена;
  in-memory и мок-путь (WireMock) сохраняются для дефолтного `make e2e` (БЛОК 7).
- Пересмотреть `RevokeSecretID`: у Vault нет «destroy all» одним вызовом — отзыв через перечисление
  и destroy `secret-id-accessors` (или удаление роли) с наблюдаемым результатом. Точка невозврата
  (ADR-0012).
- `main.go` + `integrations.Config`: добавить `VAULT_TOKEN`/`VAULT_TOKEN_FILE` (по образцу
  `gitLabToken()`); выбор реального Vault-клиента vs мок по наличию токена; при необходимости
  фикстура-маппинг владелец→Vault-идентичность (по образцу `GITLAB_OWNER_LOGINS`).
- Детерминированный сид Vault (`deploy/compose/vault-seed/`): root-токен — фикстура стенда,
  `vault auth enable approle`, KV v2 на `secret/`, базовые политики. По образцу `gitlab-seed`.
- Интеграционный набор `tests/e2e/vault_integration_test.go` (build-тег `integration`,
  `requireVault`-гейт), переиспользующий харнесс (`fetchIDToken`/`callAPI`/`waitForStatus`/
  `uniqueName`), ассертящий фактическое состояние через Vault API.
- Makefile: цели `vault-up`/`vault-seed`/`vault-test`/`vault-down`/`vault` по образцу `gitlab-*`.
  Прогон локальный ручной (CI по умолчанию не трогаем; решение обосновано в design).

Контракт периметра (`openapi/openapi.yaml`, `web/src/api/*`, proto, сгенерированный код) НЕ
меняется; `gen:check` остаётся зелёным.

## Capabilities

### New Capabilities
- `devinfra-vault-integration`: подключение реального HashiCorp Vault к DevInfra worker отдельным
  compose-профилем — реальный Vault-клиент (`X-Vault-Token`, KV v2/AppRole/identity, коды/
  идемпотентность), детерминированный сид (root-токен, approle, KV, политики), health-gate,
  интеграционный набор воркфлоу против Vault API, Makefile `vault-*` (локальный ручной прогон).

### Modified Capabilities
- `integration-clients`: уточнение требований к Vault-клиенту — аутентификация `X-Vault-Token`
  (не протекает на GitLab/Harbor), семантика KV v2/AppRole/identity и немедленного отзыва под
  реальный Vault; in-memory/мок-путь сохраняются.

## Impact

- Код: `services/devinfra-worker/internal/integrations/http.go` (`vaultHTTP`, `Config`,
  `NewHTTPClients`), `services/devinfra-worker/main.go` (`vaultToken()`, выбор клиента),
  `internal/integrations/memory.go` (без изменений — остаётся для дефолтного прогона).
- Оркестрация стенда: `deploy/compose/docker-compose.vault.yml`, `deploy/compose/vault-seed/`,
  `Makefile` (`vault-*`).
- Тесты: `tests/e2e/vault_integration_test.go` (+ возможные хелперы харнесса).
- Компенсации/откат провизии: `TeardownAppRole` (откат создания, ADR-0005), `RestoreOwners`
  (смена владельцев, ADR-0011), `RevokeSecretID` необратим (ADR-0012), `MigratePaths` частично
  необратим (ADR-0013) — наблюдаемы против реального Vault.
- НЕ затрагивается: контракт периметра/proto/кодоген (`gen:check` зелёный), реальный Harbor
  (остаётся моком), Kubernetes, продовый Vault/HA/auto-unseal.
- ADR: новый — «Vault auth-модель и раскладка secret-engine» (auth-токен, KV v2, AppRole/identity,
  revoke-семантика).
