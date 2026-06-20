## Context

Четвёртый сквозной сценарий MVP — «Удаление / вывод из эксплуатации сервиса»
(soft delete / decommission), docs/IDP_MVP_plan.md (Этап 3, «Удаление сервиса»,
строки ~171–178; порядок реализации — пункт 4). Текущее состояние:

- Контракт `proto/projects/v1`: `Service = {project, name, status, owners,
  owners_version}`, RPC `GetService/ListServices/CreateService/SetServiceOwners`.
  `ServiceStatus` enum уже содержит `SERVICE_STATUS_DECOMMISSIONED = 3`. RPC
  вывода из эксплуатации нет.
- Каталог `services/projects`: таблица `services` (id/project/name/status/
  created_at/updated_at), `CHECK status IN (creating,active,decommissioned,failed)`,
  таблица `service_owners` + `owners_version`. `repository.TransitionStatus`
  (guarded-CAS статусов, ADR-0004) — переход `ACTIVE→DECOMMISSIONED` уже выразим
  механически, но доменной операции soft-delete (с сохранением данных,
  идемпотентностью, `decommissioned_at`) нет.
- Провижн: пакет `services/projects/provisioning` + `services/projects/changeowners`
  (Saga, ADR-0008); activities в `services/devinfra-worker` против моков
  GitLab/Harbor/Vault c SSRF-guard и RetryPolicy. У интеграций есть
  Create/Delete/Teardown (для компенсаций провижна), но НЕТ обратных операций
  decommission (archive / read-only / revoke).
- IDM (`idm-rbac-min` + `change-service-owners`): модель RBAC, gRPC
  `AccessService.CheckAccess` + кэш в DragonflyDB; `RoleAdminService`
  `AssignRole/RevokeRole` + `InvalidateSubject`. Роль `owner:project:demo`
  per-project.
- gateway: `POST/GET/LIST /projects/{project}/services`, `PUT .../owners`, helper
  `authorize(w,r,project,action)`, `httpFromGRPC` (NotFound→404,
  FailedPrecondition/AlreadyExists→409, InvalidArgument→400, PermissionDenied→403).
- Портал: список (со статус-бейджем, `StatusBadge` уже умеет `decommissioned`),
  форма/прогресс создания, карточка владельцев. Действия decommission нет.

Ограничения (обязательны, openspec/config.yaml + docs/): комментарии в коде —
только на русском; fail-closed RBAC (ADR-0003/0010); SSRF-guard на исходящих;
guarded-CAS на переходах (ADR-0004); Saga с идемпотентными компенсациями
(ADR-0005/0008); миграции goose обратимые (`GOWORK=off`, пин `./tools`);
go.work-монорепо (при новых общих зависимостях — `GOWORK=off go mod tidy` во всех
затронутых модулях); данные при soft-delete НЕ удалять; не раскрывать внутренние
ошибки клиенту (детали — в лог по ключу slog `err`).

## Goals / Non-Goals

**Goals:**

- Ввести доменную операцию soft-delete каталога: guarded-CAS `ACTIVE→DECOMMISSIONED`
  с сохранением данных, `decommissioned_at`, идемпотентность повторного вызова.
- Расширить контракт `proto/projects/v1` RPC `DecommissionService` (+ поле
  `decommissioned_at`), регенерировать Go/TS.
- Реализовать Temporal-workflow «Вывод из эксплуатации» (Saga): предусловие
  снятой нагрузки K8s → GitLab archive + revoke / Harbor read-only + robot revoke /
  Vault revoke SecretID → каталог; точка невозврата на первом необратимом отзыве
  + алерт.
- ЗАКРЫТЬ открытый вопрос плана: проверка снятой нагрузки K8s при отсутствии
  K8s-worker — явный чек-флаг/предусловие за интерфейсом `LoadChecker` с границей
  под будущий worker (обоснование ниже, ADR-0012).
- Защитить операцию RBAC-действием `decommission` (gateway + projects,
  defense-in-depth, fail-closed).
- REST-ручка периметра + минимальный UI с подтверждением; локальный сквозной
  сценарий при включённом RBAC.

**Non-Goals:**

- «Перенос сервиса» (отдельный последующий change, самый рискованный).
- Полное физическое удаление (hard delete / purge) записей и ресурсов.
- Реальные (не мок) GitLab/Vault/Harbor; реальный K8s-worker (закладываем только
  границу/интерфейс проверки нагрузки); реальный OIDC/Keycloak realm; богатая
  модель политик/иерархий в IDM.
