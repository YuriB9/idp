## Context

Пятый и последний сквозной сценарий MVP — «Перенос сервиса» (transfer),
docs/IDP_MVP_plan.md (Этап 3, «Перенос сервиса»; порядок реализации — пункт 5,
самый рискованный, делается последним). Текущее состояние:

- Контракт `proto/projects/v1`: `Service = {project, name, status, owners,
  owners_version, decommissioned_at}`, RPC `GetService/ListServices/CreateService/
  SetServiceOwners/DecommissionService`. `ServiceStatus` enum: `UNSPECIFIED=0,
  CREATING=1, ACTIVE=2, DECOMMISSIONED=3, FAILED=4`. RPC переноса и значения
  `TRANSFERRING` нет.
- Каталог `services/projects`: таблица `services` (id/project/name/status/
  created_at/updated_at/decommissioned_at), `CHECK status IN (creating,active,
  decommissioned,failed)`, таблица `service_owners` (FK `service_id` CASCADE) +
  `owners_version`. `repository` умеет guarded-CAS статусов (ADR-0004), soft-delete
  (ADR-0012), но доменной операции смены колонки `project` нет. Дубликат
  `(project, name)` ловится unique-violation → `errs.ErrConflict`.
- Провижн: пакеты `services/projects/provisioning`, `changeowners`, `decommission`
  (Saga, ADR-0005/0008). Activities в `services/devinfra-worker` против моков
  GitLab/Harbor/Vault c SSRF-guard и RetryPolicy. Интеграции (`integrations.go`):
  GitLab `CreateRepo/DeleteRepo/InjectVariables/SyncMembers/RestoreMembers/Archive/
  Unarchive`; Harbor `CreateProject/DeleteProject/SetReadOnly/SetWritable`; Vault
  `SetupAppRole/TeardownAppRole/SyncOwners/RestoreOwners/RevokeSecretID`. Операций
  переноса (GitLab transfer / Vault migrate paths / Harbor update metadata) нет.
- IDM (`idm-rbac-min` + `change-service-owners`): RBAC, `AccessService.CheckAccess`
  + кэш DragonflyDB; `RoleAdminService` `AssignRole/RevokeRole` + `InvalidateSubject`
  (идемпотентны). Роль `owner:project:demo` per-project. Программного переноса
  ролей между проектами из доменного потока нет (примитивы есть).
- gateway: маршруты `POST/GET/LIST /projects/{project}/services`, `PUT .../owners`,
  `POST .../decommission`; helper `authorize(w,r,project,action)`; `httpFromGRPC`
  (`NotFound→404`, `Aborted→409`, `AlreadyExists→409`, `FailedPrecondition→422`,
  `InvalidArgument→400`, `PermissionDenied→403`). projects `mapError`
  (`ErrConflict→Aborted`, `ErrPrecondition→FailedPrecondition`, ADR-0012).
- Портал: список (со `StatusBadge`, умеет `decommissioned`), формы создания/
  владельцев, `DecommissionCard`. Действия переноса нет.

Ограничения (обязательны, openspec/config.yaml + docs/): комментарии в коде —
только на русском; fail-closed RBAC (ADR-0003/0010); SSRF-guard на исходящих;
guarded-CAS на переходах/смене `project` (ADR-0004); Saga с идемпотентными
компенсациями ДО PONR (ADR-0005/0008); миграции goose обратимые (`GOWORK=off`,
пин `./tools`); go.work-монорепо (при новых общих зависимостях — `GOWORK=off go
mod tidy` во всех затронутых модулях); не раскрывать внутренние ошибки клиенту
(детали — в лог по ключу slog `err`); перенос затрагивает ДВА проекта → проверка
прав на target обязательна.

## Goals / Non-Goals

**Goals:**

- Ввести доменную операцию переноса: смена колонки `project` `source→target`
  guarded-CAS с проверкой свободы `(target_project, name)`, сохранение id записи и
  владельцев (FK `service_id`), идемпотентность повторного вызова.
- Ввести транзитный статус `transferring` (аддитивно в enum и `CHECK`) для
  наблюдаемости и защиты от конкурентных операций на время переноса.
- Расширить контракт `proto/projects/v1` RPC `TransferService`, регенерировать
  Go/TS.
- Реализовать Temporal-workflow «Перенос» (Saga) с ТОЧКОЙ НЕВОЗВРАТА на transfer
  GitLab и алертом оператору при частичном сбое после PONR (не молчаливый откат).
