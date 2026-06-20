# Tasks — decommission-service

> Все комментарии в коде (Go, TS/TSX, конфиги, миграции) — ТОЛЬКО на русском.
> Инструменты пинованы в `./tools` (buf/goose/golangci-lint/govulncheck), запуск
> `GOWORK=off`. `services/gateway/gateway` — закоммиченный бинарь; после сборки
> `git checkout -- services/gateway/gateway`.

## 0. Подготовка

- [x] 0.1 Сверка с docs/IDP_MVP_plan.md (Этап 3, «Удаление сервиса») и затронутыми
      ADR (0003/0004/0005/0008/0009/0010/0011) и новым ADR-0012; согласовать
      решения design.md.
- [x] 0.2 Создать ветку `change/decommission-service` от `master` (прямые коммиты
      в master запрещены).

## 1. Контракты (proto/projects/v1) и кодоген

- [x] 1.1 Добавить в `proto/projects/v1` RPC `DecommissionService(project, name,
      load_drained) → Service` (комментарии на русском; пометить добавление RPC
      как BREAKING).
- [x] 1.2 Добавить аддитивное поле `decommissioned_at` в `Service` и ответы чтения
      (новый номер поля; не менять существующие теги `owners`/`owners_version`).
- [x] 1.3 `buf generate` → `pkg/api/projects/v1` (`*.pb.go`); регенерировать
      TS-клиент `web`; `GOWORK=off go mod tidy` в затронутых модулях; `gen:check`
      зелёный.

## 2. Каталог (services/projects) — soft-delete

- [x] 2.1 Обратимая миграция goose `services/projects/migrations`
      (`NNNN_add_decommissioned_at`): nullable `decommissioned_at timestamptz` на
      `services`; проверить `up`/`down`.
- [x] 2.2 repository: операция soft-delete guarded-CAS `ACTIVE→DECOMMISSIONED` +
      `decommissioned_at`; разбор `RowsAffected==0` (decommissioned→no-op,
      creating/failed→`ErrPrecondition`, конкурентная смена→`ErrConflict`);
      `ErrNotFound`; ввести sentinel `errs.ErrPrecondition` (если ещё нет).
- [x] 2.3 repository/usecase: данные (строка + владельцы) НЕ удаляются; отражение
      `decommissioned_at` в `Get`/`List` без слома keyset-пагинации.
- [x] 2.4 grpcapi `DecommissionService`: валидация → `authorize(ctx, project,
      "decommission")` → чтение статуса (NotFound / no-op для decommissioned /
      FailedPrecondition для creating|failed) → проверка `load_drained` → старт
      workflow `WorkflowID="decommission:<project>:<name>"`. Маппинг
      `ErrConflict→Aborted`, `ErrPrecondition→FailedPrecondition`,
      `ErrNotFound→NotFound`; без раскрытия деталей (slog `err`).
- [x] 2.5 Перевести существующий путь владельцев (`change-owners`) на
      `ErrConflict→Aborted` (итоговый HTTP 409 сохраняется); обновить тесты.
- [x] 2.6 Тесты: table-driven + `t.Parallel()`; soft-delete на стабе/in-memory
      (guarded-CAS, идемпотентность, предусловие, конфликт, NotFound) в дефолте;
      pgx под тегом `integration` (DSN из `*_TEST_DSN`, `Skip` без БД); `goleak`.

## 3. Workflow «Вывод из эксплуатации» (services/projects/decommission)

- [x] 3.1 Публичный контракт пакета `decommission`: имена workflow/activities,
      детерминированный `WorkflowID`, единые `ActivityOptions` (как provisioning/
      changeowners); интерфейс `LoadChecker`.
- [x] 3.2 Тело workflow (детерминированное): шаг 0 `EnsureLoadDrained` → 1
      `GitLabArchive`+revoke → 2 `HarborSetReadOnly`+robot → 3 `VaultRevokeSecretID`
      (ТОЧКА НЕВОЗВРАТА) → 4 `CatalogDecommission` (guarded-CAS). Компенсации до
      точки невозврата (Harbor→writable, GitLab→unarchive); форвард-only + алерт
      после.
- [x] 3.3 Starter в grpcapi (шаг 2.4) с `WorkflowIDReusePolicy` как у провижна.
- [x] 3.4 Temporal testsuite (замоканные activities): happy-path, предусловие
      «нагрузка не снята» (отказ до побочных эффектов), компенсация (сбой до точки
      невозврата), алерт (сбой после), конфликт guarded-CAS; `goleak`.