- Restore (возврат `DECOMMISSIONED→ACTIVE`) — данные сохраняются для будущего
  restore, но сам поток восстановления вне scope.

## Decisions

### Решение 1: Семантика soft-delete и допустимые исходные статусы

Decommission — это soft-delete: запись каталога и владельцы СОХРАНЯЮТСЯ, меняется
только `status` на `decommissioned` и проставляется `decommissioned_at`. Допустимый
исходный статус — **только `active`**. Переход:

```
UPDATE services
   SET status = 'decommissioned', decommissioned_at = now(), updated_at = now()
 WHERE id = $id AND status = 'active';
-- RowsAffected == 0 → требует разбора текущего статуса (см. ниже)
```

Обработка прочих исходных статусов (guarded-CAS дал `RowsAffected==0` → перечитать
статус):

- `decommissioned` → **идемпотентный no-op success** (целевое состояние уже
  достигнуто; повторный вызов безопасен, в т.ч. при повторной доставке/ретрае
  workflow и повторном клике в UI).
- `creating` / `failed` → **отказ-предусловие** (`ErrPrecondition` →
  `FailedPrecondition` → HTTP 422): из этих статусов вывод из эксплуатации
  недопустим (сервис ещё не активен / в ошибочном состоянии — это иной поток).
- параллельная конкурентная смена `active`→что-то ещё между чтением и CAS →
  **конфликт** (`ErrConflict` → `Aborted` → HTTP 409).

`decommissioned_at` — новая nullable-колонка `timestamptz` (обратимая миграция
goose), нужна для наблюдаемости, окна будущего restore и аудита. Отражается в
`GetService`/`ListServices` (аддитивное поле контракта). Владельцы при
decommission НЕ удаляются (данные сохраняются; см. Решение 5 про роли IDM).

### Решение 2: Форма контракта и идемпотентность

Новый RPC `DecommissionService(DecommissionServiceRequest) returns
(DecommissionServiceResponse)`:

```proto
message DecommissionServiceRequest {
  string project = 1;
  string name = 2;
  bool load_drained = 3; // явное предусловие: нагрузка снята из K8s (см. Решение 4)
}
message DecommissionServiceResponse {
  Service service = 1; // итоговое состояние (status=DECOMMISSIONED, decommissioned_at)
}
```

`decommissioned_at` добавляется в `Service` новым номером поля (аддитивно). Новый
RPC в существующем сервисе помечается BREAKING в комментарии контракта (как и
`SetServiceOwners`). Семантика идемпотентна: повторный `DecommissionService` на
уже выведенном сервисе → success c итоговым состоянием (no-op). Это согласуется с
soft-delete и устойчиво к повторам.

### Решение 3: gRPC-обработчик и порядок проверок

`grpcapi.DecommissionService`:

1. Валидация (`project`/`name` обязательны) → иначе `InvalidArgument`.
2. `CheckAccess(subject, "decommission", "project:<project>")` (см. Решение 6),
   fail-closed → `PermissionDenied`.
3. Прочитать запись каталога; нет → `NotFound`. Если уже `decommissioned` →
   вернуть текущее состояние (идемпотентный no-op, workflow не стартует). Если
   `creating`/`failed` → `FailedPrecondition` (422).
4. Предусловие снятой нагрузки: `load_drained` (см. Решение 4) — если ложно/не
   подтверждено → `FailedPrecondition` (422), workflow не стартует, побочных
   эффектов нет.
5. Старт Temporal-workflow «Вывод из эксплуатации» с детерминированным
   `WorkflowID = "decommission:<project>:<name>"`.

Внутренние ошибки наружу не раскрываются (`err.Error()` не отдаём; детали — в лог
по ключу slog `err`).

### Решение 4: ЗАКРЫТИЕ открытого вопроса — проверка снятой нагрузки K8s

Открытый вопрос плана: как проверить, что нагрузка снята из K8s до старта
workflow, при отсутствии K8s-worker в MVP — **прямой запрос к кластеру** vs
**явный чек-флаг/предусловие**.

**Решение: явный чек-флаг/предусловие за интерфейсом `LoadChecker`, реализованным
как предварительный шаг (валидация + activity), с границей под будущий K8s-worker.**
Прямой запрос к кластеру отклонён.

Обоснование:

- В MVP K8s-worker отсутствует и нет управляемого кластера, к которому можно было
  бы обратиться, — «прямой запрос» в MVP был бы фиктивным/замоканным и создавал
  бы ложное ощущение реальной проверки.
