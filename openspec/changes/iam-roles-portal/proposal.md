## Why

Сегодня RBAC (IDM) полностью «слепой» со стороны UI: роли, права и привязки
субъект↔роль существуют только в БД и сидируются миграциями, а портал умеет лишь
доменные операции над сервисами. Платформенному администратору негде увидеть, кто
какими ролями обладает, какие права несёт роль, и нельзя выдать/снять роль
субъекту иначе как ручной миграцией. См. docs/IDP_MVP_plan.md (Этап 3, RBAC/IAM) и
ADR-0003/0009/0010/0011.

Это ПЕРВЫЙ «горизонтальный» (не project-scoped) сценарий периметра: до сих пор все
маршруты были `/projects/{project}/services...`, а RBAC-helper `authorize` жёстко
формировал `resource="project:"+project`. IAM-админка вводит ресурс уровня всей
платформы и поэтому требует обобщения авторизации без регресса существующих
project-вызовов (create/list/read/owners/decommission/transfer).

Сама админка — привилегированный периметр: листинг ВСЕХ субъектов и ролей, а тем
более выдача/снятие ролей, нельзя отдавать без явного права. Поэтому change
ОБЯЗАН закрыть и обосновать (новый ADR-0014) модель полномочий админки, форму
читающего контракта и пагинацию, идемпотентность/коды assign/revoke на периметре и
безопасное обобщение gateway-helper `authorize`.

## What Changes

- **Контракт `proto/idm/v1` (аддитивно):** добавляется новый сервис
  `IamAdminService` с ЧИТАЮЩИМИ RPC: `ListRoles`, `ListPermissions`,
  `GetRolePermissions(role)`, `ListSubjectsWithRoles` (keyset-пагинация),
  `GetSubjectRoles(subject)`. Изменения чисто аддитивны (новый сервис + новые
  сообщения, существующие RPC/сообщения не трогаются → wire-совместимо).
  `RoleAdminService.AssignRole/RevokeRole` ПЕРЕИСПОЛЬЗУЮТСЯ для мутаций (не
  дублируются). Требует `buf generate` (`*.pb.go`); `gen:check` зелёный.
- **IDM repository/usecase (только чтение):** новые методы чтения каталога —
  список ролей, список всех прав, права конкретной роли, субъекты с их ролями
  (keyset по `subject`), роли конкретного субъекта. Перечисление субъектов —
  `DISTINCT subject` из `subject_roles` (субъекты без ролей системе неизвестны).
  Чтение БЕЗ побочных эффектов на кэш решений. Мутации (assign/revoke) остаются
  идемпотентными и ОБЯЗАТЕЛЬНО вызывают `InvalidateSubject` по затронутому
  субъекту (переиспользуется существующий `RoleManager`).
- **Авторизация админки (НОВОЕ RBAC-действие):** вводятся раздельные действия
  `read` и `write` на горизонтальном ресурсе `iam:global`. КАЖДАЯ IAM-ручка
  gateway вызывает `CheckAccess` ПЕРЕД проксированием (fail-closed → 403): GET-ручки
  требуют `(read, iam:global)`, assign/revoke — `(write, iam:global)`. Helper
  gateway обобщается до произвольного `resource` (`authorizeResource`); прежний
  `authorize(project, action)` становится тонкой обёрткой — без регресса
  project-вызовов.
- **Периметр (REST, ADR-0009) — горизонтальные ручки:**
  `GET /iam/roles`, `GET /iam/permissions`, `GET /iam/roles/{role}/permissions`,
  `GET /iam/subjects` (с ролями, keyset-пагинация),
  `GET /iam/subjects/{subject}/roles`,
  `POST /iam/subjects/{subject}/roles/{role}` (assign),
  `DELETE /iam/subjects/{subject}/roles/{role}` (revoke). Assign/revoke
  идемпотентны и возвращают `200` с актуальным набором ролей субъекта (повторный
  assign → 200; revoke отсутствующей связки → 200). Маппинг кодов как в
  ADR-0012/0013: deny→403, несуществующая роль→404, валидация→400. OpenAPI +
  TS-клиент регенерируются (`gen:check`).
- **Портал — новый раздел «Роли и доступы»** (новый маршрут react-router):
  read-only таблица ролей и их прав, список субъектов с ролями, форма
  назначить/снять роль субъекту (react-hook-form + zod, TanStack-мутация +
  invalidate). Обработка 403 (нет права на админку — раздел заблокирован/показан
  отказ), 404, 400; индикация результата; рантайм-валидация ответов zod `.parse`;
  без раскрытия сырых внутренних ошибок.