- Закрыть двустороннюю авторизацию: действия `transfer` (source) и `transfer_in`
  (target), проверка `CheckAccess` для обоих проектов в gateway + projects
  (defense-in-depth, fail-closed).
- Перенести роли владельцев в IDM (`revoke owner:project:<source>` +
  `assign owner:project:<target>` + `InvalidateSubject`) из доменного потока.
- REST-ручка периметра + минимальный UI с подтверждением и предупреждением о
  рисках; локальный сквозной перенос `demo→demo2` при включённом RBAC.
- ЗАФИКСИРОВАТЬ решения в ADR-0013.

**Non-Goals:**

- Полное физическое удаление (hard delete) / restore из `decommissioned`.
- Реальные (не мок) GitLab/Vault/Harbor; реальный K8s-worker; реальный
  OIDC/Keycloak realm; богатая модель политик/иерархий/динамическое создание
  ролей под новые проекты в IDM (в MVP роли сидируются).
- Перенос между проектами с переименованием сервиса (имя сохраняется; конфликт
  имени в target → отказ, а не авто-переименование).
- Откат после точки невозврата (необратимые шаги → форвард-only + алерт).

## Decisions

### Решение 1: Семантика переноса и допустимые исходные статусы

Перенос меняет проект-владельца сервиса: **id записи каталога СОХРАНЯЕТСЯ**,
меняется только колонка `project` (`source→target`); владельцы переезжают вместе с
записью автоматически (таблица `service_owners` связана FK `service_id`,
переписывать строки владельцев не нужно). `decommissioned_at` остаётся `NULL`
(переносим только активный сервис). Уникальность `(project, name)` в каталоге
требует, чтобы пара `(target_project, name)` была СВОБОДНА — иначе конфликт.

Допустимый исходный статус — **только `active`**. Прочие:
- `transferring` → конфликт/предусловие (перенос уже идёт; защита от конкурентных
  операций, см. Решение 4);
- `creating`/`failed`/`decommissioned` → отказ-предусловие (`ErrPrecondition` →
  `FailedPrecondition` → 422).

Переход выполняется в ДВЕ guarded-CAS-фазы (Решение 4/7): `active→transferring`
(начало, компенсируемо) и `transferring→active` со сменой `project` (фиксация
после необратимых инфраструктурных шагов).

Альтернативы: одношаговая прямая смена `project` без транзитного статуса
(`active→active`) — отклонена: не даёт наблюдаемости незавершённого переноса и не
защищает от конкурентных операций на время длинного рискованного workflow.

### Решение 2: Транзитный статус `transferring` — ДА (аддитивно)

Вводим `SERVICE_STATUS_TRANSFERRING = 5` (аддитивное значение enum — не ломающее)
и расширяем `CHECK status IN (creating,active,decommissioned,failed,transferring)`
(обратимая миграция goose).

Обоснование (почему статус нужен, в отличие от decommission, где статус-перехода
хватило):
- **Защита от конкурентных операций.** Перенос длинный и рискованный. CAS
  `active→transferring` атомарно «забирает» сервис: пока он `transferring`, любые
  конкурентные `transfer`/`decommission`/`change_owners` получают
  `ErrPrecondition`/`ErrConflict` (их guarded-CAS требует `status='active'`), что
  исключает гонки на середине переноса.
- **Наблюдаемость.** UI и операторы видят сервис «в процессе переноса»; при сбое
  после PONR сервис может «зависнуть» в `transferring` — это явный сигнал для
  ручного разбора (вместо тихой неопределённости).
- **Стоимость мала.** Изменение аддитивно (enum + CHECK), не ломает wire-формат и
  существующие переходы.

Альтернатива (как в decommission — без транзитного статуса, прямой `active→active`
+ логи workflow) отклонена: для самого рискованного сценария с PONR явный
транзитный статус — оправданная защита и наблюдаемость.

### Решение 3: Форма контракта и идемпотентность

Новый RPC `TransferService(TransferServiceRequest) returns
(TransferServiceResponse)`:

```proto
message TransferServiceRequest {
  string project = 1;        // исходный проект-владелец (source)
  string name = 2;           // имя сервиса (сохраняется при переносе)
  string target_project = 3; // целевой проект-владелец (target)
}
message TransferServiceResponse {
  Service service = 1;       // итоговое состояние (project=target, status=ACTIVE)
}
```

