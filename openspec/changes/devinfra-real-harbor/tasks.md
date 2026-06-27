## 1. Подготовка и ЭМПИРИЧЕСКАЯ проверка реального Harbor API

- [x] 1.1 Сверить план с `docs/IDP_MVP_plan.md` и ADR-0001/0005/0008/0012/0013/0018/0019/0020; создать git-ветку `change/devinfra-real-harbor` от актуального `master` (прямые коммиты в master запрещены)
- [x] 1.2 Зафиксировать способ запуска Harbor (официальный installer-bundle, пиннутая версия `goharbor/*`, закоммиченный конфиг — D1) и значение фикстуры admin-пароля (`HARBOR_ADMIN_PASSWORD`, дефолт `Harbor12345`)
- [x] 1.3 Поднять Harbor в Docker и ПРОБИТЬ КАЖДЫЙ метод ЭМПИРИЧЕСКИ (не по памяти), зафиксировать точные URL/тела/коды: health-gate (`/api/v2.0/health` vs `/ping`); `POST /api/v2.0/projects` (создание/повтор→`409`); `GET`/`DELETE` проекта по имени vs id (`X-Is-Resource-Name` vs `?name=`); `POST /api/v2.0/robots` v2.0 (минимальный `level`/`permissions[]`/`duration`, формат возвращённого `name`, `secret`); `GET /api/v2.0/robots?q=name=` (резолвинг id); `DELETE /api/v2.0/robots/<id>` (числовой id); набор допустимых полей `ProjectMetadata` для маркера transfer; коды идемпотентности
- [x] 1.4 Зафиксировать выявленные расхождения с текущим мок-ориентированным `harborHTTP` (комментарии в коде + раздел в ADR-0021)

## 2. Стенд: реальный Harbor в отдельном compose-профиле

- [x] 2.1 Добавить `deploy/compose/docker-compose.harbor.yml` (по образцу `docker-compose.vault.yml`/`docker-compose.gitlab.yml`): связка сервисов Harbor пиннутой версии с закоммиченным конфигом, health-gate, проброс порта; переключение `devinfra-worker` на реальный Harbor (`HARBOR_BASE_URL`, `HARBOR_USERNAME`, `HARBOR_PASSWORD`, `SSRF_DISABLED=true`). Комментарии — только на русском
- [x] 2.2 Проверить, что профиль НЕ ломает базовую локалку, e2e-override, gitlab-профиль, vault-профиль и conformance (они не ссылаются на сервисы harbor и не задают воркеру `HARBOR_USERNAME`/`HARBOR_PASSWORD`, поэтому продолжают ходить к mock-harbor)
- [x] 2.3 Добавить `deploy/compose/harbor-seed/seed.sh` (по образцу `vault-seed/seed.sh`): дождаться готовности, аутентифицироваться admin-фикстурой по REST, при необходимости предсоздать базовые сущности (идемпотентно). Комментарии — только на русском

## 3. Конфигурация и выбор клиента (services/devinfra-worker)

- [x] 3.1 В `internal/integrations/http.go` добавить в `Config` поля `HarborUsername`/`HarborPassword`; константу заголовка `Authorization` с `//nolint:gosec` (G101 — имя заголовка/схемы, не секрет, как `vaultTokenHeader`)
- [x] 3.2 В `NewHTTPClients` собрать отдельный `harborDoer` с заголовком `Authorization: Basic base64(user:pass)` ТОЛЬКО при непустых креденшелах (иначе общий `sharedDoer`); заголовок не должен протекать на GitLab/Vault; добавить флаг `real` у `harborHTTP`
- [x] 3.3 В `main.go` добавить `harborCreds()` (читает `HARBOR_USERNAME` и `HARBOR_PASSWORD` либо `HARBOR_PASSWORD_FILE`, по образцу `gitLabToken()`/`vaultToken()` через `tokenFromEnv`); пробросить креденшелы в `integrations.Config`; не логировать пароль

## 4. Реальная семантика Harbor-клиента (harborHTTP за флагом real)

