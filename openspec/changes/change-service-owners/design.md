## Context

Третий сквозной сценарий MVP — «Изменение владельцев сервиса» (docs/IDP_MVP_plan.md,
Этап 3, строки ~153–160). Текущее состояние:

- Контракт `proto/projects/v1`: `Service = {project, name, status}`, RPC
  `GetService/ListServices/CreateService`. Владельцев и RPC смены владельцев нет.
- Каталог `services/projects`: таблица `services` (id/project/name/status/
  created_at/updated_at), guarded-CAS переходы статусов (ADR-0004), keyset-
  пагинация. Колонок/таблиц владельцев нет.
- Провижн: `services/projects/provisioning` (публичный контракт workflow,
  ADR-0008) + `CreateServiceWorkflow` (Saga GitLab→Harbor→Vault→inject→ACTIVE,
  ADR-0005); activities в `services/devinfra-worker` против моков GitLab/Vault/
  Harbor с SSRF-guard и RetryPolicy.
- IDM (`idm-rbac-min`): модель `roles/permissions/role_permissions/subject_roles`
  (strict-match, deny-by-default), gRPC `AccessService.CheckAccess` + кэш в
  DragonflyDB (TTL, singleflight, `InvalidateSubject`/инвалидация поколением).
  Программного пути выдачи/отзыва ролей нет; контракт IDM — только `CheckAccess`.
- gateway: `POST/GET/LIST /projects/{project}/services`, helper
  `authorize(w,r,project,action)`, `httpFromGRPC` (PermissionDenied→403,
  FailedPrecondition→409, NotFound→404).
- Портал: экраны список/форма/прогресс создания; UI владельцев нет.

Ограничения (обязательны): комментарии в коде — только на русском; fail-closed
RBAC (ADR-0003/0010); SSRF-guard на исходящих; guarded-CAS на изменениях
(ADR-0004); Saga с идемпотентными компенсациями (ADR-0005/0008); миграции goose
обратимые (`GOWORK=off`, пин `./tools`); go.work-монорепо (при новых общих
зависимостях — `GOWORK=off go mod tidy` во всех затронутых модулях).

## Goals / Non-Goals

**Goals:**

- Ввести модель владельцев сервиса в каталог (схема + контракт + чтение).
- Реализовать декларативную идемпотентную операцию смены владельцев с
  optimistic-concurrency (guarded-CAS по версии).
- Реализовать отдельный Temporal-workflow «Изменение владельцев» (Saga):
  GitLab/Vault (моки) → каталог → IDM-роли → инвалидация кэша IDM, с
  компенсациями до точки невозврата и алертом после неё.
- Дать IDM программный путь выдачи/отзыва ролей с инвалидацией кэша.
- Защитить операцию RBAC-действием `change_owners` (gateway + projects,
  defense-in-depth, fail-closed).
- Минимальный UI владельцев; локальный сквозной сценарий при включённом RBAC.

**Non-Goals:**

- «Удаление/decommission» и «Перенос сервиса» (отдельные последующие changes).
- Реальные (не мок) GitLab/Vault/Harbor; реальный OIDC/Keycloak realm.
- Богатая модель политик/иерархий/wildcard в IDM; групповые субъекты.
- Резолв логинов во внешнем каталоге пользователей (в MVP `owner` — строковый
  идентификатор субъекта, совместимый с `subject` JWT).

## Decisions

### Решение 1: Семантика контракта — декларативный полный набор (а не дельта)

`SetServiceOwners` принимает ПОЛНЫЙ желаемый набор владельцев; diff (add/remove)
вычисляется сервером против текущего состояния каталога. План прямо требует
«декларативный diff (add/remove), не императивно». Полный набор естественно
идемпотентен (повторный вызов тем же набором — no-op по составу) и устойчив к
повторной доставке/ретраям workflow. Альтернатива — дельта `add[]/remove[]` —
отклонена: она императивна, чувствительна к гонкам (повторное применение «add»
неидемпотентно без доппроверок) и сложнее в UI (нужно знать текущий состав, чтобы
строить дельту корректно).

