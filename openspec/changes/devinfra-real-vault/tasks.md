## 1. Подготовка

- [x] 1.1 Сверить план с `docs/IDP_MVP_plan.md` и ADR-0001/0005/0011/0012/0013/0018/0019/0020; зафиксировать ожидаемые методы/коды реального Vault API ЭМПИРИЧЕСКИ против поднятого dev-Vault (не по памяти)
- [x] 1.2 Создать git-ветку `change/devinfra-real-vault` от актуального `master` (прямые коммиты в master запрещены)
- [x] 1.3 Зафиксировать пиннутый тег образа `hashicorp/vault` и значение фикстуры root-токена (`VAULT_DEV_ROOT_TOKEN_ID`)

## 2. Стенд: реальный Vault в отдельном compose-профиле

- [x] 2.1 Добавить `deploy/compose/docker-compose.vault.yml` (по образцу `docker-compose.gitlab.yml`): сервис Vault в dev-режиме (`VAULT_DEV_ROOT_TOKEN_ID`), healthcheck `GET /v1/sys/health`, проброс порта; переключение `devinfra-worker` на реальный Vault (`VAULT_BASE_URL`, `VAULT_TOKEN`, `SSRF_DISABLED=true`). Комментарии — только на русском
- [x] 2.2 Проверить, что профиль НЕ ломает базовую локалку, e2e-override, gitlab-профиль и conformance (они не ссылаются на сервис vault и не задают воркеру `VAULT_TOKEN`)
- [x] 2.3 Добавить `deploy/compose/vault-seed/seed.sh` (по образцу `gitlab-seed/seed.sh`): дождаться готовности, `vault auth enable approle` (идемпотентно), убедиться, что KV v2 на `secret/`, при необходимости предзасев секрета для transfer-теста. Комментарии — только на русском

## 3. Конфигурация и выбор клиента (services/devinfra-worker)

- [x] 3.1 В `internal/integrations/http.go` добавить в `Config` поле `VaultToken` (env `VAULT_TOKEN`/`VAULT_TOKEN_FILE`); при необходимости фикстуру маппинга владелец→субъект Vault (`VaultOwnerSubjects`, по образцу `GitLabOwnerLogins`)
- [x] 3.2 В `NewHTTPClients` собрать отдельный `vaultDoer` с заголовком `X-Vault-Token` ТОЛЬКО при непустом токене (иначе общий `sharedDoer`); заголовок не должен протекать на GitLab/Harbor; добавить флаг `real` у `vaultHTTP`
- [x] 3.3 В `main.go` добавить `vaultToken()` (читает `VAULT_TOKEN` либо `VAULT_TOKEN_FILE`, по образцу `gitLabToken()`); пробросить `VaultToken`/маппинг в `integrations.Config`; не логировать токен

## 4. Реальная семантика Vault-клиента (vaultHTTP за флагом real)

- [x] 4.1 `SetupAppRole`: проверить пути/тела/коды против реального Vault (политика/роль → 204, `role-id`/`secret-id` → `data.*`); требует включённого approle-движка (сид); убрать мок-заглушки role-id/secret-id в реальном режиме
- [x] 4.2 `TeardownAppRole`: подтвердить идемпотентность (DELETE роли/политики, `404` — успех)
- [x] 4.3 `SyncOwners`/`RestoreOwners`: identity-entity с политикой роли; safe-skip незаданного субъекта (фикстура); идемпотентность upsert/`404`
- [x] 4.4 `RevokeSecretID`: реализовать перечисление активных secret-id-accessors (`LIST`/`GET ?list=true`) и destroy каждого через `secret-id-accessor/destroy`; пустой список/`404` — успех; результат наблюдаем (после отзыва accessors пусты)
- [x] 4.5 `MigratePaths`: подтвердить KV v2 (read разбирает `data.data`, write шлёт `{"data":{...}}`), целевая политика, очистка исходного пути (`404` — успех)
- [x] 4.6 Пересмотреть `okExtra` под Vault (убрать угаданные под мок `409`; опираться на `getFound`/коды, не на текст); сохранить мок-путь при `real=false`
- [x] 4.7 Убедиться, что `internal/integrations/memory.go` (HasVault/HasVaultOwner/IsVaultRevoked/VaultPath) НЕ изменён и остаётся для дефолтного прогона

## 5. Интеграционные тесты (tests/e2e)

- [x] 5.1 Добавить `tests/e2e/vault_integration_test.go` (build-тег `integration`) с `requireVault`-гейтом по env (`VAULT_ADDR`/`VAULT_TOKEN`), хелперами доступа к Vault API и skip без gate-env; переиспользовать харнесс (`fetchIDToken`/`callAPI`/`waitForStatus`/`uniqueName`); goleak — только где есть горутины
- [x] 5.2 Тест создания: воркфлоу → `active`, ассерт через Vault API (роль и per-service политика существуют, `role-id` читается)
- [x] 5.3 Тест decommission: → `decommissioned`, ассерт отзыва (активные secret-id-accessors пусты)
- [x] 5.4 Тест transfer: → `active`, ассерт миграции (секреты по целевому пути, целевая политика, исходный путь очищен)
- [x] 5.5 Тест смены владельцев: ассерт identity-entity владельца с политикой роли (учесть одну успешную смену владельцев на сервис — `e2e-change-owners-single-shot`)

## 6. Makefile

- [x] 6.1 Добавить `COMPOSE_VAULT` и переменные (`VAULT_ADDR`, `VAULT_TOKEN`-фикстура, бюджет ожидания — мал в сравнении с GitLab) по образцу gitlab-секции
- [x] 6.2 Добавить цели `vault-up`/`vault-seed`/`vault-test`/`vault-down`/`vault`/`vault-logs` по образцу `gitlab-*`; прогон только локальный, CI не трогать

## 7. Верификация

- [x] 7.1 Прогнать дефолтный путь зелёным: `go test` всех модулей `-race -shuffle`, golangci-lint, govulncheck, `gen:check` (контракт+кодоген БЕЗ изменений), openapi-lint, web-test
- [x] 7.2 Локально прогнать `make vault` (up → seed → integration-набор → down) против реального Vault; подтвердить ассерты создания/decommission/transfer/смены владельцев
- [x] 7.3 Подтвердить, что `make e2e` и `make gitlab` не затронуты (мок-путь и gitlab-профиль работают)
- [ ] 7.4 Открыть PR с ветки `change/devinfra-real-vault`; добиться зелёного CI; после merge — отдельный PR `/opsx:archive` (sync+archive по образцу #59)
