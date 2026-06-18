## Context

Каталог сервисов уже персистентен: `repository` (guarded-CAS, транзакции, keyset), `usecase` (`Get`/`List`/`CreateRecord` — вставка `status=CREATING` БЕЗ запуска workflow), `grpcapi` (`GetService`/`ListServices`). DevInfra worker — скелет: процесс, task-queue `devinfra`, `/readyz` через `atomic.Bool`, ленивый Temporal-клиент; регистрация workflow/activities отсутствует. Локалка (docker-compose) поднимает `postgres-projects`, Temporal+UI и WireMock-моки `mock-gitlab`/`mock-vault`/`mock-harbor` (адреса в `GITLAB_BASE_URL`/`VAULT_BASE_URL`/`HARBOR_BASE_URL`). Реальных GitLab/Vault/Harbor в MVP нет.

Это первый workflow-change. Он закрывает Этап 2 «Интеграционный слой» (клиенты за интерфейсами против моков) и Этап 3 «Создание сервиса» (durable workflow + Saga) из docs/IDP_MVP_plan.md, опираясь на ADR-0001 (Temporal), ADR-0004 (guarded-CAS статусов), ADR-0005 (полный Saga-откат при недоступности Vault).

Архитектурное ограничение разделения процессов: workflow **определяется** в `services/projects` (там же запускается из gRPC), но **исполняется** DevInfra worker'ом. Чтобы оба процесса использовали один тип workflow без циклической зависимости модулей, общие определения workflow (имя, сигнатуры input/output, `WorkflowID`-конструктор, имена activities) выносятся в отдельный пакет, импортируемый и `services/projects` (для `ExecuteWorkflow`), и `services/devinfra-worker` (для регистрации). См. Decisions.

## Goals / Non-Goals

**Goals:**
- Durable Temporal-workflow «Создание сервиса»: GitLab → Harbor → Vault → инъекция секретов, с `RetryPolicy`/таймаутами/heartbeat.
- Идемпотентность запуска (детерминированный `WorkflowID`) и идемпотентность activities/компенсаций.
- Saga-компенсации в обратном порядке; полный откат при окончательной недоступности Vault (ADR-0005); fallback `FAILED`+alert при сбое самой компенсации.
- Связка с каталогом: `CreateService` (gRPC) → `CreateRecord` (status=CREATING) → запуск workflow; финал — guarded-CAS `CREATING→ACTIVE` или `CREATING→FAILED`.
- Клиенты интеграций за узкими интерфейсами в `services/devinfra-worker/internal`, реализация против моков, SSRF-guard на всех исходящих, in-memory стаб для тестов.
- Реальный `/readyz` worker'а; temporal testsuite (happy-path + компенсации/ретраи).

**Non-Goals:**
- Workflows «Изменение владельцев», «Перенос», «Удаление/decommission».
- Реальный RBAC IDM `CheckAccess` — здесь только граница/заглушка.
- Frontend-форма и экран прогресса.
- Реальные (не мок) GitLab/Vault/Harbor.

## Decisions