Optimistic-concurrency: запрос несёт `expected_version` (= `owners_version` из
последнего чтения). Это защищает от потери конкурентных изменений и даёт
guarded-CAS-семантику на уровне контракта (конфликт → `FailedPrecondition`/409).

### Решение 2: Модель владельцев в Postgres

- Новая таблица `service_owners(service_id uuid REFERENCES services(id) ON DELETE
  CASCADE, owner text, PRIMARY KEY(service_id, owner))`.
- Новая колонка `services.owners_version int NOT NULL DEFAULT 0` — версия для
  optimistic-concurrency (отдельно от `status`, т.к. смена владельцев не меняет
  жизненный статус сервиса).

Альтернатива — `owners text[]` колонкой в `services` — отклонена: теряется
нормализация/уникальность на уровне БД и наблюдаемость по владельцу; отдельная
таблица проще расширяется (роли владельца и т.п.).

Замена набора — в одной транзакции (`withTx`): вычислить diff, `DELETE`
отозванных, `INSERT` добавленных, guarded-CAS инкремент версии. Чтение для
`List` — батч-загрузка владельцев для страницы (один доп. запрос `WHERE
service_id = ANY($ids)`), без N+1 и без слома keyset-пагинации.

### Решение 3: guarded-CAS по версии владельцев

```
UPDATE services
   SET owners_version = owners_version + 1, updated_at = now()
 WHERE id = $id AND owners_version = $expected;
-- RowsAffected == 0 → errs.ErrConflict
```

CAS выполняется внутри той же транзакции, что и правки `service_owners`; при
конфликте — rollback, наружу `ErrConflict`. Соответствует ADR-0004 (не
check-then-act). Запись владельцев в каталог из workflow — отдельной activity
(`CatalogSetOwners`), по образцу `CatalogTransitionActive`.

### Решение 4: Контракт IDM — управляющие RPC ролей + инвалидация кэша

Добавляем в `proto/idm/v1` управляющие RPC `AssignRole(subject, role)` /
`RevokeRole(subject, role)` (идемпотентные; уникальность `(subject, role_id)` на
БД). После изменения привязок IDM вызывает существующий примитив
`InvalidateSubject(subject)` (или инвалидацию поколением). Несуществующая роль →
`NotFound`. Альтернатива — складывать роли напрямую в БД из projects/worker —
отклонена: нарушает границу владения данными IDM и его кэшем (кэш в Dragonfly
знает только IDM; внешняя запись оставит устаревшие решения).

Роль владельца — per-project: `owner:project:<project>` с правами на ресурс
`project:<project>` (как минимум `read`, `list`, `change_owners`). Выдача
владельца = `AssignRole(subject, owner:project:<project>)`. В MVP роль и её
права сидируются для `project:demo`; динамическое создание ролей под новые
проекты — вне scope (роль должна существовать → иначе `NotFound`).

### Решение 5: Авторизация — действие `change_owners`, две точки CheckAccess

- gateway: `authorize(w, r, project, "change_owners")` перед проксированием
  (helper уже есть; нужно лишь передать новое действие).
- projects: обобщить `authorize(ctx, project)` до `authorize(ctx, project,
  action)` и вызывать с `"change_owners"` перед доменной операцией
  (defense-in-depth).

Fail-closed: отказ/недоступность/ошибка IDM → `PermissionDenied`/403 без
побочных эффектов и без раскрытия деталей (ADR-0003/0010). `subject` — из claims.

### Решение 6: Дизайн workflow «Изменение владельцев» (Saga)

