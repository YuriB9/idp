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

### Requirement: REST-ручка изменения владельцев сервиса

Периметр (gateway, ADR-0009) ДОЛЖЕН (MUST) предоставлять ручку изменения
владельцев `PUT /projects/{project}/services/{name}/owners` с идемпотентной
семантикой «заменить набор владельцев целиком». Тело запроса ДОЛЖНО (MUST)
содержать полный желаемый набор `owners` и версию `owners_version` для
optimistic-concurrency (валидация формы — zod-совместимая: непустые строки, без
дублей). Шлюз НЕ ДОЛЖЕН (MUST NOT) содержать доменной логики — он маппит
REST↔gRPC `SetServiceOwners` и gRPC-коды в HTTP. Внутренние ошибки НЕ ДОЛЖНЫ
(MUST NOT) раскрываться (только стабильное сообщение; детали — в лог по ключу
slog `err`). Маппинг: `NotFound→404`, `FailedPrecondition→409` (конфликт
версии), `InvalidArgument→400`, `PermissionDenied→403`.

#### Scenario: Успешная замена владельцев

- **GIVEN** субъект с правом `change_owners` и существующий сервис `(p1, svc)`
- **WHEN** `PUT /projects/p1/services/svc/owners` с телом
  `{"owners":["alice","bob"],"owners_version":4}`
- **THEN** возвращается `200/202` с подтверждением запуска изменения, без
  раскрытия внутренних деталей

#### Scenario: Невалидное тело — 400

- **WHEN** `PUT .../owners` с телом, содержащим пустые строки или дубли во
  `owners`, либо без `owners_version`
- **THEN** возвращается `400`, gRPC-вызов не выполняется

#### Scenario: Конфликт версии — 409

- **GIVEN** актуальная `owners_version=5`
- **WHEN** `PUT .../owners` с устаревшей `owners_version=4`
- **THEN** gRPC возвращает `FailedPrecondition`, шлюз отдаёт `409` «конфликт
  состояния»

#### Scenario: Отсутствующий сервис — 404

- **WHEN** `PUT /projects/p1/services/missing/owners`
- **THEN** gRPC возвращает `NotFound`, шлюз отдаёт `404`

### Requirement: RBAC change_owners на ручке изменения владельцев (fail-closed)

Шлюз ДОЛЖЕН (MUST) вызывать IDM `CheckAccess` с действием `change_owners` и
ресурсом `project:<project>` ПЕРЕД проксированием ручки изменения владельцев.
Отказ политики, недоступность или ошибка IDM ДОЛЖНЫ (MUST) приводить к HTTP
`403` (fail-closed) без побочных эффектов и без раскрытия деталей; `subject`
берётся из claims контекста.

#### Scenario: Нет права — 403 до проксирования

- **GIVEN** субъект без права `(change_owners, project:p1)`
- **WHEN** `PUT /projects/p1/services/svc/owners`
- **THEN** возвращается `403`, gRPC `SetServiceOwners` не вызывается

#### Scenario: IDM недоступен — 403 (fail-closed)

- **GIVEN** IDM недоступен
- **WHEN** `PUT /projects/p1/services/svc/owners`
- **THEN** возвращается `403`, деталь ошибки только в лог по ключу slog `err`

### Requirement: Владельцы в ответах чтения периметра

Ответы чтения и листинга сервисов ДОЛЖНЫ (MUST) включать поле `owners` (и версию `owners_version`) каждого сервиса, отражая данные из `projects` (`GET .../services/{name}` и `GET .../services`). Контракт
OpenAPI и TS-клиент ДОЛЖНЫ (MUST) обновляться согласованно (`gen:check` зелёный);
рантайм-валидация ответов через zod `.parse` ДОЛЖНА (MUST) принимать новые поля.

#### Scenario: GET одного сервиса отдаёт владельцев

- **GIVEN** сервис `(p1, svc)` с владельцами `{alice, bob}`
- **WHEN** `GET /projects/p1/services/svc`
- **THEN** ответ содержит `owners:["alice","bob"]` и `owners_version`