- Честная, тестируемая граница: вызывающая сторона ЯВНО подтверждает снятие
  нагрузки (флаг `load_drained` в запросе/теле REST), а доменный слой трактует
  это как предусловие. Это делает ответственность явной и проверяемой (happy/
  отказ предусловия — детерминированно в тестах без кластера).
- Архитектурная граница сохраняется: вводится интерфейс `LoadChecker` (например,
  `EnsureDrained(ctx, project, name) error`) и **предварительный шаг workflow**
  (activity `EnsureLoadDrained`). MVP-реализация проверяет переданное предусловие
  (`load_drained`); будущий K8s-worker подменит реализацию реальным запросом к
  кластеру без изменения ядра workflow и контракта.
- Предусловие проверяется ДО любых побочных эффектов (до отзыва доступов). Не
  выполнено → `FailedPrecondition` → 422, без частичных изменений.

Решение фиксируется в ADR-0012.

### Решение 5: Отзыв доступа и роли IDM при decommission

«Немедленное прекращение доступа» в MVP достигается отзывом во ВНЕШНИХ системах
(сервис-скоупно), а не отзывом per-project роли IDM:

- GitLab: archive репозитория + отзыв доступов участников (revoke members).
- Harbor: проект → read-only + отзыв Robot-аккаунта.
- Vault: отзыв активных SecretID/токенов сервиса (немедленное прекращение
  аутентификации — это ключевое необратимое действие).

Роль владельцев `owner:project:<project>` в MVP **НЕ отзывается** при decommission
одного сервиса. Обоснование: роль per-project (общая для всех сервисов проекта),
её отзыв при выводе одного сервиса некорректно лишил бы доступа к остальным
активным сервисам проекта. Реальное прекращение доступа к выводимому сервису
происходит на уровне внешних систем (сервис-скоупно). Программный отзыв роли через
`RevokeRole` + `InvalidateSubject` остаётся доступным примитивом и будет подключён,
когда роли станут сервис-скоупными (Open Question / будущий change). Это осознанный
выбор, зафиксирован в ADR-0012.

### Решение 6: Авторизация — действие `decommission`, две точки CheckAccess

- gateway: `authorize(w, r, project, "decommission")` перед проксированием.
- projects: `authorize(ctx, project, "decommission")` перед доменной операцией и
  стартом workflow (defense-in-depth).

Fail-closed: отказ/недоступность/ошибка IDM → `PermissionDenied`/403 без побочных
эффектов и без раскрытия деталей (ADR-0003/0010). `subject` — из claims контекста.
Локально право `decommission@project:demo` сидируется субъекту `demo-user`.

### Решение 7: Дизайн workflow «Вывод из эксплуатации» (Saga)

Новый публичный пакет `services/projects/decommission` (по образцу `provisioning`/
`changeowners`, ADR-0008): имена workflow/activities, детерминированный
`WorkflowID = "decommission:<project>:<name>"` (идемпотентность повторного запуска,
`WorkflowIDReusePolicy` как у провижна). Тело детерминировано, I/O — в activities;
единые `ActivityOptions` (StartToClose 30s, Heartbeat 15s, RetryPolicy: 1s base,
x2, max 10s, 5 попыток) — как в провижне/changeowners.

Вход workflow: `{ServiceID, Project, Name, LoadDrained bool}`.

Шаги и **точка невозврата**:

0. `EnsureLoadDrained(project, name, loadDrained)` — предусловие снятой нагрузки
   (Решение 4). Не выполнено → non-retryable `ErrPrecondition` ДО любых побочных
   эффектов → workflow завершается ошибкой предусловия.
1. `GitLabArchive(project)` + отзыв доступов участников. Обратимо (unarchive/
   restore) → comp `GitLabUnarchive` доступна.
2. `HarborSetReadOnly(project)` + отзыв Robot. Обратимо → comp `HarborSetWritable`
   доступна.
3. `VaultRevokeSecretID(name)` — отзыв активных SecretID/токенов. **НЕОБРАТИМО →
   ТОЧКА НЕВОЗВРАТА** (отозванный токен нельзя «вернуть»; цель — прекратить
   доступ).
4. `CatalogDecommission(serviceID)` — guarded-CAS `ACTIVE→DECOMMISSIONED` +
   `decommissioned_at` (Решение 1).

Политика отказов (decommission содержит частично необратимые отзывы доступа —
ADR-0005/0008):

- Сбой шага 0 (предусловие) → ошибка предусловия, побочных эффектов НЕТ.
- Сбой шагов 1–2 (non-retryable после RetryPolicy, ДО точки невозврата) →
  идемпотентные компенсации в обратном порядке (`HarborSetWritable`,
  `GitLabUnarchive`), каталог не трогаем, workflow → ошибка.