Добавляется значение enum `SERVICE_STATUS_TRANSFERRING = 5` (аддитивно). Новый RPC
в существующем сервисе помечается BREAKING в комментарии контракта (как
`DecommissionService`). Идемпотентность: повторный `TransferService` на уже
перенесённом сервисе (`project` уже равен `target`, статус `active`) → success с
итоговым состоянием (no-op, новый workflow не стартует). Отдельного
expected-условия (версии) не вводим: целевое состояние однозначно определяется
парой `(target_project, name)`, а защиту от гонок даёт транзитный статус
(Решение 2) + детерминированный `WorkflowID`.

Альтернатива (`expected_version`/`if-match`) отклонена: для переноса нет
декларативного набора, как у владельцев; идемпотентность обеспечивается
детерминированным состоянием и `WorkflowID`.

### Решение 4: gRPC-обработчик и порядок проверок

`grpcapi.TransferService`:

1. Валидация (`project`/`name`/`target_project` обязательны; `target_project !=
   project`) → иначе `InvalidArgument`.
2. **Двусторонняя авторизация** (Решение 5): `CheckAccess(subject, "transfer",
   "project:<source>")` И `CheckAccess(subject, "transfer_in",
   "project:<target>")`; отказ/недоступность любого → `PermissionDenied`
   (fail-closed), без побочных эффектов.
3. Прочитать запись каталога source; нет → `NotFound`. Если `project` уже равен
   `target` и статус `active` → идемпотентный no-op success (workflow не стартует).
   Если статус не `active` (`creating`/`failed`/`decommissioned`) →
   `FailedPrecondition`. Если `transferring` → перенос уже идёт →
   `FailedPrecondition`/`Aborted`.
4. Старт Temporal-workflow «Перенос» с детерминированным `WorkflowID =
   "transfer:<source>:<name>"` (привязка к исходной паре до фиксации).

Сама смена `project` и guarded-CAS — внутри workflow (все записи каталога — в
activities, тело workflow детерминировано). Проверка свободы `(target_project,
name)` выполняется на первом шаге каталога в транзакции (Решение 7), а не
check-then-act в обработчике. Внутренние ошибки наружу не раскрываются.

### Решение 5: Двусторонняя авторизация — `transfer` (source) И `transfer_in` (target)

Перенос затрагивает два проекта, поэтому требует ДВУХ прав:
- `transfer` на `project:<source>` — право «вынести» сервис из исходного проекта;
- `transfer_in` на `project:<target>` — право «принять» сервис в целевой проект.

Оба проверяются `CheckAccess` в ДВУХ точках (defense-in-depth):
- gateway: `authorize(w, r, source, "transfer")` И `authorize(w, r, target,
  "transfer_in")` перед проксированием;
- projects: те же две проверки перед доменной операцией и стартом workflow.

Fail-closed: отказ/недоступность/ошибка IDM по ЛЮБОМУ из двух проектов →
`PermissionDenied`/403 без побочных эффектов и без раскрытия деталей
(ADR-0003/0010). `subject` — из claims контекста. Это критичное security-решение:
без права `transfer_in` на target нельзя «вынести» сервис в чужой проект.

Локально сидируются: `transfer@project:demo` и `transfer_in@project:demo2` →
субъекту `demo-user`.

Альтернатива (одно действие `transfer` только на source) отклонена: позволила бы
переместить сервис в произвольный чужой проект без согласия/прав на target —
недопустимо.

### Решение 6: Перенос ролей владельцев IDM и инвалидация кэша

После фиксации каталога workflow переносит роли владельцев между проектами для
каждого затронутого субъекта-владельца:
- `RevokeRole(subject, "owner:project:<source>")` — снять роль владельца в source;
- `AssignRole(subject, "owner:project:<target>")` — выдать роль владельца в target;
- `InvalidateSubject(subject)` по ВСЕМ затронутым субъектам — чтобы не осталось
  устаревших `allow` в кэше DragonflyDB.

Примитивы `AssignRole/RevokeRole/InvalidateSubject` идемпотентны (есть из
`change-service-owners`). В отличие от decommission (где per-project роль НЕ
отзывалась во избежание over-revoke сиблингов, ADR-0012), при переносе сервис —
вместе со всеми владельцами — целиком покидает source-проект, поэтому перенос
ролей корректен: владельцы перестают иметь основание для роли в source (в рамках
демо-модели «один сервис на проект» это безопасно) и получают её в target. Это
форвард-only шаг ПОСЛЕ PONR: при сбое — ретраи + алерт, не откат.