- **Локалка:** обратимая goose-миграция IDM засевает роль `iam-admin` с правами
  `(read, iam:global)` и `(write, iam:global)` и выдаёт её субъекту `demo-user`,
  чтобы раздел работал при включённом RBAC.
- **Документация:** README/инструкция — что такое раздел ролей, как назначить/снять
  роль, как устроена авторизация админки (read/write на `iam:global`), как
  проверить отказ (403)/успех, как инвалидируется кэш после изменения ролей.
- **ADR-0014:** модель полномочий IAM-админки (`read`/`write` на `iam:global`,
  точки `CheckAccess`), форма читающего контракта и пагинация, идемпотентность/коды
  assign/revoke, перечисление субъектов (`DISTINCT`), обобщение gateway `authorize`,
  границы scope (без динамического CRUD ролей/прав, без аудита).

## Capabilities

### New Capabilities
- `iam-administration`: административный домен IAM в IDM — чтение каталога ролей и
  прав, прав роли, субъектов с их ролями и ролей субъекта (read-only, без
  побочных эффектов на кэш, fail-closed при недоступности), модель полномочий
  админки (раздельные действия `read`/`write` на ресурсе `iam:global`,
  deny-by-default) и управление назначением ролей субъектам (переиспользование
  идемпотентных `AssignRole`/`RevokeRole` с обязательной `InvalidateSubject`).

### Modified Capabilities
- `service-contracts`: расширение `proto/idm/v1` новым сервисом `IamAdminService`
  с читающими RPC (аддитивно, wire-совместимо); регенерация Go/TS.
- `access-control`: новые действия `read`/`write` на горизонтальном ресурсе
  `iam:global`; обобщение gateway-helper `authorize` до произвольного `resource`;
  `CheckAccess` перед КАЖДОЙ IAM-ручкой (fail-closed); чтение без побочных
  эффектов на кэш, мутации — с `InvalidateSubject`.
- `perimeter-rest`: новые ГОРИЗОНТАЛЬНЫЕ ручки `/iam/*` (роли, права, субъекты,
  assign/revoke); идемпотентные assign/revoke → `200` с актуальным набором ролей;
  маппинг deny→403, роль не найдена→404, валидация→400.
- `portal-ui`: новый раздел «Роли и доступы» (таблицы ролей/прав/субъектов,
  форма assign/revoke, обработка 403/404/400, рантайм-валидация ответов).
- `local-environment`: seed роли `iam-admin` с правами `(read, iam:global)` и
  `(write, iam:global)` субъекту `demo-user` (обратимая миграция goose).

## Impact

- **Контракты/кодоген:** `proto/idm/v1` (новый `IamAdminService`), `pkg/api/idm/**`
  (`buf generate`), `openapi/openapi.yaml` (новые `/iam/*` пути), TS-клиент
  `web/src/api` (`gen:check`).
- **services/idm:** `repository` (методы чтения ролей/прав/назначений, keyset по
  subject), `usecase` (read-фасад без эффектов на кэш; reuse `RoleManager` для
  мутаций), `main.go` (новый gRPC `iamAdminServer`; reuse `roleAdminServer`).
- **services/gateway:** новый обработчик `iamAPI` (read + assign/revoke),
  gRPC-клиенты `IamAdminServiceClient` и `RoleAdminServiceClient`, обобщённый
  `authorizeResource`, маршруты `/iam/*`, маппинг кодов (reuse `httpFromGRPC`).
- **web:** новый маршрут/страница «Роли и доступы», карточки таблиц,
  форма assign/revoke (zod + react-hook-form + TanStack-мутация), обработка
  403/404; vitest-тесты (happy / 403 / назначение-снятие / валидация).
- **БД:** обратимая seed-миграция `services/idm/migrations` (роль `iam-admin`,
  права `read/write` на `iam:global`, привязка `demo-user`). Новых таблиц НЕТ
  (change read-only по модели; существующих таблиц достаточно).
- **Откат/компенсации:** не затрагивает провизию ресурсов (нет Saga/workflow);
  мутации ролей идемпотентны, кэш инвалидируется по субъекту.
- **Зависимости:** при новых общих зависимостях — `GOWORK=off go mod tidy` во
  всех затронутых модулях. NB: `services/gateway/gateway` — закоммиченный бинарь,
  после сборки не коммитить (`git checkout -- services/gateway/gateway`).
