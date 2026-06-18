# platform-libraries Specification

## Purpose
TBD - created by archiving change foundation-and-pkg. Update Purpose after archive.
## Requirements
### Requirement: HTTP-сервер с едиными политиками

`pkg/httpserver` ДОЛЖЕН (MUST) предоставлять конструктор HTTP-сервера с заданными таймаутами, graceful shutdown и middleware-стеком: Recoverer, RequestID, rate-limit, переключаемый auth и content-aware `/readyz`.

#### Scenario: Graceful shutdown дренирует in-flight запросы

- **WHEN** сервер получает сигнал остановки при наличии незавершённых запросов
- **THEN** он перестаёт принимать новые соединения и завершает in-flight в пределах drain-таймаута (`WithTimeout(WithoutCancel(ctx), 30s)`), затем закрывается

#### Scenario: Recoverer ловит панику в обработчике

- **WHEN** обработчик паникует
- **THEN** middleware Recoverer перехватывает панику, логирует с ключом `"err"` и возвращает 500 без падения процесса

### Requirement: S2S HTTP-клиент с маппингом ошибок

`pkg/httpclient` ДОЛЖЕН (MUST) предоставлять межсервисный HTTP-клиент с тюнингованным `Transport` и маппингом кодов ответа в канонические ошибки: `404 → ErrNotFound`, `409 → ErrConflict`.

#### Scenario: Маппинг 404 и 409 в sentinel-ошибки

- **WHEN** клиент получает ответ 404 или 409
- **THEN** он возвращает соответственно `errs.ErrNotFound` или `errs.ErrConflict`, проверяемые через `errors.Is`

### Requirement: Конфигурация из окружения

`pkg/config` ДОЛЖЕН (MUST) предоставлять env-хелперы, которые корректно принимают легитимный `0` и не подменяют его дефолтом.

#### Scenario: Нулевое значение не теряется

- **WHEN** переменная окружения задана как `0`
- **THEN** хелпер возвращает `0`, а не значение по умолчанию

### Requirement: Канонические ошибки

`pkg/errs` ДОЛЖЕН (MUST) определять канонические sentinel-ошибки (как минимум `ErrNotFound`, `ErrConflict`), используемые во всех сервисах без локальных дублей.

#### Scenario: Один источник sentinel-ошибок

- **WHEN** сервису нужна ошибка «не найдено» или «конфликт»
- **THEN** он использует `errs.ErrNotFound`/`errs.ErrConflict`, а не объявляет собственные

### Requirement: Пул соединений к БД

`pkg/db` ДОЛЖЕН (MUST) предоставлять `NewPool` с обязательной конфигурацией пула `*pgxpool.Pool` (размер, таймауты, lifetime).

#### Scenario: NewPool требует конфиг пула

- **WHEN** вызывается `NewPool` с конфигурацией пула
- **THEN** возвращается готовый пул с применёнными лимитами, а отсутствующая конфигурация приводит к явной ошибке

### Requirement: Auth fail-closed с JWKS

`pkg/auth` ДОЛЖЕН (MUST) валидировать JWT строго (`WithAudience`, `WithIssuer`, `WithValidMethods`, `WithExpirationRequired`) по ключам JWKS и быть **fail-closed**: пустой `JWKS_URL` → `os.Exit(1)`; отключение auth только явным `AUTH_DISABLED=true`. `JWKS_URL` ДОЛЖЕН форсироваться на https. Сравнение admin/god-key — через `subtle.ConstantTimeCompare`.

#### Scenario: Пустой JWKS_URL не запускает сервис

- **WHEN** сервис стартует с auth и пустым `JWKS_URL`
- **THEN** процесс завершается через `os.Exit(1)` (без passthrough)

#### Scenario: Явное отключение auth для локалки

- **WHEN** задано `AUTH_DISABLED=true`
- **THEN** auth-middleware пропускает запросы, и это единственный путь отключить проверку

#### Scenario: Невалидный токен отклоняется

- **WHEN** приходит токен с неверным issuer/audience, неподдерживаемым методом подписи или без срока действия
- **THEN** запрос отклоняется с 401, клиенту не отдаётся `err.Error()`

### Requirement: SSRF-guard для исходящих вызовов

`pkg/ssrf` ДОЛЖЕН (MUST) предоставлять `ValidateURL` (только https, блок private/loopback/link-local/ULA) для проверки на этапе записи и `GuardedDialContext` для проверки на этапе соединения (против TOCTOU/DNS-rebinding).

#### Scenario: Приватный/loopback адрес отклоняется на записи

- **WHEN** `ValidateURL` получает не-https URL либо адрес из private/loopback/link-local/ULA
- **THEN** он возвращает ошибку, и URL не сохраняется

#### Scenario: Ребиндинг во время dial блокируется

- **WHEN** имя резолвится в запрещённый адрес на этапе установки соединения
- **THEN** `GuardedDialContext` отклоняет соединение, даже если `ValidateURL` ранее прошёл

### Requirement: Структурированное логирование

`pkg/logger` ДОЛЖЕН (MUST) предоставлять `slog`-логгер, в котором ошибки логируются под единым ключом `"err"`.

#### Scenario: Единый ключ ошибки

- **WHEN** код логирует ошибку через общий логгер
- **THEN** она пишется под ключом `"err"` во всех сервисах

### Requirement: gRPC interceptor-стек

Общий пакет ДОЛЖЕН (MUST) предоставлять единый стек gRPC-перехватчиков (recovery, request-id, otel, auth) для серверов IDM и сервиса проектов.

#### Scenario: Паника в gRPC-хендлере перехватывается

- **WHEN** обработчик gRPC паникует
- **THEN** recovery-interceptor возвращает gRPC-статус ошибки и логирует с ключом `"err"`, процесс не падает

#### Scenario: Сквозной request-id и trace-context

- **WHEN** входящий gRPC-запрос содержит (или не содержит) request-id и trace-context в метаданных
- **THEN** interceptor-стек проставляет/пробрасывает request-id и OpenTelemetry trace-context по цепочке вызовов