## 4. DevInfra worker (services/devinfra-worker)

- [x] 4.1 Интеграции (interface + Memory + HTTP, SSRF-guard): GitLab
      `Archive`/`Unarchive`+revoke/restore members; Harbor `SetReadOnly`/
      `SetWritable`+robot revoke; Vault `RevokeSecretID` (необратимо). Секреты не
      логировать.
- [x] 4.2 activities: `EnsureLoadDrained` (`LoadChecker`, MVP — проверка
      `load_drained`), GitLab/Harbor/Vault обратные операции + компенсации,
      `CatalogDecommission` (обёртка repository, `ErrConflict`, `decommissioned_at`).
- [x] 4.3 Регистрация workflow/activities в worker; конфиг (адреса GitLab/Harbor/
      Vault; при необходимости IDM_GRPC_ADDR).
- [x] 4.4 Тесты activities на моках (идемпотентность, SSRF-guard,
      `EnsureLoadDrained` happy/fail); `goleak` где есть горутины.

## 5. IDM / локалка (services/idm)

- [x] 5.1 Обратимая миграция goose `services/idm/migrations`
      (`NNNN_seed_decommission_demo`): право `(decommission, project:demo)` →
      `demo-user`; проверить `up`/`down`.
- [x] 5.2 Тест (стаб IDM/in-memory): `CheckAccess(decommission, project:demo)`
      deny-by-default без гранта, allow с грантом; инвалидация кэша учитывает новое
      действие.

## 6. Gateway (services/gateway)

- [x] 6.1 Маршрут `POST /projects/{project}/services/{name}/decommission` →
      `authorize(w, r, project, "decommission")` (fail-closed) → проксирование в
      projects; тело `{load_drained}`.
- [x] 6.2 `httpFromGRPC`: добавить `Aborted→409`, уточнить `FailedPrecondition→422`
      (сохранить `NotFound→404`, `InvalidArgument→400`, `PermissionDenied→403`,
      `AlreadyExists→409`); не раскрывать детали.
- [x] 6.3 `decommissioned_at` в ответах `GET`/`LIST`.
- [x] 6.4 Тесты (стаб IDM, без сети): deny→403, IDM недоступен→403, конфликт→409,
      предусловие→422, статус в ответах; `goleak`. После сборки —
      `git checkout -- services/gateway/gateway`.

## 7. Периметр OpenAPI + web

- [x] 7.1 Описать `POST .../decommission` и `decommissioned_at` в OpenAPI периметра;
      `npm run gen`; `gen:check` зелёный.
- [x] 7.2 web: действие «Вывести из эксплуатации» с подтверждением (ввод имени,
      явно decommission≠purge, `load_drained`), zod-схемы, TanStack-мутация;
      блокировка для decommissioned/creating/failed.
- [x] 7.3 web: индикация статуса `decommissioned` (StatusBadge) + `decommissioned_at`
      в списке/на странице сервиса.
- [x] 7.4 web: vitest — happy / 403 / 409 / 422 (предусловие) / блокировка
      повторного действия.

## 8. Локалка и документация

- [x] 8.1 compose: убедиться, что `migrate-projects`/`migrate-idm` применяют новые
      миграции; devinfra-worker сконфигурирован; прогнать сквозной сценарий вывода
      из эксплуатации при включённом RBAC.
- [x] 8.2 README projects + инструкция: что такое decommission (soft-delete), как
      вывести сервис, что происходит с доступами/ролями, как закрыт вопрос проверки
      снятой нагрузки K8s, как проверить отказ/предусловие/разрешение.
- [x] 8.3 Опубликовать ADR-0012 как
      `docs/adr/0012-decommission-semantics-and-k8s-load-check.md` (вне openspec/).

## 9. Проверка перед PR

- [x] 9.1 `GOWORK=off go mod tidy` во всех затронутых модулях (tidy-check/
      govulncheck).
- [x] 9.2 Тесты модулей, `golangci-lint` (errname/paralleltest), `govulncheck`,
      `gen:check` (proto+OpenAPI+TS), integration — зелёные.
- [ ] 9.3 PR в `master`; после merge с зелёным CI — `/opsx:archive` (только после
      merge).
