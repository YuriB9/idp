## Why

Доменный каталог сервисов уже умеет хранить запись и переводить её статусы guarded-CAS-переходами, но `CreateServiceRecord` лишь вставляет запись со `status=CREATING` и НЕ запускает провизию ресурсов — сервис при этом физически не создаётся (нет репозитория, секретов, реестра образов). Это первый workflow-change MVP: он реализует ключевую user story Этапа 3 «Создание сервиса» из docs/IDP_MVP_plan.md — durable Temporal-workflow, который через DevInfra worker провиженит GitLab/Vault/Harbor с Saga-компенсациями. Реализуется поверх готового каталога (persistence + guarded-CAS уже в master) и закрывает Этап 2 «Интеграционный слой» (клиенты интеграций за интерфейсами против моков).

Соответствие решениям: ADR-0001 (Temporal как оркестратор), ADR-0005 (полный Saga-откат при недоступности Vault), ADR-0004 (переходы статусов guarded-CAS). Раздел docs/IDP_MVP_plan.md — Этап 2 «Интеграционный слой» и Этап 3 «Создание сервиса».

## What Changes

- **Workflow «Создание сервиса»** в `services/projects` (определение workflow-кода), исполняемый DevInfra worker'ом на task-queue `devinfra` (ADR-0001). Детерминированный `WorkflowID` для идемпотентности повторного запуска; `RetryPolicy`, таймауты и heartbeat на activities.
- **Activities провизии** (за интерфейсами, идемпотентны, SSRF-guard на всех исходящих): GitLab (репозиторий в группе проекта), Harbor (директория образов + Robot Account), Vault (политики + AppRole RoleID/SecretID), инъекция секретов (Vault/Harbor токены) в CI/CD-переменные GitLab. Клиенты интеграций — за интерфейсами в `services/devinfra-worker/internal`, реализация против WireMock-моков.
- **Регистрация workflow/activities** в DevInfra worker'е (сейчас скелет) и **реальный сигнал живости** `/readyz` (worker запущен и поллит task-queue).
- **Saga-компенсации** в обратном порядке, идемпотентные (ADR-0005): при окончательной недоступности Vault — полный откат (удалить GitLab-репо и Harbor-директорию); если сама компенсация упала — статус `FAILED` + alert оператору. non-retryable `ApplicationError` → ветка компенсации.
- **Связка с каталогом**: запуск workflow из usecase/gRPC после `CreateRecord` (status=CREATING); по успеху — guarded-CAS `CREATING → ACTIVE`; по фатальному сбою — компенсации + `CREATING → FAILED`.
- **gRPC**: новый метод `CreateService` в `ProjectsService` — стартует workflow, возвращает идентификатор/статус `CREATING`. Изменение `.proto` — **BREAKING**, регенерация `make proto`.
- Проверка прав (IDM CheckAccess) закладывается как **граница/заглушка**, не реализуется (отдельный change `idm-rbac-min`).

## Capabilities

### New Capabilities
- `service-provisioning`: Temporal-workflow «Создание сервиса» — оркестрация провизии GitLab/Vault/Harbor через DevInfra worker, идемпотентность (детерминированный WorkflowID), RetryPolicy/таймауты/heartbeat на activities, Saga-компенсации в обратном порядке с полным откатом при недоступности Vault (ADR-0005), регистрация workflow/activities и реальный `/readyz` worker'а.
- `integration-clients`: клиенты интеграций (GitLab/Vault/Harbor) за узкими интерфейсами в `services/devinfra-worker/internal` — идемпотентные операции и компенсации, SSRF-guard на всех исходящих, реализация против WireMock-моков, in-memory стаб для дефолтного прогона тестов.

### Modified Capabilities
- `service-catalog`: добавляется запуск Temporal-workflow из usecase/gRPC после вставки записи со `status=CREATING` (ранее запуск был явно вне scope); guarded-CAS-переходы `CREATING → ACTIVE` (успех) и `CREATING → FAILED` (фатальный сбой) как часть жизненного цикла создания.
- `service-contracts`: новый RPC `CreateService` в `ProjectsService` (proto) — **BREAKING** изменение контракта, регенерация стабов через `make proto`.

## Impact

- **Код**: `services/projects` (определение workflow, запуск из usecase/grpcapi, маппинг новых статус-переходов); `services/devinfra-worker` (регистрация workflow/activities, реализация activities и клиентов интеграций, реальный `/readyz`); `proto/projects/v1/projects.proto` (+ регенерация `pkg/api`).
- **API**: BREAKING-изменение gRPC-контракта `ProjectsService` (новый метод `CreateService`).
- **Зависимости**: Temporal SDK (workflow/activity, testsuite); `pkg/ssrf` (обязателен на всех исходящих к GitLab/Vault/Harbor); `pkg/httpclient`; переиспользование `pkg/{errs,logger,config,temporallog,reqid}` и `services/projects/internal/{repository,usecase}`.
- **Системы**: DevInfra worker начинает реально поллить task-queue `devinfra` и обращаться к WireMock-мокам (`GITLAB_BASE_URL`/`VAULT_BASE_URL`/`HARBOR_BASE_URL`). Реальных GitLab/Vault/Harbor в MVP нет.
- **Тесты/CI**: temporal testsuite для workflow (happy-path и ветки компенсаций/ретраев); table-driven + `t.Parallel()`; goleak в пакетах с горутинами; стаб/in-memory клиентов в дефолтном прогоне, реально-внешнее — под тегом `integration`.