Новый публичный пакет `services/projects/changeowners` (по образцу
`provisioning`, ADR-0008): имена workflow/activities, детерминированный
`WorkflowID = "change-owners:<project>:<name>"` (идемпотентность повторного
запуска; `WorkflowIDReusePolicy` как у провижна). Тело детерминировано, I/O — в
activities; единые `ActivityOptions` (StartToClose 30s, Heartbeat 15s,
RetryPolicy: 1s base, x2, max 10s, 5 попыток) — как в провижне.

Вход workflow: `{ServiceID, Project, Name, Desired []string, Previous []string,
ExpectedVersion int}`. Diff (`add`, `remove`) вычисляется детерминированно
внутри workflow из `Desired`/`Previous` (или передаётся стартером). Пустой diff →
немедленный no-op (без обращения к интеграциям).

Шаги и точка невозврата:

1. `GitLabSyncMembers(add, remove)` → comp `GitLabRestoreMembers(previous)`.
2. `VaultSyncOwners(add, remove)` → comp `VaultRestoreOwners(previous)`.
3. `CatalogSetOwners(serviceID, desired, expectedVersion)` — guarded-CAS.
   **ТОЧКА НЕВОЗВРАТА** (commit owners в каталоге).
4. `IDMSyncOwnerRoles(add, remove, role)` — Assign/Revoke + InvalidateSubject.
5. (Инвалидация кэша входит в шаг 4; при необходимости отдельная
   `IDMInvalidateSubjects`.)

Политика отказов:

- Сбой шагов 1–2 (non-retryable после RetryPolicy) → компенсации в обратном
  порядке (идемпотентны), owners не трогаем, workflow → ошибка.
- Конфликт guarded-CAS на шаге 3 (`ErrConflict`, non-retryable) → компенсации
  1–2, workflow → ошибка конфликта (наружу 409).
- Сбой шага 4 ПОСЛЕ точки невозврата → НЕ откатываем GitLab/Vault/каталог
  молча. Ретраим идемпотентный шаг IDM; при исчерпании — алерт оператору
  структурным логом (ADR-0005/0008), каталог остаётся источником правды.

Sequence (happy-path и ветка компенсации):

```
projects(API)        Temporal           DevInfra worker            GitLab/Vault/Catalog/IDM
     |  SetServiceOwners   |                    |                            |
     |-- CheckAccess(IDM) ->| (change_owners, fail-closed)                   |
     |-- StartWorkflow ---->|                    |                            |
     |   (WorkflowID=change-owners:p:n)          |                            |
     |                      |-- Task ----------->|                            |
     |                      |                    |-- GitLabSyncMembers ------>| (моки, SSRF-guard)
     |                      |                    |<-- ok ---------------------|
     |                      |                    |-- VaultSyncOwners -------->|
     |                      |                    |<-- ok ---------------------|
     |                      |                    |-- CatalogSetOwners(CAS) -->| (guarded-CAS)
     |                      |                    |<-- ok (ТОЧКА НЕВОЗВРАТА) --|
     |                      |                    |-- IDMSyncOwnerRoles ------>| Assign/Revoke+InvalidateSubject
     |                      |                    |<-- ok ---------------------|
     |                      |<-- complete -------|                            |

  Ветка компенсации (сбой Vault до точки невозврата):
     |                      |                    |-- VaultSyncOwners --(fatal)|
     |                      |                    |-- (comp) GitLabRestore --->| (идемпотентно)
     |                      |<-- fail -----------|                            |  owners НЕ изменены
```

### Решение 7: Периметр (REST, ADR-0009)

`PUT /projects/{project}/services/{name}/owners`, тело
`{owners:[...], owners_version:N}`. PUT выбран как идемпотентная замена ресурса
«набор владельцев» целиком (согласуется с декларативной семантикой). Маппинг
gRPC→HTTP уже покрыт `httpFromGRPC`: `FailedPrecondition→409` (конфликт версии),
`NotFound→404`, `InvalidArgument→400`, `PermissionDenied→403`. `owners`/
`owners_version` добавляются в ответы `GET`/`LIST`; OpenAPI + TS-клиент
регенерируются (`gen:check`).

