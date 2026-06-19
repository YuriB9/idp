## MODIFIED Requirements

### Requirement: Граница авторизации периметра

API-шлюз ДОЛЖЕН (MUST) применять существующие middlewares
(`RequestID`/`Recoverer`/`RateLimit`/`Auth`) к доменным REST-ручкам И перед
проксированием к `projects` ДОЛЖЕН (MUST) вызывать IDM `CheckAccess` с
`subject` из `auth.ClaimsFromContext`, `resource = "project:" + project` и
`action` по операции (`create` для POST, `read` для GET одного сервиса, `list`
для GET листинга). Запрос ДОЛЖЕН (MUST) проксироваться к `projects` ТОЛЬКО при
`allowed=true`. При `allowed=false` ИЛИ недоступности/ошибке вызова IDM шлюз
ДОЛЖЕН (MUST) отвечать HTTP 403 (fail-closed) со стабильным JSON-телом, НЕ
раскрывая внутренних деталей. Аутентификация на периметре ДОЛЖНА (MUST) быть
fail-closed; `AUTH_DISABLED` допустим ТОЛЬКО в локальном окружении.

#### Scenario: Доменные ручки под middlewares периметра

- **GIVEN** запрос на `/api/projects/...`
- **WHEN** он обрабатывается шлюзом
- **THEN** он проходит через `RequestID`/`Recoverer`/`RateLimit`/`Auth` и вызов
  IDM `CheckAccess` до проксирования к gRPC `projects`

#### Scenario: Разрешено — запрос проксируется

- **GIVEN** субъект с правом `(create, project:<p>)`
- **WHEN** клиент шлёт `POST /api/projects/<p>/services`
- **THEN** IDM возвращает `allowed=true`, и шлюз вызывает gRPC `CreateService`

#### Scenario: Отказ RBAC — 403

- **GIVEN** субъект без права на запрошенное действие/ресурс
- **WHEN** клиент шлёт доменный запрос
- **THEN** IDM возвращает `allowed=false`, и шлюз отвечает 403 со стабильным
  JSON-телом без раскрытия деталей, не вызывая `projects`

#### Scenario: IDM недоступен — fail-closed 403

- **GIVEN** сервис IDM недоступен или вернул ошибку вызова
- **WHEN** клиент шлёт доменный запрос
- **THEN** шлюз отвечает 403 (доступ не предоставляется молча) и не проксирует к
  `projects`

#### Scenario: Fail-closed без локального флага

- **GIVEN** окружение без `AUTH_DISABLED`
- **WHEN** стартует шлюз без валидной конфигурации проверки токенов
- **THEN** аутентификация остаётся обязательной (запрос без валидного токена не
  доходит до RBAC/gRPC)

### Requirement: Маппинг gRPC-кодов в HTTP и неразглашение внутренних ошибок

API-шлюз ДОЛЖЕН (MUST) маппить gRPC-коды в HTTP-статусы детерминированно:
`NotFound`→404, `FailedPrecondition`/`AlreadyExists`→409, `InvalidArgument`→400,
`PermissionDenied`→403, любой прочий/`Internal`/`Unknown`→500. Шлюз НЕ ДОЛЖЕН
(MUST NOT) раскрывать клиенту `err.Error()` или внутренние сообщения gRPC;
наружу отдаётся только стабильное JSON-тело ошибки. В Prometheus-метках (если
включены) ДОЛЖЕН (MUST) использоваться `RoutePattern()`, а не сырой `URL.Path`.

#### Scenario: Внутренняя ошибка не утекает

- **GIVEN** gRPC-вызов вернул `Internal` с подробным сообщением
- **WHEN** шлюз формирует HTTP-ответ
- **THEN** клиент получает 500 со стабильным JSON-телом без `err.Error()` и без
  внутренних деталей; подробность пишется только в лог (ключ slog `err`)

#### Scenario: Детерминированный маппинг кодов

- **WHEN** gRPC возвращает один из кодов `NotFound`/`FailedPrecondition`/
  `InvalidArgument`/`PermissionDenied`/`Internal`
- **THEN** HTTP-статус равен соответственно `404`/`409`/`400`/`403`/`500`

#### Scenario: Отказ авторизации из projects маппится в 403

- **GIVEN** gRPC `CreateService` вернул `PermissionDenied` (defense-in-depth в
  `projects`)
- **WHEN** шлюз формирует HTTP-ответ
- **THEN** клиент получает 403 со стабильным JSON-телом без внутренних деталей
