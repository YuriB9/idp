## 1. Подготовка и сверка

- [x] 1.1 Сверить scope с docs/IDP_MVP_plan.md (Этап 1 «Слои сервиса проектов», БЛОК 4 «База данных») и с ADR-0004 (guarded-CAS), ADR-0002 (gRPC), ADR-0006 (go.work), ADR-0007 (goose)
- [x] 1.2 Создать ветку `change/projects-catalog` от master (прямые коммиты в master запрещены); все последующие изменения — в ней
- [x] 1.3 Проверить доступность `postgres-projects` через docker-compose (DSN `postgres://projects:projects@postgres-projects:5432/projects?sslmode=disable`)

## 2. Инструмент миграций (./tools, GOWORK=off)

- [x] 2.1 Запинить goose (`github.com/pressly/goose/v3`) в `tools/go.mod` как tool-зависимость; запустить `go mod tidy` с `GOWORK=off`
- [x] 2.2 Добавить Makefile-таргет `migrate-projects` (применение миграций из `services/projects/migrations` с DSN из окружения)
- [x] 2.3 Зафиксировать выбор инструмента в ADR-0007 (файл `docs/adr/0007-migration-tool-goose.md` уже создан — проверить актуальность)

## 3. Схема БД

- [x] 3.1 Миграция `0001_create_services` (`-- +goose Up/Down`): таблица `services` (`id`, `project`, `name`, `status`, `created_at`, `updated_at`), все комментарии на русском
- [x] 3.2 Уникальный индекс по `(project, name)`; составной индекс `(created_at, id)` для keyset-пагинации; `CHECK` по допустимым значениям `status`
- [x] 3.3 Обратимый down-шаг (drop таблицы/индексов); проверить идемпотентность повторного `goose up`

## 4. Контракт gRPC (.proto, BREAKING)

- [x] 4.1 Добавить в `proto/projects/v1/projects.proto` rpc `ListServices` и сообщения `ListServicesRequest{project,page_size,page_token}` / `ListServicesResponse{services[],next_page_token}` / `Service{project,name,status}`; комментарии на русском; пометить изменение **BREAKING**
- [x] 4.2 Регенерировать стабы `make proto` (buf через `./tools`); закоммитить сгенерированное в `pkg/api/projects/v1`; убедиться, что `git diff --exit-code` после повторной генерации пуст

## 5. Repository (services/projects/internal/repository)

- [x] 5.1 Узкий интерфейс `dbConn` (Exec/Query/QueryRow), удовлетворяемый `*pgxpool.Pool` и `pgx.Tx`; модель `Service` и тип `Status` со строгим маппингом в/из proto-enum (без молчаливого дефолта)
- [x] 5.2 `withTx(ctx, pool, fn)` — begin/commit/rollback с defer-rollback и игнором `pgx.ErrTxClosed`
- [x] 5.3 `CreateServiceRecord` — вставка со `status=CREATING`; нарушение уникальности `(project, name)` → `errs.ErrConflict`
- [x] 5.4 `GetService(project, name)` — чтение; отсутствие → `errs.ErrNotFound`
- [x] 5.5 `TransitionStatus(ctx, db, id, expected, next)` — guarded-CAS `UPDATE ... WHERE id=$id AND status=$expected`; `RowsAffected==0 → errs.ErrConflict` (ADR-0004)
- [x] 5.6 `ListServices` — keyset-пагинация по `(created_at, id)`, `LIMIT` с клампом page_size; кодек курсора base64(JSON); невалидный курсор → `errs.ErrValidation`
- [x] 5.7 Хук публикации статуса/события — вызывается вызывающим кодом только после commit (в этом change — заглушка-лог)

## 6. Usecase (services/projects/internal/usecase)

- [x] 6.1 Usecase-операции `Get`, `List`, `CreateRecord` поверх repository (CreateRecord — без запуска Temporal workflow)
- [x] 6.2 Маппинг ошибок usecase→gRPC: `ErrNotFound→NotFound`, `ErrConflict→FailedPrecondition/Aborted`, `ErrValidation→InvalidArgument`, прочее→`Internal` без отдачи `err.Error()` клиенту

## 7. gRPC API и подключение БД (services/projects)

- [x] 7.1 Реализовать `ProjectsServiceServer` (`internal/grpcapi`) поверх usecase: `GetService` (404→`codes.NotFound`) и `ListServices` (keyset)
- [x] 7.2 В `main.go` создать пул через `pkg/db.NewPool` (обязательный PoolConfig), прокинуть в repository/usecase/grpcapi; зарегистрировать реальную реализацию вместо каркаса
- [x] 7.3 Добавить в `/readyz` `httpserver.ReadinessCheck{Name:"postgres", Check: pool.Ping}` (рядом с существующим temporal)

## 8. Тесты

- [x] 8.1 In-memory/стаб repository в дефолтном прогоне: table-driven + `t.Parallel()`; покрыть переходы статусов и guarded-CAS-конфликт (`RowsAffected==0 → ErrConflict`)
- [x] 8.2 Тесты keyset-пагинации: порядок `(created_at, id)` без дублей/пропусков, пустой `next_page_token` в конце, невалидный курсор → `ErrValidation`
- [x] 8.3 Тесты usecase/grpcapi: `GetService` NotFound-маппинг, отсутствие отдачи `err.Error()` клиенту
- [x] 8.4 Integration-тесты под тегом `integration` против реального Postgres: уникальность `(project, name)` и конкурентный guarded-CAS (две транзакции → один успех, один `ErrConflict`)

## 9. Проверка и сдача

- [x] 9.1 Прогнать `go build ./...`, `go test ./...` (дефолт) и линтер golangci-lint; integration-прогон с тегом при доступной БД
- [x] 9.2 Проверить, что все комментарии в новом коде — на русском; `git diff --exit-code` после `make proto` пуст
- [ ] 9.3 Открыть PR из `change/projects-catalog`, добиться зелёного CI (включая integration-джоб); после merge — `/opsx:archive`
