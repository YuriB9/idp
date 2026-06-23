## Why

Сейчас все три управляемые системы DevInfra (GitLab/Vault/Harbor) — это WireMock-моки
(`deploy/mocks/mappings/*`): воркфлоу create/change-owners/decommission/transfer ходят
к ним по HTTP, но мок принимает любые тела и коды, поэтому клиент GitLab
(`services/devinfra-worker/internal/integrations/http.go`) написан под мок и НЕ совместим
с реальным GitLab API: нет аутентификации, `CreateRepo` шлёт `namespace` строкой (а не
`namespace_id`), `SyncMembers` шлёт `username` (а не `user_id`), идемпотентные коды
(409/404) угаданы под мок. Чтобы доказать, что провизия реально создаёт/переносит/
архивирует репозитории, нужно заменить ИМЕННО GitLab-плечо на настоящий GitLab CE
(Vault и Harbor остаются моками — граница MVP) и прогнать воркфлоу против него.

Изменение соответствует docs/IDP_MVP_plan.md (Этап 1, управляемые ресурсы DevInfra) и
ADR-0001 (Temporal/activities), 0004 (guarded-CAS статусов), 0005 (Saga-откат создания —
компенсация GitLab `DeleteRepo`), 0008 (split определения/исполнения воркфлоу), 0011
(owners + role-sync), 0012 (decommission → GitLab archive), 0013 (transfer → GitLab
transfer, точка невозврата), 0018 (E2E auth/детерминизм, стенд-override и харнесс).

## What Changes

- **GitLab-клиент под реальный API** (`internal/integrations/http.go` + `Config`): исходящая
  аутентификация (`PRIVATE-TOKEN`), резолвинг `namespace`→`group_id` и владелец→`user_id`
  с кэшем, корректные тела (`namespace_id`, `user_id`), перепроверка URL-кодирования пути
  `project%2Fname` на archive/unarchive/transfer/variables, пересмотр идемпотентных кодов
  под реальный GitLab. In-memory (`memory.go`) и HTTP-стаб против WireMock-моков остаются
  для дефолтного прогона.
- **Выбор клиента в рантайме** (`devinfra-worker/main.go` + новый env `GITLAB_TOKEN`,
  опц. `GITLAB_NAMESPACE_*`): реальный GitLab включается через конфиг/профиль, по умолчанию
  поведение не меняется (моки + `SSRF_DISABLED=true`).
- **Реальный GitLab CE в отдельном compose-override** (`deploy/compose/docker-compose.gitlab.yml`)
  по образцу `docker-compose.e2e.yml` (ADR-0018): тяжёлый образ (~2.5GB, старт 3-5 мин) НЕ
  в дефолтной локалке; health-gate, детерминированный сид (root-PAT, предсозданные группы
  `demo`/`demo2`, тест-пользователи под владельцев), CI-runner не нужен.
- **Интеграционные тесты воркфлоу против реального GitLab** (build-тег `integration`),
  переиспользующие харнесс `tests/e2e`: ассерт через GitLab API (репозиторий существует в
  нужной группе, CI-переменные заданы, decommission→archived, transfer→сменил namespace).
- **Makefile**: цель(и) подъёма стенда с GitLab + прогона (по образцу `e2e-up/e2e-test/
  e2e-down`). Прогон ТОЛЬКО локальный, ручной — CI не трогаем.
- Откат/компенсации (ADR-0005/0013) наблюдаются против реального GitLab: `DeleteRepo` —
  откат создания; `TransferRepo` — частично необратим (точка невозврата).

НЕ меняются: контракты периметра (`openapi/openapi.yaml`, `web/src/api/*`, proto,
сгенерированный код) — **gen:check остаётся зелёным**; Vault и Harbor (моки).

## Capabilities

### New Capabilities
- `devinfra-gitlab-integration`: реальный GitLab CE в отдельном compose-профиле,
  детерминированный сид (токен/группы/пользователи), health-gate и таймаут-бюджеты,
  интеграционные тесты воркфлоу-уровня против реального GitLab API (тег `integration`),
  Makefile-цели локального ручного прогона.

### Modified Capabilities
- `integration-clients`: GitLab-клиент дорабатывается под семантику РЕАЛЬНОГО GitLab API —
  исходящая аутентификация на всех запросах к GitLab, резолвинг `namespace`→`group_id` и
  владелец→`user_id`, корректные тела/коды идемпотентности; in-memory и стаб против моков
  сохраняются; выбор реализации по конфигу.

## Impact

- Код: `services/devinfra-worker/internal/integrations/{http.go}`, `services/devinfra-worker/main.go`
  (новые env, выбор клиента). `memory.go` без изменений семантики.
- Стенд: `deploy/compose/docker-compose.gitlab.yml` (override), сид GitLab; локалка
  (`docker-compose.yml`), e2e-override и conformance-таргет НЕ затрагиваются.
- Тесты: новый набор интеграционных тестов (тег `integration`), переиспользует харнесс
  `tests/e2e`; дефолтный прогон (in-memory) не меняется.
- Makefile: новые цели `gitlab-*`.
- Границы/сервисы: только DevInfra worker (activities GitLab) и оркестрация стенда; gRPC/
  Temporal-контракты и периметр не затрагиваются.
- Безопасность: SSRF-guard сохраняется (в проде включён); `GITLAB_TOKEN` не хардкодится
  сверх тест-фикстур; сырые ошибки GitLab не утекают клиенту/в ассерты как контракт.
