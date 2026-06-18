## Why

Фундамент (`foundation-and-pkg`) дал каркас сервиса `projects`: gRPC-сервер, Temporal lazy-клиент и health, но без персистентности — `GetService` возвращает `Unimplemented`, Postgres не подключён, схемы нет. Прежде чем строить Temporal-workflow «Создание сервиса» (Этап 2–3), нужен доменный слой каталога: надёжное хранилище единиц-сервисов и атомарные переходы статусов. Это первый доменный change, он закрывает БЛОК 4 (База данных) и часть Этапа 1 (Слои сервиса проектов) из docs/IDP_MVP_plan.md и материализует ADR-0004 (guarded-CAS).

## What Changes

- Добавляется Postgres-схема каталога: таблица `services` (`id`, `project`, `name`, `status`, `created_at`, `updated_at`) с уникальностью `(project, name)`; инструмент миграций пинуется в `./tools` (`GOWORK=off`).
- Реализуется слой `repository` поверх `pkg/db`: переходы статусов — через **guarded-CAS** (`UPDATE ... WHERE id=$id AND status=$expected`, `RowsAffected==0 → errs.ErrConflict`), не check-then-act (ADR-0004). Многошаговые записи — через `withTx` с узким интерфейсом `dbConn` для `*pgxpool.Pool` и `pgx.Tx`; публикация статусов/событий — после commit.
- Добавляется keyset-пагинация по `(created_at, id)` с непрозрачным base64-курсором.
- Реализуется `usecase`-слой и доменная реализация gRPC `ProjectsService.GetService` (чтение из каталога, 404 → `codes.NotFound`); добавляется `ListServices` с keyset-пагинацией; добавляется `CreateServiceRecord` (вставка со `status=CREATING`) **без** запуска Temporal workflow.
- `/readyz` сервиса `projects` начинает реально пинговать Postgres (через `pkg/httpserver.ReadinessCheck`).
- **BREAKING**: в `proto/projects/v1/projects.proto` добавляется RPC `ListServices` (+ request/response сообщения с полями курсора); изменение wire-контракта помечается BREAKING, стабы регенерируются через `make proto`.
- Тесты: table-driven + `t.Parallel()`; обязательно покрыт guarded-CAS-конфликт (`RowsAffected==0 → ErrConflict`), переходы статусов и keyset-пагинация. Стаб/in-memory — в дефолтном прогоне, реальная БД — под тегом `integration`.

План отката/компенсаций: change не провизионит внешние ресурсы (GitLab/Vault/Harbor) и не запускает workflow — Saga-компенсации не требуются. Откат на уровне БД обеспечивается транзакциями (`withTx`) и обратимостью миграций (down-скрипты инструмента миграций).

## Capabilities

### New Capabilities
- `service-catalog`: персистентный каталог единиц-сервисов в Postgres — схема и миграции, repository с guarded-CAS-переходами статусов и транзакциями, keyset-пагинация, usecase-слой и доменная реализация gRPC-чтения (`GetService`/`ListServices`/`CreateServiceRecord`), content-aware `/readyz` с пингом Postgres.

### Modified Capabilities
- `service-contracts`: в gRPC-контракт `ProjectsService` добавляется RPC `ListServices` (keyset-пагинация по курсору); `GetService` перестаёт быть только каркасом и обретает доменную семантику (NotFound при отсутствии). Изменение `.proto` — **BREAKING**.

## Impact

- **Код**: `services/projects` (новые слои `repository`/`usecase`, подключение Postgres, реализация gRPC, реальный `/readyz`); `proto/projects/v1/projects.proto` + регенерация стабов в `pkg/api/projects/v1`; `services/projects/go.mod` (`replace ../../pkg`).
- **Инструменты**: `./tools` — новый пин инструмента миграций (goose или golang-migrate, выбор в design/ADR), `GOWORK=off`; Makefile-target для миграций.
- **БД**: Postgres `postgres-projects` (DSN `postgres://projects:projects@postgres-projects:5432/projects?sslmode=disable`), новая таблица `services`.
- **CI**: задействуется существующий `integration`-джоб для тестов с реальной БД.
- **ADR**: возможен новый ADR на выбор инструмента миграций; опирается на ADR-0004.
- **Вне scope**: Temporal workflows/activities «Создание сервиса» (`create-service-workflow`), реальный RBAC IDM (`idm-rbac-min`), user stories владельцев/переноса/удаления.
