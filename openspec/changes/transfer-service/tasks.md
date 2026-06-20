# Tasks — transfer-service

> Все комментарии в коде (Go, TS/TSX, конфиги, миграции) — ТОЛЬКО на русском.
> Инструменты пинованы в `./tools` (buf/goose/golangci-lint/govulncheck), запуск
> `GOWORK=off`. `services/gateway/gateway` — закоммиченный бинарь; после сборки
> `git checkout -- services/gateway/gateway`.

## 0. Подготовка

- [x] 0.1 Сверка с docs/IDP_MVP_plan.md (Этап 3, «Перенос сервиса») и затронутыми
      ADR (0003/0004/0005/0008/0009/0010/0011/0012) и новым ADR-0013; согласовать
      решения design.md.
- [x] 0.2 Создать ветку `change/transfer-service` от `master` (прямые коммиты в
      master запрещены).

## 1. Контракты (proto/projects/v1) и кодоген

- [x] 1.1 Добавить в `proto/projects/v1` RPC `TransferService(project, name,
      target_project) → Service` (комментарии на русском; пометить добавление RPC
      как BREAKING; зафиксировать идемпотентность).
- [x] 1.2 Добавить аддитивное значение enum `SERVICE_STATUS_TRANSFERRING = 5` (не
      менять существующие номера/смыслы).
- [x] 1.3 `buf generate` → `pkg/api/projects/v1` (`*.pb.go`); регенерировать
      TS-клиент `web`; `GOWORK=off go mod tidy` в затронутых модулях; `gen:check`
      зелёный.

## 2. Каталог (services/projects) — смена project

- [x] 2.1 Обратимая миграция goose `services/projects/migrations`
      (`NNNN_add_transferring_status`): расширить `CHECK status IN (...)` значением
      `transferring`; проверить `up`/`down`.
- [x] 2.2 repository: `CatalogBeginTransfer` — в транзакции проверка свободы
      `(target, name)` (занято → `ErrConflict`) + guarded-CAS `active→transferring`;
      разбор `RowsAffected==0` (`transferring`→идёт, `creating`/`failed`/
      `decommissioned`→`ErrPrecondition`, конкурентная смена→`ErrConflict`);
      `ErrNotFound`.
- [x] 2.3 repository: `CatalogCommitTransfer` — guarded-CAS `transferring→active`
      + `project=target` (повторная проверка свободы); `RowsAffected==0`→
      `ErrConflict`; идемпотентность (повтор на `target/active` → no-op).
      `CatalogAbortTransfer` — guarded-CAS `transferring→active`.
- [x] 2.4 repository/usecase: id записи и владельцы (`service_owners`, FK
      `service_id`) сохраняются; `decommissioned_at` остаётся `NULL`; `transferring`
      корректно читается в `Get`/`List` без слома keyset-пагинации.
- [x] 2.5 grpcapi `TransferService`: валидация (`target_project` обязателен,
      `!= project`) → двойной `authorize(ctx, source, "transfer")` +
      `authorize(ctx, target, "transfer_in")` (fail-closed) → чтение статуса
      (NotFound / no-op для уже перенесённого / FailedPrecondition для
      не-`active` / Aborted для занятого имени) → старт workflow
      `WorkflowID="transfer:<source>:<name>"`. Маппинг `ErrConflict→Aborted`,
      `ErrPrecondition→FailedPrecondition`, `ErrNotFound→NotFound`,
      `ErrValidation→InvalidArgument`; без раскрытия деталей (slog `err`).
- [x] 2.6 Тесты: table-driven + `t.Parallel()`; перенос на стабе/in-memory (две
      фазы guarded-CAS, идемпотентность, предусловие, конфликт занятого имени,
      NotFound) в дефолте; pgx под тегом `integration` (DSN из `PROJECTS_TEST_DSN`,
      `Skip` без БД); `goleak`.

## 3. Workflow «Перенос» (services/projects/transfer)

- [x] 3.1 Публичный контракт пакета `transfer`: имена workflow/activities,
      детерминированный `WorkflowID`, единые `ActivityOptions` (как
      decommission/provisioning).
- [x] 3.2 Тело workflow (детерминированное): шаг 0 `CatalogBeginTransfer` → 1
      `GitLabTransferRepo` (ТОЧКА НЕВОЗВРАТА) → 2 `VaultMigratePaths` → 3
      `HarborUpdateMetadata` → 4 `CatalogCommitTransfer` → 5 `TransferOwnerRoles`.
      Компенсация `CatalogAbortTransfer` до точки невозврата; форвард-only + алерт
      оператору (slog `err`) после.
