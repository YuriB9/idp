## Why

После «Создания сервиса», минимального RBAC и «Изменения владельцев» четвёртый
сквозной сценарий MVP — «Удаление / вывод из эксплуатации сервиса» (soft delete /
decommission), см. docs/IDP_MVP_plan.md (Этап 3, раздел «Удаление сервиса», строки
~171–178; порядок реализации — пункт 4). Сегодня статус `DECOMMISSIONED` есть в
enum контракта и в `CHECK` каталога, но доменного пути перехода нет: нет RPC
вывода из эксплуатации, нет workflow обратных операций, нет ручки периметра и
действия в портале, нет RBAC-операции `decommission`. Без этого сценария нельзя
безопасно вывести активный сервис из эксплуатации: отозвать доступы во внешних
системах (GitLab/Harbor/Vault), синхронно отразить статус в каталоге и закрыть
доступ. Сценарий переиспользует моки GitLab/Harbor/Vault и инфраструктуру
Temporal/Saga (ADR-0001/0005/0008), guarded-CAS каталога (ADR-0004) и fail-closed
RBAC (ADR-0003/0010), поэтому идёт до самого рискованного «Переноса».

Этот change ОБЯЗАН закрыть открытый вопрос плана (docs/IDP_MVP_plan.md, строка 172
и блок «Открытый вопрос» в openspec/config.yaml): «проверка снятой нагрузки из K8s
до старта workflow при отсутствии K8s-worker в MVP» — выбрать и обосновать путь
(прямой запрос к кластеру vs явный чек-флаг/предусловие), реализовать как
отдельный предварительный шаг и зафиксировать решение (новый ADR-0012).

## What Changes

- **BREAKING (контракт projects):** в `proto/projects/v1` добавляется новый RPC
  вывода из эксплуатации `DecommissionService(project, name, load_drained)` и
  аддитивное поле `decommissioned_at` в `Service`/ответах чтения. Семантика
  идемпотентна: повторный вызов на уже выведенном сервисе — успешный no-op.
  Требует `buf generate` (`*.pb.go`) и регенерации TS-клиента портала;
  `gen:check` зелёный.
- **Каталог:** доменная операция soft-delete через guarded-CAS
  `ACTIVE→DECOMMISSIONED` (`UPDATE ... WHERE id=$id AND status='active'`,
  `RowsAffected==0 → ErrConflict`). Допустимый исходный статус — только `active`;
  `creating`/`failed` → отказ-предусловие; повторный `decommissioned` → no-op.
  Данные каталога СОХРАНЯЮТСЯ (строки/владельцы не удаляются). Добавляется
  nullable-колонка `decommissioned_at` (обратимая миграция goose), отражается в
  `Get`/`List`.
- **Workflow «Вывод из эксплуатации»** (Temporal, отдельный пакет
  `services/projects/decommission`): предварительный шаг — проверка снятой
  нагрузки K8s (через явный чек-флаг/предусловие за интерфейсом `LoadChecker`,
  граница под будущий K8s-worker), затем activities: GitLab archive + отзыв
  доступов, Harbor → read-only + отзыв Robot, Vault → отзыв активных
  SecretID/токенов (немедленное прекращение доступа), затем guarded-CAS
  `ACTIVE→DECOMMISSIONED` в каталоге. Saga с ТОЧКОЙ НЕВОЗВРАТА на первом
  необратимом отзыве (Vault): сбой ДО неё → идемпотентные компенсации; сбой
  ПОСЛЕ → форвард-only ретраи + алерт оператору, не молчаливый откат
  (ADR-0005/0008).
- **Авторизация:** новая RBAC-операция `decommission` на ресурсе
  `project:<project>`; gateway и projects вызывают IDM `CheckAccess` перед
  операцией (defense-in-depth, fail-closed → 403/PermissionDenied).
- **Периметр (REST, ADR-0009):** `POST /projects/{project}/services/{name}/decommission`
  (идемпотентное действие soft-delete; тело несёт явное предусловие
  `load_drained`), статус `decommissioned`/`decommissioned_at` в ответах
  `GET`/`LIST`; маппинг gRPC→HTTP кодов (конфликт→409, предусловие→422).