- Шаг 3 (Vault) и далее — **форвард-only**: молчаливого отката НЕТ. Идемпотентные
  шаги ретраятся; при окончательном сбое шага 3 или 4 (включая конфликт
  guarded-CAS на шаге 4, когда статус сменился конкурентно) — алерт оператору
  структурным логом (доступ уже отозван, каталог = целевой источник правды,
  требуется форвард-довыполнение/ручной разбор).

Sequence (happy-path и ветки):

```
projects(API)        Temporal           DevInfra worker            K8s/GitLab/Harbor/Vault/Catalog
     |  Decommission      |                    |                            |
     |-- CheckAccess(IDM) ->| (decommission, fail-closed)                   |
     |-- read status ----->| (active? иначе no-op/precond/404)              |
     |-- StartWorkflow ---->|                    |                            |
     |   (WorkflowID=decommission:p:n)           |                            |
     |                      |-- Task ----------->|                            |
     |                      |                    |-- EnsureLoadDrained ------>| (предусловие; нет K8s-worker → флаг)
     |                      |                    |<-- ok ---------------------|
     |                      |                    |-- GitLabArchive+revoke --->| (моки, SSRF-guard)
     |                      |                    |<-- ok ---------------------|
     |                      |                    |-- HarborReadOnly+robot --->|
     |                      |                    |<-- ok ---------------------|
     |                      |                    |-- VaultRevokeSecretID ---->| ТОЧКА НЕВОЗВРАТА (необратимо)
     |                      |                    |<-- ok ---------------------|
     |                      |                    |-- CatalogDecommission(CAS)>| (guarded-CAS ACTIVE→DECOMMISSIONED)
     |                      |                    |<-- ok ---------------------|
     |                      |<-- complete -------|                            |

  Ветка предусловия (нагрузка не снята):
     |                      |                    |-- EnsureLoadDrained --(fatal precond)
     |                      |<-- fail -----------|   побочных эффектов НЕТ

  Ветка компенсации (сбой Harbor до точки невозврата):
     |                      |                    |-- HarborReadOnly ---(fatal)|
     |                      |                    |-- (comp) GitLabUnarchive ->| (идемпотентно)
     |                      |<-- fail -----------|   каталог НЕ изменён

  Ветка алерта (сбой после точки невозврата):
     |                      |                    |-- VaultRevokeSecretID ---->| ok (НЕВОЗВРАТ)
     |                      |                    |-- CatalogDecommission --(fatal/conflict)
     |                      |   форвард-only ретраи; при исчерпании — АЛЕРТ оператору (slog err)
```

### Решение 8: Периметр (REST, ADR-0009) и маппинг ошибок

`POST /projects/{project}/services/{name}/decommission`, тело
`{load_drained: bool}`. POST-действие на под-ресурсе выбрано вместо `DELETE`:
это soft-delete (данные сохраняются), а `DELETE` семантически подразумевал бы
удаление/purge — выбор POST исключает двусмысленность и явно отражает действие
вывода из эксплуатации. Тело несёт явное предусловие `load_drained` (Решение 4).
Идемпотентно: повторный POST на уже выведенном сервисе → success (итоговое
состояние). `decommissioned_at` добавляется в ответы `GET`/`LIST`; OpenAPI +
TS-клиент регенерируются (`gen:check`).

Маппинг ошибок (сводно):

| Ситуация | repository | projects (gRPC) | gateway (HTTP) |
|---|---|---|---|
| Нет права / IDM недоступен | — | `PermissionDenied` | 403 |
| Сервис не найден | `ErrNotFound` | `NotFound` | 404 |
| Конфликт guarded-CAS (конкурентная смена статуса) | `ErrConflict` | `Aborted` | 409 |
| Предусловие не выполнено (нагрузка не снята / статус `creating`/`failed`) | `ErrPrecondition` | `FailedPrecondition` | 422 |
| Невалидный запрос | — | `InvalidArgument` | 400 |
| Внутренняя ошибка | — | `Internal` (без деталей) | 500 |

Уточнение `httpFromGRPC`: чтобы развести конкурентный конфликт (409) и
невыполненное предусловие (422), принимается gRPC-каноничное разделение:
`codes.Aborted` = конкурентный конфликт (CAS) → 409; `codes.FailedPrecondition` =
семантическое предусловие → 422. Маппинг `repository.ErrConflict → Aborted`
вводится в projects; в gateway добавляется `Aborted→409` и уточняется
`FailedPrecondition→422`. Чтобы не регрессировать `change-owners` (конфликт версии
владельцев → 409), путь владельцев в projects также переводится на
`ErrConflict→Aborted` (итоговый HTTP-код 409 сохраняется; обновляются
соответствующие тесты projects/gateway). `AlreadyExists→409` сохраняется.

