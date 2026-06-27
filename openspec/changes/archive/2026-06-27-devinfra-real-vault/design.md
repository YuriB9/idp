## Context

DevInfra worker (`services/devinfra-worker`) исполняет Temporal-воркфлоу провизии и компенсаций
против GitLab/Vault/Harbor через узкие интерфейсы (`integration-clients`). После `devinfra-real-gitlab`
(ADR-0019) реальным сделано ТОЛЬКО GitLab-плечо; Vault остаётся WireMock-моком на `:9082`
(`VAULT_BASE_URL=http://mock-vault:8080`). Текущий `vaultHTTP`
(`services/devinfra-worker/internal/integrations/http.go:506-621`) писался под мок:

- **Нет аутентификации.** `doer` Vault — общий `sharedDoer` без заголовков; `X-Vault-Token` не
  ставится. Реальный Vault на любой `/v1/...` без токена отвечает `403`.
- **`SetupAppRole`** (`:513`): `PUT /v1/sys/policies/acl/<role>` (политика), `POST /v1/auth/approle/role/<role>`,
  `GET .../role-id`, `POST .../secret-id`. Под реальный Vault: запись политики/роли → `204` (без тела),
  `role-id`/`secret-id` возвращают `data.role_id`/`data.secret_id` — это уже верно. AppRole-движок
  ДОЛЖЕН быть включён сидом (`vault auth enable approle`) — иначе `/v1/auth/approle/...` → `404`.
- **`TeardownAppRole`** (`:551`): `DELETE` роли и политики, `404` как успех — совпадает с реальным Vault
  (`204` на delete, `404` идемпотентно).
- **`SyncOwners`** (`:559`): `PUT /v1/identity/entity/name/<role>-<user>` с `policies`. Реальный Vault
  СОЗДАЁТ entity, но привязка субъекта к аутентификации требует entity-alias (`canonical_id` +
  `mount_accessor`). Для стенда entity достаточно для ассерта (как owner→login в GitLab — упрощён
  фикстурой).
- **`RevokeSecretID`** (`:584`): `POST .../secret-id/destroy` с пустым телом. Реальному Vault нужен
  `secret_id` или `secret_id_accessor`; «destroy all» одним вызовом НЕТ. Это разрыв.
- **`MigratePaths`** (`:592`): ходит в `/v1/secret/data/...` (KV v2), read ждёт `data.data`, write шлёт
  `{"data":{...}}` — это уже верная форма KV v2. `secret/` в dev-режиме Vault смонтирован как KV v2 по
  умолчанию — совпадает.

Ограничения: комментарии в коде — только на русском; ветка `change/devinfra-real-vault` от master;
SSRF-guard сохранить (на стенде `SSRF_DISABLED=true`); БЛОК 7 (integration-тег, in-memory/мок в
дефолте); `gen:check` зелёный; контракт периметра/proto не трогать.

## Goals / Non-Goals

**Goals:**
- Реальный Vault (dev-режим) в отдельном compose-override; health-gate; не ломая
  локалку/e2e/gitlab-профиль/conformance.
- `vaultHTTP` под реальный Vault API: `X-Vault-Token` только на Vault, KV v2/AppRole/identity, коды,
  идемпотентность через `getFound`/коды; флаг `real`; in-memory/мок сохранены.
- Наблюдаемый немедленный отзыв (`RevokeSecretID`) под реальный Vault.
- Детерминированный сид (root-токен, approle, KV v2, политики) по образцу `gitlab-seed`.
- Интеграционный набор воркфлоу против Vault API (тег `integration`, `requireVault`); Makefile `vault-*`.

**Non-Goals:**
- Реальный Harbor (остаётся моком).
- Изменение контракта периметра/proto/кодогена.
- Kubernetes; продовый Vault/HA/auto-unseal/raft/Vault Agent Injector; ротация секретов на проде.
- AppRole-логин самого worker (берём статический dev root-токен — фикстура стенда).
- Замена поллинга на SSE; редизайн портала.

## Decisions

### D1. Аутентификация worker→Vault: статический dev root-токен (фикстура), `X-Vault-Token`

Vault стартует в dev-режиме с `VAULT_DEV_ROOT_TOKEN_ID` — известным фиксированным root-токеном
(фикстура стенда, как `glpat-...` в `gitlab-seed`). Worker аутентифицируется этим токеном через
заголовок `X-Vault-Token` на КАЖДОМ Vault-запросе. Токен подаётся через `integrations.Config.VaultToken`
(env `VAULT_TOKEN` или `VAULT_TOKEN_FILE`, как `gitLabToken()`); в код сверх фикстуры не хардкодится;
не логируется.

