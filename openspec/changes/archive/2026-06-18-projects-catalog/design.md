## Context

Сервис `projects` сейчас — каркас из `foundation-and-pkg`: gRPC-сервер (`grpcx.ServerOptions`), Temporal lazy-клиент, HTTP `/readyz` (пинг только Temporal). `GetService` возвращает `Unimplemented`, Postgres не подключён. Готовы переиспользуемые `pkg/*`: `db.NewPool`, `errs` (sentinel'ы), `httpserver.ReadinessCheck`, `logger` (ключ `"err"`). Локально доступен `postgres-projects` (DSN `postgres://projects:projects@postgres-projects:5432/projects?sslmode=disable`). Контракт `proto/projects/v1/projects.proto` уже содержит enum `ServiceStatus` и каркас `GetService`.

Этот change материализует БЛОК 4 (База данных) и слой `usecase`/`repository` из Этапа 1 docs/IDP_MVP_plan.md, реализуя ADR-0004 (guarded-CAS). Это первый доменный change; workflow создания сервиса начинается только после его зелёного CI.

Ограничения: все комментарии в коде — на русском; реализация — в ветке `change/projects-catalog` от master; `.proto` wire-изменения — BREAKING; инструменты кодогена/миграций — в `./tools` с `GOWORK=off`.

## Goals / Non-Goals

**Goals:**
- Postgres-схема `services` + обратимые миграции через инструмент в `./tools`.
- `repository` поверх `pkg/db` с guarded-CAS-переходами статусов, `withTx` и узким `dbConn`.
- Keyset-пагинация `(created_at, id)` с непрозрачным base64-курсором.
- `usecase`-слой и доменная реализация gRPC `GetService` (404→NotFound) и `ListServices`; `CreateServiceRecord` (status=CREATING) без workflow.
- Реальный пинг Postgres в `/readyz`.
- Тесты: in-memory в дефолтном прогоне; integration под тегом; покрыты CAS-конфликт, переходы, пагинация.

**Non-Goals:**
- Temporal workflows/activities «Создание сервиса» и реальная провизия GitLab/Vault/Harbor (`create-service-workflow`).
- Реальный RBAC IDM `CheckAccess` (`idm-rbac-min`).
- User stories владельцев/переноса/удаления; физическое удаление записей.

## Decisions

### Решение 1: Инструмент миграций — goose (CLI + embed) против golang-migrate

Выбираем **goose** (`github.com/pressly/goose/v3`), пинуем в `./tools` (`GOWORK=off`), миграции — `.sql`-файлы в `services/projects/migrations`, применяются Makefile-таргетом (`make migrate-projects`).

Почему goose, а не golang-migrate:
- Один бинарь без CGO и без отдельного драйвера-формата строки; нативная работа с pgx/lib-pq.
- Простые `-- +goose Up/Down` в одном файле — обратимость рядом с прямой миграцией.
- Возможность встроить (`embed.FS`) и применять программно из integration-тестов; для MVP достаточно CLI-таргета.

Альтернатива golang-migrate отклонена: требует раздельных up/down-файлов и драйвера в строке источника; больше церемонии для одной таблицы. Выбор фиксируется отдельным ADR (см. adr.md).

### Решение 2: guarded-CAS как единственный способ менять статус (ADR-0004)

Каждый переход — `UPDATE services SET status=$new, updated_at=now() WHERE id=$id AND status=$expected`. Метод repository возвращает `errs.ErrConflict` при `RowsAffected()==0`. Никаких «прочитать → проверить → записать». Это даёт атомарность и корректную конкуренцию с будущим workflow.

Сигнатура (эскиз):
```
func (r *Repo) TransitionStatus(ctx context.Context, db dbConn, id uuid.UUID, expected, next Status) error
```
`dbConn` — узкий интерфейс (`Exec`/`Query`/`QueryRow`), удовлетворяемый `*pgxpool.Pool` и `pgx.Tx`, чтобы один метод работал и автономно, и внутри `withTx`.

### Решение 3: withTx и публикация после commit

Многошаговые записи — через `withTx(ctx, pool, func(tx) error)`: begin → вызов → commit/rollback; rollback откладывается через defer и игнорирует `pgx.ErrTxClosed`. Любая публикация статуса/события вызывается вызывающим кодом **после** успешного возврата `withTx` (после commit), а не внутри транзакции. В этом change «публикация» — заглушка-хук (лог), реальная шина — в последующих changes.

### Решение 4: Keyset-пагинация и формат курсора

Сортировка строгая по `(created_at, id)` (id как тай-брейкер, оба в индексе). Запрос продолжения: `WHERE (created_at, id) > ($ts, $id) ORDER BY created_at, id LIMIT $n`. Курсор — base64(JSON `{created_at, id}`) — непрозрачный для клиента; декод-ошибка → `errs.ErrValidation`. Пустой `next_page_token` означает конец. Поддерживающий индекс: `CREATE INDEX ON services (created_at, id)`.

Маппинг на gRPC: `ListServicesRequest{ project, page_size, page_token }`, `ListServicesResponse{ services[], next_page_token }`. `page_size` клампится (дефолт 50, максимум 200).

### Решение 5: Изменение .proto (BREAKING) и слои сервиса

Добавляем в `proto/projects/v1/projects.proto`: rpc `ListServices`, сообщения `ListServicesRequest/Response` и `Service` (project, name, status). Это wire-изменение → помечаем **BREAKING**, регенерируем стабы `make proto` (buf через `./tools`), коммитим сгенерированное (`git diff --exit-code` чист).

Раскладка слоёв в `services/projects` (docs Этап 1):
```
internal/repository  — Postgres-доступ, guarded-CAS, withTx, dbConn, keyset
internal/usecase     — доменные операции (Get/List/Create), маппинг ошибок
internal/grpcapi     — реализация ProjectsServiceServer поверх usecase
migrations/          — *.sql (goose)
```
Маппинг ошибок usecase→gRPC: `ErrNotFound→codes.NotFound`, `ErrConflict→codes.Aborted/FailedPrecondition`, `ErrValidation→codes.InvalidArgument`; внутренние — `codes.Internal` без `err.Error()` клиенту.

### Решение 6: Статус в БД и enum-маппинг

В БД `status` хранится как текст (`creating/active/decommissioned/failed`) для читаемости, со строгим маппингом в `ServiceStatus` proto. Незнакомое значение из БД не маппится в дефолт молча — это ошибка (`ErrValidation`/`Internal`). Zero-value enum (`UNSPECIFIED`) запрещён как валидный статус записи.

## Risks / Trade-offs

- **Текстовый статус допускает мусор в БД** → ограничение `CHECK (status IN (...))` в миграции + строгий маппинг в коде (паника/ошибка на неизвестном, не дефолт).
- **goose как новый инструмент в `./tools`** → изолирован `GOWORK=off`, пин версии в `tools/go.mod`, применение только через Makefile-таргет; ADR фиксирует выбор.
- **Keyset требует строгого порядка** → составной индекс `(created_at, id)` и id-тай-брейкер; без них возможны дубли/пропуски при равных `created_at`.
- **BREAKING .proto** → у каталога ещё нет внешних потребителей доменных методов (gateway вызывает каркас), риск низкий; помечаем BREAKING, регенерация в том же PR.
- **Конкурентный CAS трудно покрыть детерминированно** → in-memory стаб моделирует `RowsAffected==0`; реальную гонку проверяет integration-тест с двумя транзакциями.
- **`/readyz` пинг Postgres добавляет нагрузку** → лёгкий `Ping` с коротким таймаутом из контекста проверки.

## Migration Plan

1. Создать ветку `change/projects-catalog` от master.
2. Пин goose в `./tools` (`GOWORK=off`), Makefile-таргет `migrate-projects`, ADR на выбор инструмента.
3. Миграция `0001_create_services`: таблица + уникальный индекс `(project, name)` + индекс `(created_at, id)` + CHECK по статусу; обязательный down.
4. Обновить `.proto` (BREAKING) и регенерировать стабы (`make proto`), закоммитить сгенерированное.
5. Реализовать `repository` (dbConn, guarded-CAS, withTx, keyset) + `usecase` + `grpcapi`; подключить `db.NewPool` в `main.go`; добавить пинг Postgres в `/readyz`.
6. Тесты: in-memory (CAS-конфликт, переходы, пагинация) в дефолтном прогоне; integration под тегом.
7. PR с зелёным CI (включая integration-джоб), merge в master, затем `/opsx:archive`.

**Откат:** изменения только в БД-схеме и коде сервиса; внешние ресурсы не провизятся, Saga-компенсации не нужны. Откат схемы — `goose down` (обратимая миграция); откат кода — revert PR.

## Open Questions

- Тип `id`: UUID (генерация на стороне приложения) против `bigserial`. Предлагается UUID для идемпотентности будущего workflow; финализировать в реализации.
- Точный gRPC-код для проигранного CAS на чтение/листинг не критичен (CAS встречается на записи) — для записи использовать `FailedPrecondition` против `Aborted`; зафиксировать при реализации usecase-маппинга.