### 1. Где живёт код workflow и кто его исполняет
Определение workflow и общие константы (имя workflow, тип input/output, имена activities, конструктор `WorkflowID`) выносятся в общий пакет под `services/projects` (например `services/projects/internal/provisioning` или экспортируемый пакет, доступный worker'у через go.work). `services/projects/grpcapi` вызывает `temporalClient.ExecuteWorkflow` на task-queue `devinfra`; `services/devinfra-worker` регистрирует этот workflow и конкретные реализации activities.
- **Почему так, а не «весь код в worker'е»:** API-процесс должен знать только сигнатуру и `WorkflowID`/имя для запуска; реализация activities (с клиентами GitLab/Vault/Harbor) остаётся в worker'е. Это уважает раздельность процессов (ADR-0001) и не тянет интеграционные зависимости в API.
- **Альтернатива (отклонена):** дублировать строковые имена workflow/activities в обоих модулях — хрупко, рассинхрон не ловится компилятором.

### 2. Идемпотентность запуска — детерминированный WorkflowID
`WorkflowID = "create-service:" + project + ":" + name` (стабильно для пары, уникальной по БД). Политика `WorkflowIDReusePolicy = REJECT_DUPLICATE`/`ALLOW_DUPLICATE_FAILED_ONLY` так, чтобы повторный запуск для того же сервиса не порождал второй конкурентный workflow, но позволял пересоздать после `FAILED`. Запись каталога (`CreateRecord`, status=CREATING) фиксируется ДО `ExecuteWorkflow`; уникальность `(project, name)` в БД — первый барьер от дублей, `WorkflowID` — второй.

### 3. RetryPolicy / таймауты / heartbeat
Для всех activities: `StartToCloseTimeout` (порядка десятков секунд для HTTP к мокам), `RetryPolicy{InitialInterval, BackoffCoefficient=2.0, MaximumInterval, MaximumAttempts}`. Транзиентные ошибки (сеть/5xx) — retryable; окончательные/валидационные — `temporal.NewNonRetryableApplicationError` → ветка компенсации. Для долгих операций — heartbeat, чтобы worker-краш не «вешал» activity до StartToClose.

### 4. Saga-компенсации
Workflow ведёт список выполненных шагов с их компенсациями; при non-retryable ошибке выполняет компенсации в обратном порядке. Компенсации идемпотентны (удаление отсутствующего ресурса = no-op-успех). При окончательной недоступности Vault (после GitLab+Harbor) — удалить Harbor-директорию, затем GitLab-репо (ADR-0005). Если компенсация сама исчерпала ретраи — workflow завершается так, что каталог переводится в `FAILED` + alert оператору (структурный лог уровня error + метрика/событие; механизм alert — лог-маркер в MVP).

### 5. Финальный переход статуса — кто и где
Перевод `CREATING→ACTIVE`/`CREATING→FAILED` через `repository.TransitionStatus` (guarded-CAS, expected=CREATING, ADR-0004). Выполняется финальной activity workflow (worker имеет доступ к Postgres-пулу), а не из API-процесса, чтобы durable-исполнение само довело статус до конца независимо от живости API. `RowsAffected==0` → `errs.ErrConflict` (не перетирать молча).

### 6. Клиенты интеграций
Узкие интерфейсы в `services/devinfra-worker/internal` (по одному на систему), каждый — операция провизии + парная компенсация. HTTP-реализация поверх `pkg/httpclient` с обязательным `pkg/ssrf` (`ValidateURL` на конфиге base-URL + `GuardedDialContext` в транспорте). In-memory стаб реализует те же интерфейсы для дефолтного прогона тестов. Секреты (RoleID/SecretID, Robot-токен) не логируются в открытом виде.

### 7. Контракт gRPC
Новый RPC `CreateService(CreateServiceRequest{project,name}) → CreateServiceResponse{id, status}`. Изменение `.proto` — **BREAKING**, регенерация `make proto` (buf через `./tools`), стабы в `pkg/api`.

### Sequence-диаграмма (happy-path)

```
Client        projects(gRPC)      Postgres        Temporal        devinfra-worker        GitLab  Harbor  Vault
  |  CreateService  |                 |               |                  |                  |       |      |
  |---------------->|                 |               |                  |                  |       |      |
  |                 | CreateRecord(CREATING)          |                  |                  |       |      |
  |                 |---------------->| INSERT status=CREATING            |                  |       |      |
  |                 |<----------------|               |                  |                  |       |      |
  |                 | ExecuteWorkflow (WID=create-service:p:n, TQ=devinfra)                 |       |      |
  |                 |-------------------------------->|                  |                  |       |      |
  |  {id,CREATING}  |                                 | dispatch         |                  |       |      |
  |<----------------|                                 |----------------->| act: GitLab create|----->|      |
  |                 |                                 |                  |<------------------|       |      |
  |                 |                                 |                  | act: Harbor create|------>|      |
  |                 |                                 |                  | act: Vault AppRole|------------->|
  |                 |                                 |                  | act: inject secrets→GitLab CI/CD |
  |                 |                                 |                  | act: Transition CREATING→ACTIVE  |
  |                 |                                 |                  |-----> Postgres (guarded-CAS)     |
```

### Sequence-диаграмма (компенсация — Vault недоступен)

```
worker: GitLab create  -> ok
worker: Harbor create  -> ok
worker: Vault AppRole   -> retry... retry... NonRetryableApplicationError
worker (компенсации, обратный порядок):
    compensate Harbor (delete dir)   -> ok (идемпотентно)
    compensate GitLab (delete repo)  -> ok (идемпотентно)
worker: Transition CREATING→FAILED (guarded-CAS)
    [если компенсация упала окончательно] -> Transition CREATING→FAILED + alert(оператор)
```

## Risks / Trade-offs

- **Рассинхрон имён workflow/activities между API и worker** → общий пакет с константами/типами, импортируемый обоими модулями (Decision 1); строки не дублируются.
- **Частичный откат: компенсация сама падает** → идемпотентные компенсации + fallback `FAILED`+alert (ADR-0005); не молчать (структурный лог error + маркер alert).
- **Дубль-запуск создания** → детерминированный `WorkflowID` + ReusePolicy + уникальность `(project, name)` в БД (Decision 2).
- **SSRF при исходящих к интеграциям** → `pkg/ssrf` обязателен в каждом клиенте (Decision 6); тест на блокировку запрещённого адреса.
- **Недетерминизм workflow-кода** (время/случайность/прямой I/O в workflow) → весь I/O только в activities; `WorkflowID`/таймеры через Temporal API; покрытие temporal testsuite.
- **Утечка секретов в логи** → секреты не пишутся в открытом виде; проверяется в тестах/ревью.
- **Финальный guarded-CAS проигран** (статус уже не CREATING, напр. ручное вмешательство) → `errs.ErrConflict`, фиксируется в логе, не перетирается молча.

## Migration Plan

1. Ветка `change/create-service-workflow` от `master` (прямые коммиты в master запрещены).
2. `.proto`: добавить `CreateService` (+ сообщения), `make proto`, закоммитить регенерированные стабы (codegen-check в CI).
3. Реализовать общий пакет workflow, activities и клиенты интеграций (worker), запуск из `grpcapi`/`usecase`, финальную transition-activity.
4. WireMock-mappings для GitLab/Vault/Harbor (создание+удаление ресурсов) в `deploy/mocks/mappings`.
5. Тесты: temporal testsuite (happy + компенсации/ретраи), in-memory клиенты в дефолтном прогоне, goleak; integration — под тегом.
6. PR с зелёным CI (матрица модулей + govulncheck + codegen-check + integration-джоб) → merge → `/opsx:archive` после merge.

**Rollback:** workflow исполняется worker'ом; откат изменения — revert ветки. BREAKING `.proto` — потребители внутренние (gateway каркас), wire-несовместимость помечена; откат = revert регенерации.

## Open Questions

- Механизм alert оператору в MVP: фиксируем как структурный лог уровня error + метка/метрика (полноценный alerting — вне scope). Подтвердить, что лог-маркер достаточен для MVP.
- Точный формат `WorkflowIDReusePolicy` для пересоздания после `FAILED` (REJECT_DUPLICATE vs ALLOW_DUPLICATE_FAILED_ONLY) — уточнить при реализации под поведение пересоздания.