## Risks / Trade-offs

- [BREAKING контракта projects] → Изменения аддитивны по номерам полей
  (`decommissioned_at`), новый RPC помечен BREAKING в комментарии; регенерация Go
  и TS в одном change, `gen:check` в CI ловит дрейф.
- [Точка невозврата: доступ отозван, каталог ещё `active`] → Форвард-only ретраи
  идемпотентного `CatalogDecommission`; при исчерпании — алерт оператору (не тихий
  откат). Повторный запуск workflow с тем же `WorkflowID` довыполнит каталог
  идемпотентно; каталог = целевой источник правды.
- [Конфликт guarded-CAS после точки невозврата] → Доступ уже отозван; конфликт
  означает конкурентную смену статуса. Workflow фиксирует алерт оператору;
  молчаливого «возврата» доступа нет (это и есть цель decommission).
- [Фиктивность проверки нагрузки в MVP] → Закрыто Решением 4/ADR-0012: явное
  предусловие за интерфейсом `LoadChecker`, граница под будущий K8s-worker; без
  имитации несуществующего кластера.
- [Over-revoke роли владельцев] → В MVP роль per-project НЕ отзывается при
  decommission одного сервиса (Решение 5); доступ режется во внешних системах
  (сервис-скоупно). Документировано; полноценный отзыв — при сервис-скоупных ролях.
- [Изменение маппинга `ErrConflict→Aborted` затрагивает change-owners] → Итоговые
  HTTP-коды сохраняются (конфликт→409); обновляются тесты projects/gateway,
  проверяющие gRPC-код. Описано в Решении 8.
- [SSRF/утечка секретов в моках] → SSRF-guard на исходящих (как в провижне);
  секреты/токены не логируются.
- [go.work tidy-drift при новых зависимостях] → `GOWORK=off go mod tidy` во всех
  затронутых модулях перед PR (tidy-check/govulncheck). `services/gateway/gateway`
  (бинарь) не коммитить.

## Migration Plan

1. Ветка `change/decommission-service` от master (прямые коммиты в master
   запрещены).
2. Контракты: расширить `proto/projects/v1` (`DecommissionService` +
   `decommissioned_at`), `buf generate`, регенерировать TS-клиент; зафиксировать
   `gen:check`.
3. БД: обратимая миграция `services/projects/migrations` (`decommissioned_at`);
   проверить `up`/`down`.
4. projects: repository (soft-delete guarded-CAS `ACTIVE→DECOMMISSIONED`,
   `decommissioned_at`, `ErrConflict→Aborted`, `ErrPrecondition`), usecase,
   grpcapi (`DecommissionService`, `authorize` с `decommission`), пакет
   `decommission` (workflow + starter).
5. devinfra-worker: моки/activities GitLab archive+revoke / Harbor read-only+robot /
   Vault revoke SecretID + компенсации; `EnsureLoadDrained` (`LoadChecker`);
   `CatalogDecommission`; регистрация.
6. gateway: маршрут `POST .../decommission`, `decommissioned_at` в ответах, RBAC
   `decommission`, уточнение `httpFromGRPC` (`Aborted→409`, `FailedPrecondition→422`).
7. IDM/локалка: seed права `decommission@project:demo`; прогнать сквозной сценарий
   при включённом RBAC.
8. web: действие/диалог вывода из эксплуатации, zod, мутация.
9. README/инструкция; `GOWORK=off go mod tidy` по модулям; зелёный CI; merge;
   затем `/opsx:archive`.

Откат: миграция обратима (`goose down`); контрактные изменения аддитивны; выкладка
по сервисам независима (worker регистрирует workflow по имени — старые потоки не
затрагиваются).

## Open Questions

- Сервис-скоупные роли владельцев (вместо per-project), чтобы decommission мог
  корректно отзывать роль владельца через `RevokeRole`+`InvalidateSubject` без
  over-revoke. Сейчас в MVP роль per-project не отзывается (Решение 5).
- Поток restore (`DECOMMISSIONED→ACTIVE`): данные сохраняются для будущего
  восстановления, но сам поток вне scope этого change.
- Реальная проверка снятой нагрузки через K8s-worker (реализация интерфейса
  `LoadChecker` запросом к кластеру) — будущий change при появлении K8s-worker.