Реализация: в `NewHTTPClients` — отдельный `vaultDoer` с `headers: {"X-Vault-Token": cfg.VaultToken}`
ТОЛЬКО при непустом токене (иначе общий `sharedDoer` без заголовка — мок-путь). Заголовок не протекает
на GitLab/Harbor (у каждого свой `doer`). Это зеркалит `gitlabDoer` (`http.go:67-70`).

**Альтернатива (отклонена):** AppRole-логин самого worker (worker логинится role-id/secret-id, получает
token, рефрешит). Сложнее: bootstrap secret-id, обработка TTL/refresh. Для одного сервис-актора на
тест-стенде статический root-токен проще и детерминированнее (тот же аргумент, что в ADR-0019 для PAT).

### D2. Раскладка secret-engine: dev-дефолты + сид только для approle/политик

В dev-режиме Vault `secret/` уже смонтирован как **KV v2** — `MigratePaths` (`/v1/secret/data/...`,
`data`-обёртка) корректен без изменений. Сид ДОЛЖЕН лишь:
1. дождаться готовности (`GET /v1/sys/health`);
2. `vault auth enable approle` (включить AppRole-движок — без него `SetupAppRole` → 404);
3. (опц.) предзаписать базовый секрет в `secret/data/<demo-сервис>` для проверки `MigratePaths`
   source→target в transfer-тесте.

Per-service политики/роли создаёт САМ клиент (`SetupAppRole`), не сид — это часть воркфлоу провизии.
Сид = только инфраструктура движка (approle enable + KV v2 готов).

### D3. Семантика немедленного отзыва `RevokeSecretID` — перечисление accessors + destroy

«destroy all» одним вызовом в Vault нет. Реальный отзыв всех активных secret-id роли:
`LIST /v1/auth/approle/role/<role>/secret-id` (метод `LIST` = `GET` с `?list=true`) → список
`keys` (это `secret_id_accessor`), затем по каждому
`POST /v1/auth/approle/role/<role>/secret-id-accessor/destroy` с телом `{"secret_id_accessor": "<a>"}`.
Идемпотентность: пустой список (нет активных) → no-op-успех (`404` на LIST = успех). Наблюдаемость
отзыва в тесте: после `RevokeSecretID` повторный `LIST` возвращает пустой набор accessors (или role-id
больше не логинится). Альтернатива «удалить роль целиком» отклонена: теряем role-id (нужен для ассертов
и для возможного re-provision), а контракт ADR-0012 — отозвать ДОСТУП (secret-id), не снести роль.

Под мок (`real=false`) сохраняется текущий путь (`POST .../secret-id/destroy`, `404`→успех).

### D4. Маппинг владелец→Vault-идентичность — entity по фикстуре, safe-skip

`SyncOwners` создаёт identity-entity `PUT /v1/identity/entity/name/<role>-<subject>` с `policies`.
Полная привязка субъекта к аутентификации (entity-alias с `mount_accessor`) ВНЕ scope MVP (как
owner→login упрощён в GitLab). В реальном режиме субъект (UUID) при необходимости отображается
фикстурой `VAULT_OWNER_SUBJECTS` (по образцу `GITLAB_OWNER_LOGINS`); незаданный субъект безопасно
пропускается. Ассертим в тесте: entity с ожидаемым именем существует и несёт политику роли
(`GET /v1/identity/entity/name/<role>-<subject>` → `data.policies` содержит `<role>`). Идемпотентность:
повторный `PUT` → `200/204` (upsert), `DELETE` отсутствующего → `404`=успех.

### D5. Идемпотентность под реальные коды Vault

Запись (policy/role/kv/entity) → `204` или `200` (оба ⊂ 2xx, `call` уже трактует как успех). Чтение
отсутствующего → `404` (`getFound`→false / `okExtra=404`). Пересмотреть `okExtra`: убрать угаданные
под мок `409` там, где Vault их не возвращает (Vault — upsert-семантика, повторная запись `204`, не
`409`). Идемпотентность строим на `getFound`/кодах, не на тексте ошибки (как ADR-0019).

### D6. Профиль и CI: отдельный override, локальный ручной прогон (зеркалим gitlab)

Несмотря на лёгкость Vault (старт секунды, в отличие от GitLab 3-5 мин), по умолчанию **зеркалим
gitlab-паттерн**: отдельный override `docker-compose.vault.yml`, локальный ручной прогон, CI НЕ трогаем.

Обоснование: (1) единообразие операционной модели интеграционных профилей (один и тот же
up/seed/test/down контракт для всех «реальных» плеч); (2) интеграционные тесты против внешнего стенда
— БЛОК 7, отдельный тег, вне дефолтного `go test`; (3) добавление в CI — отдельное решение с
бюджетом/флейки-рисками, не блокирует эту задачу. Лёгкость Vault фиксируем как факт (короткий
health-budget ~60s vs 600s у GitLab), но это влияет лишь на таймауты, не на расположение прогона.