#### Scenario: LIST отдаёт владельцев по каждой записи

- **WHEN** `GET /projects/p1/services`
- **THEN** каждая запись страницы содержит своё поле `owners`, zod `.parse`
  проходит без ошибок

### Requirement: REST-ручка вывода из эксплуатации

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять ручку вывода сервиса из
эксплуатации `POST /projects/{project}/services/{name}/decommission` с телом,
несущим явное предусловие снятой нагрузки (`load_drained`). Метод POST на
под-ресурсе выбран вместо `DELETE`, т.к. это soft-delete (данные каталога
сохраняются), а `DELETE` подразумевал бы удаление/purge. Ручка ДОЛЖНА (MUST) быть
идемпотентной: повторный вызов на уже выведенном сервисе возвращает успех с
итоговым состоянием. gateway ДОЛЖЕН (MUST) вызывать IDM `CheckAccess`
(`decommission`, `project:<project>`) ПЕРЕД проксированием (fail-closed) и НЕ
ДОЛЖЕН (MUST NOT) раскрывать внутренние ошибки клиенту. OpenAPI остаётся
единственным источником правды; TS-клиент/zod регенерируются (`gen:check`).

#### Scenario: Успешный запуск вывода из эксплуатации

- **GIVEN** субъект с правом `decommission` на `project:demo` и активный сервис
  `(demo, svc)` со снятой нагрузкой
- **WHEN** выполняется `POST /projects/demo/services/svc/decommission` с
  `{load_drained:true}`
- **THEN** gateway проксирует в `projects`, запускается вывод из эксплуатации,
  возвращается успешный ответ без раскрытия внутренних деталей

#### Scenario: Идемпотентный повтор

- **GIVEN** сервис `(demo, svc)` уже в статусе `decommissioned`
- **WHEN** повторно выполняется `POST .../decommission`
- **THEN** возвращается успех с итоговым состоянием (без ошибки)

### Requirement: Маппинг кодов и статус decommissioned в ответах периметра

gateway ДОЛЖЕН (MUST) маппить gRPC-коды в HTTP для операции вывода из эксплуатации:
`PermissionDenied→403`, `NotFound→404`, конкурентный конфликт (`Aborted`)→409,
невыполненное предусловие (`FailedPrecondition`)→422, `InvalidArgument→400`,
прочее→500 (без деталей). Чтобы развести конкурентный конфликт (409) и
невыполненное предусловие (422), `httpFromGRPC` ДОЛЖЕН (MUST) трактовать
`codes.Aborted` как 409, а `codes.FailedPrecondition` как 422. Ответы `GET`/`LIST`
ДОЛЖНЫ (MUST) включать статус `decommissioned` и `decommissioned_at` для выведенных
сервисов. Существующий маппинг конфликта владельцев (`change-owners`) ДОЛЖЕН (MUST)
сохранять итоговый HTTP-код 409 (через `Aborted`).

#### Scenario: Предусловие не выполнено → 422

- **GIVEN** активный сервис `(demo, svc)` и `{load_drained:false}`
- **WHEN** выполняется `POST .../decommission`
- **THEN** `projects` возвращает `FailedPrecondition`, gateway отвечает `422`, без
  раскрытия внутренних деталей

#### Scenario: Конкурентный конфликт → 409

- **GIVEN** статус сервиса сменился конкурентной операцией
- **WHEN** выполняется `POST .../decommission`
- **THEN** `projects` возвращает `Aborted`, gateway отвечает `409`

#### Scenario: Нет права → 403 (fail-closed)

- **GIVEN** субъект без права `decommission` (или IDM недоступен)
- **WHEN** выполняется `POST .../decommission`
- **THEN** gateway отвечает `403`, запрос в `projects` не проксируется при отказе
  на периметре, деталь ошибки — только в лог по ключу slog `err`

#### Scenario: Статус decommissioned в чтении

- **GIVEN** сервис `(demo, svc)` выведен из эксплуатации
- **WHEN** выполняется `GET /projects/demo/services/svc`
- **THEN** ответ содержит `status=decommissioned` и `decommissioned_at`