- [x] 4.1 `CreateProject`: `POST /api/v2.0/projects` (`project_name`; `409`→успех); `POST /api/v2.0/robots` с телом v2.0 (`level`/`permissions[]`/`duration`); вернуть ФАКТИЧЕСКИЕ `name`/`secret` из ответа (убрать мок-заглушки `mock-harbor-secret`/сконструированное имя в реальном режиме)
- [x] 4.2 `DeleteProject` (компенсация создания, ADR-0005): резолв robot id по имени и `DELETE /api/v2.0/robots/<id>`; затем удаление проекта (по имени→id при необходимости; пустой проект); `404`→успех
- [x] 4.3 `SetReadOnly` (decommission, ADR-0012, точка невозврата): реализовать как ОТЗЫВ robot (резолв id → `DELETE /api/v2.0/robots/<id>`), проект сохраняется; убрать несуществующее поле `read_only`; идемпотентность (`404`→успех); результат наблюдаем (robot проекта отсутствует)
- [x] 4.4 `SetWritable` (компенсация decommission): воссоздать robot проекта (новый secret); идемпотентность
- [x] 4.5 `UpdateMetadata` (transfer, ADR-0013): обновить ДОПУСТИМОЕ наблюдаемое поле `ProjectMetadata` под целевой проект (убрать несуществующий `owner_project`); идемпотентность; значение наблюдаемо через `GET` проекта
- [x] 4.6 Пересмотреть `okExtra`/`getFound` под реальные коды Harbor (повторное создание→`409`, удаление отсутствующего→`404`; опираться на коды/`getFound`, не на текст); сохранить мок-путь при `real=false`
- [x] 4.7 Убедиться, что `internal/integrations/memory.go` (HasHarbor/IsHarborReadOnly/HarborProject) НЕ изменён и остаётся для дефолтного прогона

## 5. Интеграционные тесты (tests/e2e)

- [x] 5.1 Добавить `tests/e2e/harbor_integration_test.go` (build-тег `integration`) с `requireHarbor`-гейтом по env (`HARBOR_ADDR`/`HARBOR_USERNAME`/`HARBOR_PASSWORD`), хелперами доступа к Harbor API (HTTP Basic) и skip без gate-env; переиспользовать харнесс (`fetchIDToken`/`callAPI`/`waitForStatus`/`uniqueName`); goleak — только где есть горутины
- [x] 5.2 Тест создания: воркфлоу → `active`, ассерт через Harbor API (проект существует, robot создан, секрет выдан)
- [x] 5.3 Тест decommission: → `decommissioned`, ассерт отзыва (robot-аккаунт проекта отсутствует/отозван, проект сохраняется)
- [x] 5.4 Тест transfer: → `active`, ассерт обновления наблюдаемого поля метаданных проекта под целевой проект
- [x] 5.5 Не ассертить сырые тексты ошибок Harbor как контракт; учесть детерминизм WorkflowID

## 6. Makefile

- [x] 6.1 Добавить `COMPOSE_HARBOR` и переменные (`HARBOR_ADDR`, `HARBOR_USERNAME`/`HARBOR_PASSWORD`-фикстуры, бюджет ожидания соразмерен GitLab) по образцу gitlab/vault-секций
- [x] 6.2 Добавить цели `harbor-up`/`harbor-seed`/`harbor-test`/`harbor-down`/`harbor`/`harbor-logs` по образцу `gitlab-*`/`vault-*`; прогон только локальный, CI не трогать

## 7. ADR и документация

- [x] 7.1 Завести `docs/adr/0021-harbor-auth-and-project-robot-layout.md` (Status: Accepted) с решениями D1–D7 и эмпирически подтверждёнными расхождениями Harbor API

## 8. Верификация

- [x] 8.1 Прогнать дефолтный путь зелёным: `go test` всех модулей `-race -shuffle`, golangci-lint (учесть gosec G101!), govulncheck, `gen:check` (контракт+кодоген БЕЗ изменений), openapi-lint, web-test
- [ ] 8.2 Локально прогнать `make harbor` (up → seed → integration-набор → down) против реального Harbor; подтвердить ассерты создания/decommission/transfer
- [x] 8.3 Подтвердить, что `make e2e`, `make gitlab` и `make vault` не затронуты (мок-путь и gitlab/vault-профили работают)
- [ ] 8.4 Открыть PR с ветки `change/devinfra-real-harbor`; добиться зелёного CI; после merge — отдельный PR `/opsx:archive` (sync+archive по образцу #59/#67)
