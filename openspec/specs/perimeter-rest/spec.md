# perimeter-rest Specification

## Purpose
TBD - created by syncing change create-service-portal. Update Purpose after archive.
## Requirements
### Requirement: REST-ручка создания сервиса

API-шлюз ДОЛЖЕН (MUST) реализовать `POST /api/projects/{project}/services` с
телом `{name}`, который маппит запрос в gRPC `projectsv1.CreateService(project,
name)` и при успехе возвращает HTTP 201 с телом по форме OpenAPI (идентификатор
записи и стартовый статус `creating`). Шлюз НЕ ДОЛЖЕН (MUST NOT) выполнять
доменную логику создания сам — он лишь проксирует к `projects`.

#### Scenario: Успешный запуск создания

- **GIVEN** валидные `project` и тело `{name}`
- **WHEN** клиент шлёт `POST /api/projects/{project}/services`
- **THEN** шлюз вызывает gRPC `CreateService`, и при `OK` отвечает 201 с JSON,
  содержащим `id` и `status:"creating"` по схеме OpenAPI

#### Scenario: Конфликт имени сервиса

- **GIVEN** в проекте уже существует сервис с таким `name`
- **WHEN** клиент шлёт `POST /api/projects/{project}/services`
- **THEN** gRPC возвращает `FailedPrecondition`/`AlreadyExists`, и шлюз отвечает
  409 с JSON-ошибкой без раскрытия `err.Error()`

#### Scenario: Невалидное тело запроса

- **WHEN** тело отсутствует, не парсится или `name` пустой/не проходит валидацию
- **THEN** шлюз отвечает 400 с JSON-ошибкой и НЕ вызывает gRPC (либо gRPC
  вернул `InvalidArgument` → 400), не раскрывая внутренних деталей

### Requirement: REST-ручка чтения статуса сервиса

API-шлюз ДОЛЖЕН (MUST) реализовать `GET /api/projects/{project}/services/{name}`,
который маппит запрос в gRPC `projectsv1.GetService(project, name)` и возвращает
200 с JSON по форме OpenAPI (`project`, `name`, `status`). Отсутствие записи
ДОЛЖНО (MUST) отображаться в HTTP 404.

#### Scenario: Сервис найден

- **GIVEN** в каталоге есть запись `(project, name)` со статусом `creating`
- **WHEN** клиент шлёт `GET /api/projects/{project}/services/{name}`
- **THEN** шлюз отвечает 200 с JSON `{project, name, status:"creating"}`

#### Scenario: Сервис не найден

- **GIVEN** записи `(project, name)` нет
- **WHEN** клиент шлёт `GET /api/projects/{project}/services/{name}`
- **THEN** gRPC возвращает `NotFound`, и шлюз отвечает 404 с JSON-ошибкой без
  раскрытия внутренних деталей

### Requirement: REST-ручка листинга сервисов проекта

API-шлюз ДОЛЖЕН (MUST) реализовать `GET /api/projects/{project}/services` с
keyset-пагинацией: query-параметры `page_size` и `page_token` маппятся в gRPC
`projectsv1.ListServices`, а `next_page_token` из ответа возвращается клиенту по
форме OpenAPI. Шлюз НЕ ДОЛЖЕН (MUST NOT) интерпретировать содержимое
непрозрачного курсора.

#### Scenario: Первая страница без курсора

- **GIVEN** в проекте есть сервисы
- **WHEN** клиент шлёт `GET /api/projects/{project}/services?page_size=N`
- **THEN** шлюз вызывает gRPC `ListServices` и отвечает 200 с массивом сервисов
  и `next_page_token` (пустой, если выборка исчерпана)

#### Scenario: Продолжение по курсору

- **GIVEN** предыдущий ответ вернул непустой `next_page_token`
- **WHEN** клиент шлёт запрос с `page_token=<курсор>`
- **THEN** шлюз передаёт курсор в gRPC без изменений и возвращает следующую
  страницу

### Requirement: Маппинг gRPC-кодов в HTTP и неразглашение внутренних ошибок

API-шлюз ДОЛЖЕН (MUST) маппить gRPC-коды в HTTP-статусы детерминированно:
`NotFound`→404, `FailedPrecondition`/`AlreadyExists`→409, `InvalidArgument`→400,
любой прочий/`Internal`/`Unknown`→500. Шлюз НЕ ДОЛЖЕН (MUST NOT) раскрывать
клиенту `err.Error()` или внутренние сообщения gRPC; наружу отдаётся только
стабильное JSON-тело ошибки. В Prometheus-метках (если включены) ДОЛЖЕН (MUST)
использоваться `RoutePattern()`, а не сырой `URL.Path`.

#### Scenario: Внутренняя ошибка не утекает

- **GIVEN** gRPC-вызов вернул `Internal` с подробным сообщением
- **WHEN** шлюз формирует HTTP-ответ
- **THEN** клиент получает 500 со стабильным JSON-телом без `err.Error()` и без
  внутренних деталей; подробность пишется только в лог (ключ slog `err`)

#### Scenario: Детерминированный маппинг кодов

- **WHEN** gRPC возвращает один из кодов `NotFound`/`FailedPrecondition`/
  `InvalidArgument`/`Internal`
- **THEN** HTTP-статус равен соответственно `404`/`409`/`400`/`500`

### Requirement: Граница авторизации периметра

API-шлюз ДОЛЖЕН (MUST) применять существующие middlewares
(`RequestID`/`Recoverer`/`RateLimit`/`Auth`) к доменным REST-ручкам и оставлять
точку для проверки RBAC IDM `CheckAccess` как заглушку (как в gRPC
`CreateService`), не выполняя реальный RBAC в этом change. Аутентификация на
периметре ДОЛЖНА (MUST) быть fail-closed; `AUTH_DISABLED` допустим ТОЛЬКО в
локальном окружении.

#### Scenario: Доменные ручки под middlewares периметра

- **WHEN** запрос приходит на `/api/projects/...`
- **THEN** он проходит через `RequestID`/`Recoverer`/`RateLimit`/`Auth` до
  вызова gRPC

#### Scenario: Fail-closed без локального флага

- **GIVEN** окружение без `AUTH_DISABLED`
- **WHEN** стартует шлюз без валидной конфигурации проверки токенов
- **THEN** аутентификация остаётся обязательной (запрос без валидного токена не
  доходит до gRPC)
