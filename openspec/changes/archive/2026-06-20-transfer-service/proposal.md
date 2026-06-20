## Why

Пятый и ПОСЛЕДНИЙ сквозной сценарий MVP — «Перенос сервиса» (transfer): смена
проекта-владельца сервиса с переносом репозитория GitLab в новую группу,
миграцией путей/политик Vault, обновлением метаданных/прав Harbor, переносом
ролей владельцев в IDM и обновлением каталога. См. docs/IDP_MVP_plan.md (Этап 3,
раздел «Перенос сервиса»; порядок внедрения — пункт 5, делается ПОСЛЕДНИМ как
самый рискованный). Сегодня доменного пути переноса нет: нет RPC, нет workflow,
нет ручки периметра и действия в портале, нет RBAC-действий переноса, нет
доменной операции смены колонки `project` в каталоге и нет activities переноса в
DevInfra worker.

Перенос — самый РИСКОВАННЫЙ сценарий: transfer репозитория GitLab и миграция
путей Vault частично необратимы, поэтому workflow строится с ЯВНОЙ политикой
ТОЧКИ НЕВОЗВРАТА (PONR) и алертом оператору при частичном сбое — НЕ молчаливым
откатом (ADR-0005/0008). Операция затрагивает ДВА проекта (source и target),
поэтому требует двусторонней авторизации (нельзя «вынести» сервис в чужой проект
без права на target). Сценарий переиспользует Temporal/Saga (ADR-0001/0005/0008),
guarded-CAS каталога (ADR-0004), fail-closed RBAC (ADR-0003/0010), синхронизацию
ролей IDM (ADR-0011) и маппинг ошибок 409/422 (ADR-0012).

Этот change ОБЯЗАН закрыть и обосновать (новый ADR-0013) ключевые открытые
вопросы: семантику переноса (сохранение id записи, смена колонки `project`,
допустимые исходные статусы, поведение при занятом имени в target), двустороннюю
авторизацию (какие RBAC-действия и где точки `CheckAccess`), необходимость
транзитного статуса `transferring`, и точное расположение точки невозврата.

## What Changes

- **BREAKING (контракт projects):** в `proto/projects/v1` добавляется новый RPC
  `TransferService(project, name, target_project)` и аддитивно значение enum
  `SERVICE_STATUS_TRANSFERRING = 5`. Семантика идемпотентна: повторный вызов на
  уже перенесённом сервисе — успешный no-op. Требует `buf generate` (`*.pb.go`) и
  регенерации TS-клиента портала; `gen:check` зелёный.
- **Транзитный статус `transferring`:** вводится аддитивно для наблюдаемости и
  защиты от конкурентных операций на время переноса (`active→transferring→active`
  в target). Расширяется `CHECK status IN (...)` каталога (обратимая миграция
  goose).
- **Каталог:** доменная операция переноса — смена колонки `project`
  `source→target` через guarded-CAS с проверкой свободы `(target_project, name)`.
  Многошаговые записи в транзакции. Допустимый исходный статус — только `active`;
  `creating`/`failed`/`decommissioned`/`transferring` → отказ-предусловие или
  конфликт. Данные владельцев переезжают вместе с записью (FK `service_id`
  сохраняется); id записи каталога СОХРАНЯЕТСЯ.
- **Workflow «Перенос»** (Temporal, отдельный пакет `services/projects/transfer`):
  Saga — (0) guarded-CAS `active→transferring` (компенсируемо: `→active`) →
  (1) **GitLab transfer репозитория в новую группу — ТОЧКА НЕВОЗВРАТА** →
  (2) Vault миграция путей (копия секретов `source→target` + новые политики +
  очистка старых) → (3) Harbor обновление метаданных/прав → (4) guarded-CAS
  каталога `transferring→active` со сменой `project` на target → (5) перенос
  ролей владельцев в IDM (`revoke owner:project:<source>` +
  `assign owner:project:<target>` + `InvalidateSubject`). До PONR — идемпотентная
  компенсация; после — форвард-only ретраи + алерт оператору, не молчаливый откат.
- **Авторизация (двусторонняя):** новые RBAC-действия `transfer` (на
  source-проекте) И `transfer_in` (на target-проекте). gateway и projects
  вызывают IDM `CheckAccess` для ОБОИХ проектов перед операцией (defense-in-depth,
  fail-closed → 403/PermissionDenied). Отказ на любом из проектов → отказ операции.
- **Периметр (REST, ADR-0009):**
  `POST /projects/{project}/services/{name}/transfer` с телом `{target_project}`
  (POST-действие на под-ресурсе, идемпотентно). Маппинг gRPC→HTTP: deny→403,
  конкурентный конфликт/занятое имя→409 (через `Aborted`/`AlreadyExists`),
  предусловие→422 (через `FailedPrecondition`) — как в ADR-0012.