### Решение 8: Маппинг ошибок (сводно)

| Ситуация | projects (gRPC) | gateway (HTTP) |
|---|---|---|
| Нет права / IDM недоступен | `PermissionDenied` | 403 |
| Сервис не найден | `NotFound` | 404 |
| Конфликт версии (CAS) | `FailedPrecondition` | 409 |
| Невалидный запрос | `InvalidArgument` | 400 |
| Внутренняя ошибка | `Internal` (без деталей) | 500 |

## Risks / Trade-offs

- [BREAKING контракта projects/idm] → Изменения аддитивны по номерам полей;
  новый RPC помечен BREAKING в комментарии; регенерация Go и TS в одном change,
  `gen:check` в CI ловит дрейф.
- [Несогласованность после точки невозврата: каталог обновлён, IDM-роли — нет]
  → Идемпотентный ретрай шага 4; при исчерпании — алерт оператору (не тихий
  откат). Каталог = источник правды; повторный запуск workflow с тем же
  `WorkflowID` довыполнит IDM-синк идемпотентно.
- [Устаревшие allow/deny в кэше IDM] → Обязательная `InvalidateSubject` по всем
  затронутым (add ∪ remove) субъектам внутри activity IDM-синка; покрыто
  спекой и тестом на miniredis/стабе.
- [Гонки конкурентных смен владельцев] → guarded-CAS по `owners_version`;
  проигравший получает 409 и перечитывает состояние.
- [Роль `owner:project:<project>` не существует для нового проекта] → В MVP
  сидируется для `project:demo`; для прочих — `NotFound` (явная ошибка, не
  тихое игнорирование). Динамическое создание ролей — вне scope (Open Question).
- [SSRF/утечка секретов в моках GitLab/Vault] → SSRF-guard на исходящих
  (как в провижне); секреты не логируются.
- [go.work tidy-drift при новых зависимостях] → `GOWORK=off go mod tidy` во всех
  затронутых модулях перед PR (tidy-check/govulncheck).

## Migration Plan

1. Ветка `change/change-service-owners` от master.
2. Контракты: расширить `proto/projects/v1` и `proto/idm/v1`, `buf generate`,
   регенерировать TS-клиент; зафиксировать `gen:check`.
3. БД: обратимая миграция `services/projects/migrations` (таблица
   `service_owners` + колонка `owners_version`); проверить `up`/`down`.
4. projects: repository (модель/CAS/чтение owners), usecase, grpcapi
   (`SetServiceOwners`, обобщённый `authorize`), пакет `changeowners` (workflow +
   starter).
5. devinfra-worker: моки/activities GitLab/Vault владельцев + компенсации,
   `CatalogSetOwners`, `IDMSyncOwnerRoles`; регистрация.
6. IDM: управляющие RPC ролей + инвалидация кэша; миграция/seed роли владельца.
7. gateway: маршрут `PUT .../owners`, owners в ответах, RBAC `change_owners`.
8. web: UI/форма владельцев, zod, мутации.
9. Локалка: расширить seed; прогнать сквозной сценарий при включённом RBAC.
10. README/инструкция; `GOWORK=off go mod tidy` по модулям; зелёный CI; merge;
    затем `/opsx:archive`.

Откат: миграция обратима (`goose down`); контрактные изменения аддитивны;
выкладка по сервисам независима (worker регистрирует workflow по имени —
старые потоки не затрагиваются).

## Open Questions

- Динамическое создание роли `owner:project:<project>` для произвольных проектов
  (сейчас сидируется только `project:demo`). Закрыть в последующем change или
  расширением seed/админ-пути IDM.
- Нужен ли отдельный жизненный статус сервиса на время смены владельцев
  (`changing_owners`) для наблюдаемости в UI, или достаточно версии и логов
  workflow. Текущее решение — без отдельного статуса (смена владельцев не меняет
  жизненный цикл сервиса).
