## ADDED Requirements

### Requirement: Схема каталога сервисов в Postgres

Каталог единиц-сервисов ДОЛЖЕН (MUST) храниться в PostgreSQL в таблице `services` с полями `id`, `project`, `name`, `status`, `created_at`, `updated_at`. Уникальность пары `(project, name)` ДОЛЖНА (MUST) обеспечиваться ограничением БД (а не проверкой в коде). Схема ДОЛЖНА (MUST) применяться инструментом миграций, пинованным в `./tools` (запуск с `GOWORK=off`), с обратимыми (down) шагами.

#### Scenario: Применение миграций создаёт схему

- **GIVEN** чистая база `postgres-projects`
- **WHEN** запускается команда миграций из `./tools`
- **THEN** создаётся таблица `services` с уникальным индексом по `(project, name)`, и повторный запуск миграций идемпотентен (без ошибок и без изменений)

#### Scenario: Дубликат имени в проекте отклоняется БД

- **GIVEN** в каталоге уже есть запись `(project=p1, name=svc)`
- **WHEN** выполняется вставка второй записи `(project=p1, name=svc)`
- **THEN** вставка отклоняется нарушением уникального ограничения, и repository возвращает `errs.ErrConflict` (не молчаливый дубль)

### Requirement: Переходы статусов через guarded-CAS

Все переходы статуса сервиса (`CREATING → ACTIVE → DECOMMISSIONED`, а также `→ FAILED`) ДОЛЖНЫ (MUST) выполняться как guarded compare-and-set: `UPDATE services SET status=$new, updated_at=now() WHERE id=$id AND status=$expected`; при `RowsAffected==0` слой ДОЛЖЕН (MUST) возвращать `errs.ErrConflict`. Реализация НЕ ДОЛЖНА (MUST NOT) использовать check-then-act (прочитать статус → проверить в коде → записать без условия по `$expected`). Соответствует ADR-0004.

#### Scenario: Успешный переход из ожидаемого статуса

- **GIVEN** запись со `status=CREATING`
- **WHEN** вызывается переход с `expected=CREATING`, `new=ACTIVE`
- **THEN** `RowsAffected==1`, статус становится `ACTIVE`, `updated_at` обновляется, ошибка не возвращается

#### Scenario: Конфликт guarded-CAS при неожиданном статусе

- **GIVEN** запись со `status=ACTIVE`
- **WHEN** вызывается переход с `expected=CREATING`, `new=ACTIVE`
- **THEN** `RowsAffected==0`, статус не меняется, и возвращается `errs.ErrConflict`

#### Scenario: Конкурентные попытки одного перехода

- **GIVEN** запись со `status=CREATING` и две одновременные попытки перейти в `ACTIVE` с `expected=CREATING`
- **WHEN** обе попытки выполняются конкурентно
- **THEN** ровно одна получает `RowsAffected==1` (успех), а вторая — `RowsAffected==0` → `errs.ErrConflict` (без молчаливого перетирания)

### Requirement: Многошаговые записи в транзакции

Многошаговые операции записи ДОЛЖНЫ (MUST) выполняться в одной транзакции через хелпер `withTx`. Слой ДОЛЖЕН (MUST) использовать узкий интерфейс `dbConn`, удовлетворяемый и `*pgxpool.Pool`, и `pgx.Tx`, чтобы один и тот же код работал внутри и вне транзакции. Публикация статусов/событий ДОЛЖНА (MUST) происходить только после успешного commit.

#### Scenario: Откат при ошибке внутри транзакции

- **GIVEN** многошаговая запись, второй шаг которой завершается ошибкой
- **WHEN** транзакция выполняется через `withTx`
- **THEN** транзакция откатывается (rollback), ни одно изменение не фиксируется, и событие/статус не публикуется

#### Scenario: Публикация только после commit

- **GIVEN** успешная многошаговая запись через `withTx`
- **WHEN** транзакция зафиксирована (commit)
- **THEN** публикация статуса/события выполняется после commit, а при сбое commit публикация не происходит

### Requirement: Keyset-пагинация каталога