- **Портал:** действие «Перенести сервис» (по образцу `DecommissionCard`/
  `OwnersCard`) с подтверждением, ЯВНЫМ предупреждением о необратимости/рисках,
  выбором target-проекта, обработкой 403/409/422 и индикацией результата;
  `StatusBadge` умеет `transferring`.
- **Локалка:** расширение seed IDM (право `transfer@project:demo` субъекту
  `demo-user`; второй демо-проект `project:demo2` с ролью `owner:project:demo2` и
  правом `transfer_in@project:demo2`), чтобы сквозной перенос `demo→demo2`
  проходил при включённом RBAC.
- **Документация:** README projects + инструкция — что такое перенос, что
  происходит с репозиторием/секретами/ролями/доступами, риски и точка невозврата,
  как проверить отказ/предусловие/разрешение.
- **ADR-0013:** семантика переноса (смена `project`, судьба id/owners/статуса,
  транзитный статус `transferring`), форма контракта и идемпотентность,
  двусторонняя авторизация (`transfer` + `transfer_in`, точки `CheckAccess`),
  политика точки невозврата (PONR на transfer GitLab) и алерта.

## Capabilities

### New Capabilities
- `service-transfer`: доменная операция переноса сервиса в другой проект (смена
  колонки `project` через guarded-CAS с проверкой свободы `(target_project,
  name)`, транзитный статус `transferring`, сохранение id/владельцев,
  идемпотентность), gRPC `TransferService` и Temporal-workflow «Перенос» (Saga:
  `transferring` → GitLab transfer [PONR] → Vault миграция путей → Harbor
  метаданные → каталог `project=target` → перенос ролей IDM; алерт после PONR).

### Modified Capabilities
- `service-contracts`: расширение `proto/projects/v1` (RPC `TransferService` +
  аддитивное значение enum `SERVICE_STATUS_TRANSFERRING`); регенерация Go/TS.
- `access-control`: новые действия `transfer` (source) и `transfer_in` (target);
  defense-in-depth `CheckAccess` для двух проектов в gateway+projects (fail-closed).
- `perimeter-rest`: REST-ручка переноса, двусторонняя авторизация на периметре,
  маппинг конфликта/занятого имени (409) и предусловия (422).
- `service-provisioning`: новые activities DevInfra worker (GitLab transfer repo,
  Vault миграция путей, Harbor обновление метаданных) и activity смены `project` в
  каталоге.
- `service-ownership`: программный перенос ролей владельцев между проектами
  (`revoke owner:project:<source>` + `assign owner:project:<target>` +
  `InvalidateSubject` по затронутым субъектам) из доменного потока переноса.
- `portal-ui`: UI действия переноса с подтверждением, предупреждением о рисках и
  индикацией статуса `transferring`.
- `local-environment`: расширение seed (второй проект `project:demo2`, права
  `transfer@project:demo` и `transfer_in@project:demo2`).

## Impact

- **Контракты/кодоген:** `proto/projects/v1`, `pkg/api/**` (`buf generate`),
  TS-клиент `web/src/api` (`gen:check`).
- **services/projects:** `repository` (guarded-CAS смены `project`, проверка
  свободы `(target_project, name)`, транзитный `transferring`, миграция CHECK),
  `usecase`, `grpcapi` (`TransferService`, двойной `authorize` —
  `transfer`/`transfer_in`), новый пакет `transfer` (workflow + starter).
- **services/devinfra-worker:** новые activities GitLab transfer repo / Vault
  migrate paths / Harbor update metadata + `CatalogBeginTransfer`/
  `CatalogCommitTransfer`; интеграционные методы (interface + Memory + HTTP,
  SSRF-guard).
- **services/idm:** перенос ролей владельцев (`RevokeRole`+`AssignRole`+
  `InvalidateSubject` — примитивы есть, подключить из потока); seed прав
  `transfer@project:demo`, `transfer_in@project:demo2`, проект `project:demo2`.
- **services/gateway:** маршрут `POST .../transfer`, двусторонний RBAC
  (`transfer`+`transfer_in`), маппинг кодов (переиспользует `Aborted→409`,
  `AlreadyExists→409`, `FailedPrecondition→422` из ADR-0012).
- **web:** действие/диалог переноса с выбором target и предупреждением, zod-схемы,
  TanStack-мутация, `StatusBadge` для `transferring`.
- **БД:** миграция `services/projects/migrations` (CHECK + `transferring`), seed
  IDM `services/idm/migrations` (`project:demo2`, права переноса).
- **Откат/компенсации:** Saga workflow — компенсация идемпотентна ДО точки
  невозврата (transfer GitLab); ПОСЛЕ — форвард-only ретраи + алерт оператору, без
  молчаливого отката (ADR-0005/0008).
- **Зависимости:** при новых общих зависимостях — `GOWORK=off go mod tidy` во всех
  затронутых модулях (tidy-check/govulncheck). NB: `services/gateway/gateway` —
  закоммиченный бинарь, после сборки не коммитить (`git checkout --
  services/gateway/gateway`).