NB ограничение MVP: роли per-project и сидируются. Перенос предполагает, что
роль `owner:project:<target>` существует (сидирована). Динамическое создание
ролей под новые проекты — вне scope.

### Решение 7: Доменная операция каталога (две фазы, guarded-CAS, транзакция)

Две activity-обёртки над repository (по образцу `CatalogDecommission`):

**Фаза A — `CatalogBeginTransfer(serviceID, target)`** (до PONR, компенсируемо):
в ОДНОЙ транзакции:
1. проверить свободу `(target, name)` (нет конфликтующей записи) — занято →
   `errs.ErrConflict`;
2. guarded-CAS `UPDATE services SET status='transferring', updated_at=now() WHERE
   id=$id AND status='active'`; `RowsAffected==0` → разбор статуса (`transferring`
   → уже идёт; `creating`/`failed`/`decommissioned` → `ErrPrecondition`;
   конкурентная смена → `ErrConflict`).

Компенсация `CatalogAbortTransfer`: guarded-CAS `transferring→active` (вернуть
исходный статус, `project` не менялся).

**Фаза B — `CatalogCommitTransfer(serviceID, target)`** (после PONR, форвард-only):
guarded-CAS `UPDATE services SET project=$target, status='active', updated_at=now()
WHERE id=$id AND status='transferring'`. Защищена повторной проверкой свободы
`(target, name)` (на случай гонки) в той же транзакции. `RowsAffected==0` →
`ErrConflict` (для алерта workflow). Идемпотентно: повтор на уже перенесённой
записи (`project=target, status=active`) → no-op success.

Занятое `(target_project, name)` детектируется и до side-effect (Фаза A), и
повторно в Фазе B; источник истины — unique-constraint каталога.

### Решение 8: Дизайн workflow «Перенос» (Saga) и ТОЧКА НЕВОЗВРАТА

Новый публичный пакет `services/projects/transfer` (по образцу `decommission`,
ADR-0008): имена workflow/activities, детерминированный `WorkflowID =
"transfer:<source>:<name>"`, `WorkflowIDReusePolicy` как у провижна. Тело
детерминировано, I/O — в activities; единые `ActivityOptions` (StartToClose 30s,
Heartbeat 15s, RetryPolicy: 1s base, x2, max 10s, 5 попыток).

Вход workflow: `{ServiceID, Source, Target, Name}`.

Шаги и **точка невозврата**:

0. `CatalogBeginTransfer(serviceID, target)` — Фаза A (Решение 7). Компенсируемо
   (`CatalogAbortTransfer`). Занятое имя/неверный статус → ошибка ДО side-effect.
1. `GitLabTransferRepo(source, target, name)` — перенос репозитория в новую группу.
   **НЕОБРАТИМО (в MVP не моделируем чистый transfer-back: меняются namespace/URL)
   → ТОЧКА НЕВОЗВРАТА.**
2. `VaultMigratePaths(source, target, name)` — копия секретов `source→target` +
   запись новых политик + очистка старых путей. Форвард-only, идемпотентно.
3. `HarborUpdateMetadata(source, target, name)` — обновление метаданных/прав
   директории образов под target. Форвард-only, идемпотентно.
4. `CatalogCommitTransfer(serviceID, target)` — Фаза B: guarded-CAS
   `transferring→active` + `project=target` (Решение 7). Форвард-only, идемпотентно.
5. `TransferOwnerRoles(source, target, owners)` — перенос ролей IDM + инвалидация
   (Решение 6). Форвард-only, идемпотентно.

Политика отказов (ADR-0005/0008):
- Сбой шага 0 (предусловие/конфликт) → ошибки наружу, побочных эффектов НЕТ.
- Сбой шага 0 ПОСЛЕ успешного CAS, но ДО шага 1 (например, инфра-сбой запуска) —
  компенсация `CatalogAbortTransfer` (`transferring→active`), workflow → ошибка.
- Шаг 1 (GitLab transfer) и далее — **форвард-only**: молчаливого отката НЕТ.
  Идемпотентные шаги ретраятся; при окончательном сбое любого из шагов 1–5
  (включая конфликт guarded-CAS на шаге 4) — **алерт оператору** структурным логом
  (`slog` ключ `err`): репозиторий/секреты уже частично перенесены, требуется
  форвард-довыполнение/ручной разбор; сервис может остаться в `transferring` до
  устранения. Каталог = целевой источник правды.

Sequence (happy-path и ветки):