Листинг сервисов ДОЛЖЕН (MUST) использовать keyset-пагинацию с детерминированной сортировкой по `(created_at, id)`. Курсор ДОЛЖЕН (MUST) быть непрозрачным (base64) и кодировать ключ последней отданной строки. Слой НЕ ДОЛЖЕН (MUST NOT) применять offset-пагинацию. Невалидный/повреждённый курсор ДОЛЖЕН (MUST) приводить к `errs.ErrValidation`.

#### Scenario: Постраничный обход без пропусков и дублей

- **GIVEN** в каталоге N записей и размер страницы `limit`
- **WHEN** клиент проходит страницы, передавая курсор из предыдущего ответа
- **THEN** строки идут в порядке `(created_at, id)`, без дублей и пропусков, и последняя страница возвращает пустой курсор продолжения

#### Scenario: Невалидный курсор отклоняется

- **GIVEN** клиент передаёт повреждённый (не-base64 или нераскодируемый) курсор
- **WHEN** выполняется листинг
- **THEN** возвращается `errs.ErrValidation`, запрос к БД с битым ключом не выполняется

### Requirement: Usecase и gRPC-чтение каталога

Сервис `projects` ДОЛЖЕН (MUST) реализовать `usecase`-слой поверх repository и доменную реализацию gRPC: `GetService` (чтение из каталога; отсутствие записи → `codes.NotFound`) и `ListServices` (keyset-пагинация). Допускается `CreateServiceRecord` — вставка со `status=CREATING` БЕЗ запуска Temporal workflow. Сервис НЕ ДОЛЖЕН (MUST NOT) отдавать клиенту `err.Error()` внутренних ошибок.

#### Scenario: GetService возвращает существующую запись

- **GIVEN** в каталоге есть запись `(project=p1, name=svc, status=ACTIVE)`
- **WHEN** вызывается `GetService(project=p1, name=svc)`
- **THEN** возвращается ответ со `status=SERVICE_STATUS_ACTIVE` и совпадающими `project`/`name`

#### Scenario: GetService для отсутствующей записи → NotFound

- **GIVEN** в каталоге нет записи `(project=p1, name=missing)`
- **WHEN** вызывается `GetService(project=p1, name=missing)`
- **THEN** возвращается gRPC-статус `codes.NotFound`, без раскрытия внутренних деталей ошибки

#### Scenario: CreateServiceRecord не запускает workflow

- **WHEN** вызывается `CreateServiceRecord(project=p1, name=svc)`
- **THEN** в каталоге появляется запись со `status=CREATING`, и Temporal workflow НЕ запускается

### Requirement: Готовность сервиса с пингом Postgres

`/readyz` сервиса `projects` ДОЛЖЕН (MUST) быть content-aware и реально пинговать Postgres через `pkg/httpserver.ReadinessCheck`. При недоступности БД эндпоинт ДОЛЖЕН (MUST) возвращать неуспех (не 200), чтобы Kubernetes не направлял трафик в неготовый под.

#### Scenario: Postgres доступен

- **GIVEN** пул соединений к Postgres исправен
- **WHEN** выполняется запрос к `/readyz`
- **THEN** проверка `postgres` проходит и эндпоинт сообщает готовность

#### Scenario: Postgres недоступен

- **GIVEN** Postgres недоступен (пинг падает)
- **WHEN** выполняется запрос к `/readyz`
- **THEN** эндпоинт возвращает неуспешный статус с указанием неготовой зависимости `postgres`

### Requirement: Тестовое покрытие доменного слоя

Тесты ДОЛЖНЫ (MUST) быть table-driven и использовать `t.Parallel()`. Обязательно ДОЛЖНЫ (MUST) быть покрыты: конфликт guarded-CAS (`RowsAffected==0 → ErrConflict`), переходы статусов и keyset-пагинация. Стаб/in-memory тесты ДОЛЖНЫ (MUST) проходить в дефолтном прогоне; тесты против реальной БД ДОЛЖНЫ (MUST) быть под тегом сборки `integration`.

#### Scenario: Дефолтный прогон не требует БД

- **WHEN** выполняется `go test ./...` без тега `integration`
- **THEN** доменные тесты (guarded-CAS-конфликт, переходы, пагинация) проходят на стабе/in-memory без подключения к Postgres

#### Scenario: Integration-тесты под тегом

- **WHEN** выполняется прогон с тегом `integration` при доступной БД
- **THEN** запускаются тесты repository против реального Postgres, проверяющие guarded-CAS и уникальность `(project, name)`