- [x] 3.3 Starter в grpcapi (шаг 2.5) с `WorkflowIDReusePolicy` как у провижна.
- [x] 3.4 Temporal testsuite (замоканные activities): happy-path, занятое имя в
      target (отказ до side-effect), компенсация (сбой до точки невозврата), алерт
      (сбой после), конфликт guarded-CAS фиксации; `goleak`.

## 4. DevInfra worker (services/devinfra-worker)

- [x] 4.1 Интеграции (interface + Memory + HTTP, SSRF-guard): GitLab
      `TransferRepo(source→target)`; Vault `MigratePaths` (копия секретов + новые
      политики + очистка старых); Harbor `UpdateMetadata` (под target). Секреты не
      логировать.
- [x] 4.2 activities: `GitLabTransferRepo`, `VaultMigratePaths`,
      `HarborUpdateMetadata`, `CatalogBeginTransfer`/`CatalogCommitTransfer`/
      `CatalogAbortTransfer` (обёртки repository, `ErrConflict`),
      `TransferOwnerRoles` (IDM revoke source + assign target + `InvalidateSubject`).
- [x] 4.3 Регистрация workflow/activities в worker; конфиг (адреса GitLab/Harbor/
      Vault; IDM_GRPC_ADDR для переноса ролей).
- [x] 4.4 Тесты activities на моках (идемпотентность, SSRF-guard, перенос ролей);
      `goleak` где есть горутины.

## 5. IDM / локалка (services/idm)

- [x] 5.1 Обратимая миграция goose `services/idm/migrations`
      (`NNNN_seed_transfer_demo`): роль `owner:project:demo2` и проект
      `project:demo2`; права `(transfer, project:demo)` и
      `(transfer_in, project:demo2)` → `demo-user`; проверить `up`/`down`.
- [x] 5.2 Тест (стаб IDM/in-memory): `CheckAccess(transfer, project:demo)` и
      `CheckAccess(transfer_in, project:demo2)` allow с грантами, deny-by-default
      без; перенос ролей (revoke source + assign target) + инвалидация.

## 6. Gateway (services/gateway)

- [x] 6.1 Маршрут `POST /projects/{project}/services/{name}/transfer` → двойной
      `authorize(w, r, source, "transfer")` + `authorize(w, r, target,
      "transfer_in")` (fail-closed) → проксирование в projects; тело
      `{target_project}`.
- [x] 6.2 Маппинг кодов: переиспользовать `Aborted→409`, `AlreadyExists→409`,
      `FailedPrecondition→422`, `NotFound→404`, `InvalidArgument→400`,
      `PermissionDenied→403` (из ADR-0012, новых правил не нужно); не раскрывать
      детали.
- [x] 6.3 Тесты (стаб IDM, без сети): deny на source→403, deny на target→403, IDM
      недоступен→403, занятое имя→409, предусловие→422, `transferring` в ответах;
      `goleak`. После сборки — `git checkout -- services/gateway/gateway`.

## 7. Периметр OpenAPI + web

- [x] 7.1 Описать `POST .../transfer` (тело `{target_project}`) и статус
      `transferring` в OpenAPI периметра; `npm run gen`; `gen:check` зелёный.
- [x] 7.2 web: действие «Перенести сервис» (по образцу `DecommissionCard`):
      подтверждение вводом имени, ЯВНОЕ предупреждение о необратимости/рисках,
      выбор target-проекта, zod-схемы, TanStack-мутация; блокировка для
      не-`active`.
- [x] 7.3 web: `StatusBadge` умеет `transferring`; индикация в списке/на странице
      сервиса.
- [x] 7.4 web: vitest — happy / 403 (source/target) / 409 (занятое имя) / 422
      (предусловие) / блокировка недопустимого статуса.

## 8. Локалка и документация

- [x] 8.1 compose: убедиться, что `migrate-projects`/`migrate-idm` применяют новые
      миграции; devinfra-worker сконфигурирован (адреса + IDM_GRPC_ADDR); прогнать
      сквозной перенос `demo→demo2` при включённом RBAC.
- [x] 8.2 README projects + инструкция: что такое перенос, что происходит с
      репозиторием/секретами/ролями/доступами, риски и точка невозврата, как
      проверить отказ (403 source/target)/предусловие (422)/конфликт (409).
- [x] 8.3 Опубликовать ADR-0013 как
      `docs/adr/0013-transfer-semantics-ponr-and-dual-authorization.md` (вне
      openspec/).

## 9. Проверка перед PR

- [x] 9.1 `GOWORK=off go mod tidy` во всех затронутых модулях (tidy-check/
      govulncheck).
- [x] 9.2 Тесты модулей, `golangci-lint` (errname/paralleltest), `govulncheck`,
      `gen:check` (proto+OpenAPI+TS), integration — зелёные.
- [ ] 9.3 PR в `master`; после merge с зелёным CI — `/opsx:archive` (только после
      merge через отдельный PR sync+archive по образцу).