```
projects(API)        Temporal           DevInfra worker        GitLab/Vault/Harbor/Catalog/IDM
   |  Transfer           |                    |                          |
   |-- CheckAccess src -->| (transfer, fail-closed)                      |
   |-- CheckAccess tgt -->| (transfer_in, fail-closed)                   |
   |-- read status ------>| (active? иначе no-op/precond/404)            |
   |-- StartWorkflow ---->| (WorkflowID=transfer:src:name)               |
   |                      |-- Task ----------->|                          |
   |                      |                    |-- CatalogBeginTransfer -->| CAS active→transferring (+своб. имени)
   |                      |                    |<-- ok --------------------|
   |                      |                    |-- GitLabTransferRepo ---->| ТОЧКА НЕВОЗВРАТА (необратимо)
   |                      |                    |<-- ok --------------------|
   |                      |                    |-- VaultMigratePaths ----->| копия+новые политики+очистка старых
   |                      |                    |<-- ok --------------------|
   |                      |                    |-- HarborUpdateMetadata -->| метаданные/права под target
   |                      |                    |<-- ok --------------------|
   |                      |                    |-- CatalogCommitTransfer ->| CAS transferring→active, project=target
   |                      |                    |<-- ok --------------------|
   |                      |                    |-- TransferOwnerRoles ---->| revoke owner:src + assign owner:tgt + invalidate
   |                      |                    |<-- ok --------------------|
   |                      |<-- complete -------|                          |

  Ветка компенсации (сбой до точки невозврата — на шаге 0):
   |                      |                    |-- CatalogBeginTransfer -->| занято имя/неверный статус → ошибка
   |                      |<-- fail -----------|   побочных эффектов НЕТ
   |   (если CAS прошёл, но старт шага 1 сорвался) → CatalogAbortTransfer (transferring→active)

  Ветка алерта (сбой после точки невозврата):
   |                      |                    |-- GitLabTransferRepo ---->| ok (НЕВОЗВРАТ)
   |                      |                    |-- VaultMigratePaths --(fatal)
   |                      |   форвард-only ретраи; при исчерпании — АЛЕРТ оператору (slog err)
   |                      |   сервис остаётся в transferring до ручного довыполнения
```

### Решение 9: Периметр (REST, ADR-0009) и маппинг ошибок

`POST /projects/{project}/services/{name}/transfer`, тело `{target_project}`.
POST-действие на под-ресурсе выбрано (по образцу `decommission`): перенос —
доменное действие, не CRUD-replace ресурса. Идемпотентно: повторный POST на уже
перенесённом сервисе → success (итоговое состояние). gateway вызывает `CheckAccess`
для ОБОИХ проектов (Решение 5) перед проксированием. OpenAPI + TS-клиент
регенерируются (`gen:check`).

Маппинг ошибок (сводно, переиспользует разведение 409/422 из ADR-0012):

| Ситуация | repository | projects (gRPC) | gateway (HTTP) |
|---|---|---|---|
| Нет права (source или target) / IDM недоступен | — | `PermissionDenied` | 403 |
| Сервис не найден | `ErrNotFound` | `NotFound` | 404 |
| Занятое имя в target / конкурентный конфликт guarded-CAS | `ErrConflict` | `Aborted` | 409 |
| Недопустимый исходный статус (creating/failed/decommissioned/transferring) | `ErrPrecondition` | `FailedPrecondition` | 422 |
| Невалидный запрос (нет target / target==source) | `ErrValidation` | `InvalidArgument` | 400 |
| Внутренняя ошибка | — | `Internal` (без деталей) | 500 |

Занятое имя в target и конкурентный конфликт guarded-CAS оба → `ErrConflict →
Aborted → 409` (как дубликат `(project, name)` при `CreateService`), UI
показывает различающиеся тексты по контексту. `AlreadyExists→409` в gateway уже
есть и сохраняется. Никаких новых правил `httpFromGRPC` не требуется (ADR-0012 уже
ввёл `Aborted→409` и `FailedPrecondition→422`).

### Решение 10: Объём UI

UI держим в этом change (по образцу `DecommissionCard`/`OwnersCard`): карточка
«Перенести сервис» с подтверждением (ввод имени), ЯВНЫМ предупреждением о
необратимости (transfer GitLab/миграция Vault), выбором/вводом target-проекта,
обработкой 403/409/422, индикацией `transferring`/результата. Объём сопоставим с
`DecommissionCard` — выносить в отдельный change не требуется.

