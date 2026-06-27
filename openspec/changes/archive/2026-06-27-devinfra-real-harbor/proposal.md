## Why

После `devinfra-real-gitlab` (ADR-0019, PR #58, sync/archive #59) и `devinfra-real-vault`
(ADR-0020, PR #66, sync/archive #67) реальными сделаны GitLab- и Vault-плечи DevInfra; Harbor
остался ПОСЛЕДНИМ WireMock-моком. HTTP-клиент Harbor (`harborHTTP` в
`services/devinfra-worker/internal/integrations/http.go`) писался под мок и расходится с реальным
Harbor v2.0 API: запросы НЕ несут аутентификацию (общий `sharedDoer` без заголовков — реальный
Harbor требует HTTP Basic admin); `DeleteProject`/`SetReadOnly` обращаются к robot-аккаунту ПО ИМЕНИ
(`robot$<proj>`), тогда как реальный Harbor ждёт ЧИСЛОВОЙ `id` (`DELETE /api/v2.0/robots/<id>`);
тело `POST /api/v2.0/robots` минимально (`{name, level}`) — реальному v2.0 нужны `permissions[]` +
`duration`; `SetReadOnly` шлёт несуществующее поле `read_only` в метаданных проекта (в Harbor нет
project-level `read_only`); идемпотентные коды (`409`/`404`) угаданы под мок.

Чтобы интеграционно проверять воркфлоу провизии/decommission/transfer против НАСТОЯЩЕГО Harbor (как
уже сделано для GitLab и Vault) и зафиксировать auth-модель и раскладку проектов/robot-аккаунтов,
нужно подключить реальный Harbor — последним плечом после GitLab и Vault.

Соответствие плану: `docs/IDP_MVP_plan.md` (интеграции DevInfra-worker с GitLab/Vault/Harbor);
прямое продолжение ADR-0019/0020. Опирается на ADR-0001 (Temporal/activities), ADR-0005 (Saga-откат
создания — компенсация `DeleteProject`), ADR-0008 (split воркфлоу), ADR-0012 (decommission → Harbor
`SetReadOnly` + отзыв robot, необратимо), ADR-0013 (transfer → Harbor `UpdateMetadata`),
ADR-0018 (E2E-харнесс и стенд-override).

## What Changes

- Поднять РЕАЛЬНЫЙ Harbor в отдельном compose-override (`deploy/compose/docker-compose.harbor.yml`)
  по образцу `docker-compose.gitlab.yml`/`docker-compose.vault.yml`, с health-gate
  (`GET /api/v2.0/health` или `/api/v2.0/ping`). НЕ ломать локалку/e2e/gitlab-профиль/vault-профиль/
  conformance. Harbor — НЕ один образ, а связка контейнеров (core+db+redis+registry+jobservice+
  portal+nginx); способ запуска (официальный installer-bundle с пиннутой версией и закоммиченным
  конфигом vs урезанный API-only субсет) обоснован в design.
- Доработать `harborHTTP` до семантики РЕАЛЬНОГО Harbor v2.0 API: заголовок `Authorization: Basic`
  ТОЛЬКО на Harbor-запросах (отдельный `doer.headers`, как PRIVATE-TOKEN у GitLab и X-Vault-Token у
  Vault); резолвинг robot `id` (и при необходимости project `id`) по имени через список/поиск (как
  group_id/user_id у GitLab); тело `POST /api/v2.0/robots` с `level`/`permissions[]`/`duration` и
  забор ФАКТИЧЕСКИХ `name`/`secret` из ответа (инъекция в GitLab-переменные берёт возвращённые
  значения, не сконструированные); корректная read-only-семантика (в Harbor нет project-level
  `read_only` — отзыв robot как наблюдаемый «недоступен на запись»); идемпотентность по кодам/
  `getFound`, не по тексту. Флаг `real` включает реальную семантику по наличию креденшелов;
  in-memory и мок-путь (WireMock) сохраняются для дефолтного `make e2e` (БЛОК 7).
- `main.go` + `integrations.Config`: добавить `HARBOR_USERNAME`/`HARBOR_PASSWORD`/
  `HARBOR_PASSWORD_FILE` (по образцу `gitLabToken()`/`vaultToken()` + общий `tokenFromEnv`); выбор
  реального Harbor-клиента vs мок по наличию креденшелов; пароль не логировать.
- Детерминированный сид Harbor (`deploy/compose/harbor-seed/`): admin-пароль — фикстура стенда
  (`HARBOR_ADMIN_PASSWORD`, дефолт `Harbor12345`), при необходимости базовые сущности. По образцу
  `gitlab-seed`/`vault-seed`.
- Интеграционный набор `tests/e2e/harbor_integration_test.go` (build-тег `integration`,
  `requireHarbor`-гейт), переиспользующий харнесс (`fetchIDToken`/`callAPI`/`waitForStatus`/
  `uniqueName`), ассертящий фактическое состояние через Harbor API (проект существует, robot создан,
  секрет выдан; при decommission — robot отозван/проект недоступен на запись; при transfer —
  метаданные обновлены).
- Makefile: цели `harbor-up`/`harbor-seed`/`harbor-test`/`harbor-down`/`harbor`/`harbor-logs` по
  образцу `gitlab-*`/`vault-*`. Прогон локальный ручной (CI по умолчанию не трогаем; решение
  обосновано в design).

Контракт периметра (`openapi/openapi.yaml`, `web/src/api/*`, `web/public/openapi.yaml`, proto,
сгенерированный код) НЕ меняется; `gen:check` остаётся зелёным.

## Capabilities

### New Capabilities
- `devinfra-harbor-integration`: подключение реального Harbor к DevInfra worker отдельным
  compose-профилем — реальный Harbor-клиент (HTTP Basic, резолвинг robot/project id, фактические
  name/secret робота, read-only через отзыв robot, коды/идемпотентность), детерминированный сид
  (admin-пароль), health-gate, интеграционный набор воркфлоу против Harbor API, Makefile `harbor-*`
  (локальный ручной прогон).

### Modified Capabilities
- `integration-clients`: уточнение требований к Harbor-клиенту — аутентификация `Authorization:
  Basic` (не протекает на GitLab/Vault), семантика проектов/robot-аккаунтов v2.0 (резолвинг id,
  фактические name/secret, read-only через отзыв robot), идемпотентность по кодам; in-memory/мок-путь
  сохраняются.

## Impact

- Код: `services/devinfra-worker/internal/integrations/http.go` (`harborHTTP`, `Config`,
  `NewHTTPClients`), `services/devinfra-worker/main.go` (`harborCreds()`, выбор клиента),
  `internal/integrations/memory.go` (без изменений — остаётся для дефолтного прогона).
- Оркестрация стенда: `deploy/compose/docker-compose.harbor.yml`, `deploy/compose/harbor-seed/`,
  `Makefile` (`harbor-*`).
- Тесты: `tests/e2e/harbor_integration_test.go` (+ возможные хелперы харнесса).
- Компенсации/откат провизии: `DeleteProject` (откат создания, ADR-0005), `SetReadOnly`↔`SetWritable`
  (decommission/компенсация, ADR-0012), `UpdateMetadata` (transfer, ADR-0013) — наблюдаемы против
  реального Harbor.
- НЕ затрагивается: контракт периметра/proto/кодоген (`gen:check` зелёный), реальные GitLab/Vault
  (по своим профилям без изменений), Kubernetes, продовый Harbor/HA, Trivy/Notary/подпись образов,
  реальные push/pull образов, ротация секретов на проде.
- ADR: новый — «Harbor auth-модель и раскладка проектов/robot-аккаунтов» (HTTP Basic admin,
  резолвинг id, read-only через отзыв robot, способ запуска Harbor в Docker).