### D7. Где тесты: отдельный vault-набор против реального Vault + мок-GitLab

Создаём **отдельный** `tests/e2e/vault_integration_test.go` с `requireVault` (по env `VAULT_ADDR`/
`VAULT_TOKEN`), а стенд `docker-compose.vault.yml` поднимает реальный Vault, оставляя GitLab/Harbor
моками. Альтернатива (расширять gitlab-набор/стек для сквозной инъекции Vault→GitLab-variables на двух
реальных бэкендах) отклонена для MVP: связывает два тяжёлых профиля, увеличивает время и площадь
флейков; изоляция плеча проще диагностируется и зеркалит ADR-0019. Vault-набор активируется только при
своём gate-env и не влияет на `make e2e`/`make gitlab`.

### Sequence: воркфлоу ↔ activities ↔ Vault (dev)

```
Создание (CreateServiceWorkflow, порядок GitLab→Harbor→Vault→Inject→ACTIVE):
  workflow ──VaultSetupAppRole──▶ vaultHTTP
                                   PUT  /v1/sys/policies/acl/<role>          (204)
                                   POST /v1/auth/approle/role/<role>          (204)
                                   GET  /v1/auth/approle/role/<role>/role-id  (200 data.role_id)
                                   POST /v1/auth/approle/role/<role>/secret-id(200 data.secret_id)
            (X-Vault-Token на каждом запросе)
  RetryPolicy: Temporal-дефолт; non-retryable «ProvisioningFatal» → компенсация
  Компенсация (ADR-0005): VaultTeardownAppRole ─▶ DELETE role + policy (404=успех)

Смена владельцев (ADR-0011):
  VaultSyncOwners  ─▶ PUT/DELETE /v1/identity/entity/name/<role>-<subject>
  Компенсация: VaultRestoreOwners ─▶ восстановление прежнего набора (идемпотентно)

Decommission (ADR-0012, точка невозврата):
  VaultRevokeSecretID ─▶ LIST secret-id → по accessors POST secret-id-accessor/destroy (необратимо)

Transfer (ADR-0013, частично необратимо):
  VaultMigratePaths ─▶ GET secret/data/<src> → PUT secret/data/<dst> → новая policy → DELETE src
```

Идемпотентность воркфлоу — детерминированный WorkflowID (как в существующих воркфлоу; см.
`e2e-change-owners-single-shot`: одна успешная смена владельцев на сервис). Переходы статуса каталога —
guarded-CAS с ожидаемым исходным статусом (ADR-0004/0008), без изменений в этой задаче.

## Risks / Trade-offs

- **[Эмпирические расхождения Vault API от памяти]** (как у GitLab: transfer PUT не POST). →
  Проверять методы/коды/тела против ПОДНЯТОГО Vault, а не по памяти; уроки фиксировать в коде
  комментариями и в ADR.
- **[`LIST` secret-id-accessor — нестандартный HTTP-метод]** Vault использует `LIST` (или
  `GET ?list=true`). → Реализовать через `GET` с `?list=true` (совместимо с `net/http`); idемпотентно
  трактовать `404` (нет secret-id) как пустой набор.
- **[Регрессия мок-пути]** Изменения `vaultHTTP` могут сломать дефолтный `make e2e`. → Ветвление по
  `real`; мок-путь и in-memory не трогаем; покрыто дефолтным прогоном.
- **[Утечка токена]** `X-Vault-Token` не логировать; отдельный `doer` (не протекает на GitLab/Harbor);
  токен через `VAULT_TOKEN_FILE` при необходимости.
- **[dev-режим не для прода]** Явный Non-Goal; SSRF-guard на стенде выключен (`SSRF_DISABLED=true`), в
  проде включён; auth-модель прода (AppRole/auto-unseal) — вне scope, отмечено в ADR.

## Migration Plan

Инкрементально, каждый шаг зелёный: (1) compose-override + сид + Makefile (стенд поднимается, сид
идемпотентен); (2) `Config.VaultToken` + `vaultDoer` + `main.go` `vaultToken()` (мок-путь не затронут);
(3) `vaultHTTP` реальная семантика за флагом `real` (RevokeSecretID, okExtra, identity); (4)
интеграционный набор + `vault-*` цели. Откат: удалить override/сид/набор и ветку `real`-семантики —
дефолтный мок-путь и контракт периметра не затронуты, `gen:check` зелёный на каждом шаге.

## Open Questions

- Точная форма LIST secret-id у пиннутой версии Vault (`?list=true` vs метод `LIST`) — закрыть
  эмпирически при реализации против поднятого Vault.
- Нужен ли предзасев секрета в `secret/data/<demo>` для transfer-теста, или воркфлоу создаёт его сам —
  уточнить при написании `MigratePaths`-ассерта.