## Risks / Trade-offs

- [BREAKING контракта projects] → Изменения аддитивны (новое значение enum
  `TRANSFERRING=5`, новый RPC), номера полей не переиспользуются; регенерация Go и
  TS в одном change, `gen:check` ловит дрейф.
- [Точка невозврата: репозиторий/секреты перенесены, каталог ещё `transferring`] →
  Форвард-only ретраи идемпотентных шагов (Vault/Harbor/каталог/IDM); при
  исчерпании — алерт оператору (не тихий откат). Повторный запуск workflow с тем же
  `WorkflowID` довыполнит шаги идемпотентно; каталог = целевой источник правды.
- [Сервис «завис» в `transferring`] → Явный наблюдаемый сигнал для оператора (UI +
  лог-алерт); статус защищает от конкурентных операций; ручное довыполнение/
  перезапуск workflow.
- [Занятое имя `(target_project, name)`] → Детект до side-effect (Фаза A) и
  повторно в Фазе B; источник истины — unique-constraint; → `ErrConflict`/409 без
  частичного переноса (при детекте на Фазе A).
- [Перенос в чужой проект без прав на target] → Двусторонняя авторизация
  (`transfer` + `transfer_in`), fail-closed, в двух точках (Решение 5).
- [Перенос ролей IDM оставляет устаревший allow] → `RevokeRole` на source +
  `AssignRole` на target + `InvalidateSubject` по всем затронутым субъектам
  (Решение 6); примитивы идемпотентны.
- [GitLab transfer-back не моделируется как чистая компенсация] → Поэтому transfer
  GitLab = PONR (Решение 8): до него только каталог (компенсируемо), после —
  форвард-only; не оставляем полу-перенесённый репозиторий в неопределённости.
- [SSRF/утечка секретов в моках] → SSRF-guard на исходящих (как в провижне); при
  миграции Vault секреты/токены НЕ логируются.
- [go.work tidy-drift при новых зависимостях] → `GOWORK=off go mod tidy` во всех
  затронутых модулях перед PR (tidy-check/govulncheck). `services/gateway/gateway`
  (бинарь) не коммитить.

## Migration Plan

1. Ветка `change/transfer-service` от master (прямые коммиты в master запрещены).
2. Контракты: расширить `proto/projects/v1` (`TransferService` +
   `SERVICE_STATUS_TRANSFERRING`), `buf generate`, регенерировать TS-клиент;
   зафиксировать `gen:check`.
3. БД: обратимая миграция `services/projects/migrations` (CHECK + `transferring`);
   проверить `up`/`down`.
4. projects: repository (`CatalogBeginTransfer`/`CommitTransfer`/`AbortTransfer`,
   guarded-CAS смены `project`, проверка свободы `(target, name)`), usecase,
   grpcapi (`TransferService`, двойной `authorize`), пакет `transfer` (workflow +
   starter).
5. devinfra-worker: интеграции/activities GitLab transfer / Vault migrate paths /
   Harbor update metadata + `TransferOwnerRoles`; регистрация.
6. gateway: маршрут `POST .../transfer`, двусторонний RBAC (`transfer`+
   `transfer_in`); маппинг кодов уже есть из ADR-0012.
7. IDM/локалка: seed проекта `project:demo2`, ролей и прав
   (`transfer@project:demo`, `transfer_in@project:demo2`); прогнать сквозной
   перенос `demo→demo2` при включённом RBAC.
8. web: действие/диалог переноса (выбор target, предупреждение о рисках), zod,
   мутация, `StatusBadge` для `transferring`.
9. README/инструкция; `GOWORK=off go mod tidy` по модулям; зелёный CI; merge;
   затем `/opsx:archive`.

Откат: миграция обратима (`goose down`); контрактные изменения аддитивны; выкладка
по сервисам независима (worker регистрирует workflow по имени — старые потоки не
затрагиваются).

## Open Questions

- Сервис-скоупные роли владельцев (вместо per-project) и динамическое создание
  ролей под новые проекты — упростили бы перенос ролей без предположения о
  сидированной `owner:project:<target>`. В MVP роли сидируются.
- Реальный transfer-back GitLab как компенсация (если появится модель обратимого
  group-transfer) — позволил бы сдвинуть PONR позже; в MVP transfer GitLab = PONR.
- Перенос сервиса с конфликтом имени в target через авто-переименование — вне
  scope (сейчас занятое имя → отказ).