- **Портал:** действие «Вывести из эксплуатации» с подтверждением (ввод имени,
  явно: decommission, не purge), обработка 403/409/422, индикация статуса
  `decommissioned`, блокировка повторного/недопустимого действия.
- **Локалка:** расширение seed IDM (право `decommission@project:demo` субъекту
  `demo-user`), чтобы сквозной сценарий проходил при включённом RBAC.
- **Документация:** README projects + инструкция — что такое decommission
  (soft-delete), как вывести сервис, что происходит с доступами/ролями, как
  закрыт вопрос проверки снятой нагрузки K8s, как проверить отказ/предусловие/
  разрешение.
- **ADR-0012:** семантика decommission (soft-delete, допустимые исходные статусы,
  `decommissioned_at`, отзыв ролей) и закрытие вопроса проверки снятой нагрузки
  K8s (выбор + обоснование + граница под будущий worker); политика точки
  невозврата при необратимых отзывах доступа.

## Capabilities

### New Capabilities
- `service-decommissioning`: доменная операция вывода сервиса из эксплуатации
  (soft-delete каталога через guarded-CAS `ACTIVE→DECOMMISSIONED`, `decommissioned_at`,
  идемпотентность), gRPC `DecommissionService` и Temporal-workflow «Вывод из
  эксплуатации» (Saga: предусловие снятой нагрузки K8s → GitLab archive/Harbor
  read-only/Vault revoke → каталог; точка невозврата + алерт).

### Modified Capabilities
- `service-contracts`: расширение `proto/projects/v1` (RPC `DecommissionService` +
  поле `decommissioned_at`); регенерация Go/TS.
- `access-control`: новая операция `decommission`; defense-in-depth CheckAccess в
  gateway+projects (fail-closed).
- `perimeter-rest`: REST-ручка вывода из эксплуатации, `decommissioned_at` в
  ответах чтения, маппинг конфликта (409) и предусловия (422).
- `service-provisioning`: новые activities DevInfra worker для обратных операций
  (GitLab archive + revoke, Harbor read-only + robot revoke, Vault revoke
  SecretID) и предварительная проверка снятой нагрузки K8s.
- `portal-ui`: UI действия вывода из эксплуатации с подтверждением и индикацией.
- `local-environment`: расширение seed (право `decommission@project:demo`).

## Impact

- **Контракты/кодоген:** `proto/projects/v1`, `pkg/api/**` (`buf generate`),
  TS-клиент `web/src/api` (`gen:check`).
- **services/projects:** `repository` (guarded-CAS `ACTIVE→DECOMMISSIONED`,
  `decommissioned_at`, миграция), `usecase`, `grpcapi` (`DecommissionService`,
  `authorize` с действием `decommission`), новый пакет `decommission` (workflow +
  starter).
- **services/devinfra-worker:** новые activities GitLab archive / Harbor read-only /
  Vault revoke SecretID + предварительная проверка нагрузки (`LoadChecker`);
  интеграционные моки `integrations`.
- **services/idm:** seed права `decommission@project:demo` (примитивы
  Assign/Revoke/Invalidate уже есть; отзыв ролей владельцев в MVP не выполняется —
  см. design/ADR-0012).
- **services/gateway:** маршрут `POST .../decommission`, `decommissioned_at` в
  ответах, RBAC `decommission`, уточнение `httpFromGRPC` (конфликт→409 через
  `Aborted`, предусловие→422 через `FailedPrecondition`).
- **web:** действие/диалог вывода из эксплуатации, zod-схемы, TanStack-мутация.
- **БД:** миграция `services/projects/migrations` (`decommissioned_at`), seed IDM
  `services/idm/migrations`.
- **Откат/компенсации:** Saga workflow — компенсации идемпотентны ДО точки
  невозврата (первый необратимый отзыв Vault); ПОСЛЕ — форвард-only ретраи +
  алерт оператору, без молчаливого отката (ADR-0005/0008).
- **Зависимости:** при новых общих зависимостях — `GOWORK=off go mod tidy` во
  всех затронутых модулях (tidy-check/govulncheck). NB: `services/gateway/gateway`
  — закоммиченный бинарь, после сборки не коммитить.
