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

### Requirement: REST-ручка переноса сервиса

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять ручку переноса сервиса
`POST /projects/{project}/services/{name}/transfer` с телом, несущим
`target_project`. Метод POST на под-ресурсе выбран (по образцу `decommission`),
т.к. перенос — доменное действие, а не CRUD-replace ресурса. Ручка ДОЛЖНА (MUST)
быть идемпотентной: повторный вызов на уже перенесённом сервисе возвращает успех с
итоговым состоянием. gateway ДОЛЖЕН (MUST) вызывать IDM `CheckAccess` для ОБОИХ
проектов (`transfer` на `project:<source>` И `transfer_in` на `project:<target>`)
ПЕРЕД проксированием (fail-closed) и НЕ ДОЛЖЕН (MUST NOT) раскрывать внутренние
ошибки клиенту. OpenAPI остаётся единственным источником правды; TS-клиент/zod
регенерируются (`gen:check`).

#### Scenario: Успешный запуск переноса

- **GIVEN** субъект с правами `transfer` на `project:demo` и `transfer_in` на
  `project:demo2`, активный сервис `(demo, svc)`, пара `(demo2, svc)` свободна
- **WHEN** выполняется `POST /projects/demo/services/svc/transfer` с
  `{target_project:"demo2"}`
- **THEN** gateway проксирует в `projects`, запускается перенос, возвращается
  успешный ответ без раскрытия внутренних деталей

#### Scenario: Идемпотентный повтор

- **GIVEN** сервис уже перенесён в `(demo2, svc)`
- **WHEN** повторно выполняется `POST /projects/demo/services/svc/transfer` с
  `{target_project:"demo2"}`
- **THEN** возвращается успех с итоговым состоянием (без ошибки)

### Requirement: Двусторонняя авторизация и маппинг кодов переноса на периметре

gateway ДОЛЖЕН (MUST) при переносе проверять права на ОБА проекта (`transfer` на
source И `transfer_in` на target) перед проксированием; отказ по любому из них →
`403` (fail-closed), запрос в `projects` не проксируется. gateway ДОЛЖЕН (MUST)
маппить gRPC-коды в HTTP для операции переноса: `PermissionDenied→403`,
`NotFound→404`, конкурентный конфликт/занятое имя в target (`Aborted`/
`AlreadyExists`)→409, невыполненное предусловие (`FailedPrecondition`)→422,
`InvalidArgument→400`, прочее→500 (без деталей). Новых правил `httpFromGRPC` не
требуется — используется разведение `Aborted→409`/`FailedPrecondition→422` из
ADR-0012. Ответы `GET`/`LIST` ДОЛЖНЫ (MUST) корректно отражать транзитный статус
`transferring`.

#### Scenario: Занятое имя в target → 409

- **GIVEN** в `project:demo2` уже есть сервис `svc`
- **WHEN** выполняется `POST /projects/demo/services/svc/transfer` с
  `{target_project:"demo2"}`
- **THEN** `projects` возвращает `Aborted`, gateway отвечает `409`

#### Scenario: Недопустимый исходный статус → 422

- **GIVEN** сервис `(demo, svc)` в статусе `creating` (или `transferring`)
- **WHEN** выполняется `POST .../transfer`
- **THEN** `projects` возвращает `FailedPrecondition`, gateway отвечает `422`, без
  раскрытия внутренних деталей

#### Scenario: Нет права на source или target → 403 (fail-closed)

- **GIVEN** субъект без права `transfer` на source (или без `transfer_in` на
  target, или IDM недоступен)
- **WHEN** выполняется `POST .../transfer`
- **THEN** gateway отвечает `403`, запрос в `projects` не проксируется, деталь
  ошибки — только в лог по ключу slog `err`

#### Scenario: Транзитный статус в чтении

- **GIVEN** сервис `(demo, svc)` находится в процессе переноса
- **WHEN** выполняется `GET /projects/demo/services/svc`
- **THEN** ответ содержит `status=transferring`

### Requirement: Горизонтальные REST-ручки чтения каталога IAM

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять ГОРИЗОНТАЛЬНЫЕ (не project-scoped)
read-only ручки IAM-админки: `GET /iam/roles` (список ролей),
`GET /iam/permissions` (все права), `GET /iam/roles/{role}/permissions` (права
роли), `GET /iam/subjects` (субъекты с их ролями, keyset-пагинация
`page_size`/`page_token`), `GET /iam/subjects/{subject}/roles` (роли субъекта).
gateway ДОЛЖЕН (MUST) ПЕРЕД проксированием каждой ручки вызывать `CheckAccess`
с `resource="iam:global"`, `action="read"` (fail-closed) и НЕ ДОЛЖЕН (MUST NOT)
раскрывать внутренние ошибки клиенту. OpenAPI остаётся единственным источником
правды; TS-клиент/zod регенерируются (`gen:check`). Перечисление субъектов
отражает `DISTINCT subject` модели (субъекты без ролей не возвращаются).

#### Scenario: Успешное чтение каталога

- **GIVEN** субъект с правом `(read, iam:global)`
- **WHEN** выполняется `GET /iam/roles` (и `GET /iam/subjects`)
- **THEN** gateway после `CheckAccess` проксирует в IDM и возвращает `200` с
  ролями (и страницей субъектов с ролями + `next_page_token`), без раскрытия
  внутренних деталей

#### Scenario: Нет права read → 403 (fail-closed)

- **GIVEN** субъект без права `(read, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется любой `GET /iam/*`
- **THEN** gateway отвечает `403`, запрос в IDM-чтение не проксируется, деталь —
  только в лог по ключу slog `err`

#### Scenario: Права несуществующей роли → 404

- **GIVEN** роли `no-such-role` нет
- **WHEN** выполняется `GET /iam/roles/no-such-role/permissions`
- **THEN** gateway отвечает `404` (из `NotFound`), без внутренних деталей

### Requirement: REST-ручки назначения и снятия роли субъекту

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/subjects/{subject}/roles/{role}`
(назначить роль) и `DELETE /iam/subjects/{subject}/roles/{role}` (снять роль),
проксируемые в `RoleAdminService.AssignRole`/`RevokeRole`. gateway ДОЛЖЕН (MUST)
ПЕРЕД проксированием вызывать `CheckAccess` с `resource="iam:global"`,
`action="write"` (fail-closed → 403). Обе ручки ДОЛЖНЫ (MUST) быть идемпотентными:
повторное назначение уже имеющейся роли и снятие отсутствующей связки возвращают
`200`. Ответ ДОЛЖЕН (MUST) нести актуальный набор ролей субъекта
(`{subject, roles[]}`), а не пустое тело (для рантайм-валидации zod и обновления
UI). Несуществующая роль при назначении → `404` (из `NotFound`); пустые
`subject`/`role` → `400` (из `InvalidArgument`). Конкурентный конфликт состояния не
возникает (операции идемпотентны), поэтому `409`/`422` для этих ручек НЕ
применяются. Внутренние ошибки наружу НЕ раскрываются.

#### Scenario: Успешное назначение роли

- **GIVEN** субъект с правом `(write, iam:global)`, роль `iam-admin` существует
- **WHEN** выполняется `POST /iam/subjects/alice/roles/iam-admin`
- **THEN** роль назначается, возвращается `200` с актуальным набором ролей
  субъекта `alice`

#### Scenario: Идемпотентный повтор назначения и снятия

- **GIVEN** у субъекта `alice` уже есть роль `iam-admin`
- **WHEN** повторно `POST .../roles/iam-admin`, затем дважды
  `DELETE .../roles/iam-admin`
- **THEN** каждый вызов возвращает `200` с актуальным набором ролей (без ошибки)

#### Scenario: Нет права write → 403 (fail-closed)

- **GIVEN** субъект без права `(write, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется `POST`/`DELETE /iam/subjects/{subject}/roles/{role}`
- **THEN** gateway отвечает `403`, мутация не проксируется, деталь — только в лог

#### Scenario: Несуществующая роль при назначении → 404

- **GIVEN** роли `no-such-role` нет
- **WHEN** выполняется `POST /iam/subjects/alice/roles/no-such-role`
- **THEN** gateway отвечает `404` (из `NotFound`), привязка не создаётся

#### Scenario: Пустой subject или role → 400

- **WHEN** выполняется мутация с пустым `subject` или `role`
- **THEN** gateway отвечает `400` (из `InvalidArgument`), без раскрытия деталей

### Requirement: REST-ручки создания и удаления роли

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять `POST /iam/roles` (создать роль,
тело `{name}`) и `DELETE /iam/roles/{role}` (удалить роль), проксируемые в
`IamCatalogService.CreateRole`/`DeleteRole`. gateway ДОЛЖЕН (MUST) ПЕРЕД
проксированием вызывать `CheckAccess` с `resource="iam:global"`, `action="manage"`
(fail-closed → 403). Создание возвращает `201` с `{name, system:false}`; дубль
имени → `409` (из `AlreadyExists`); пустое имя → `400`. Удаление пользовательской
роли возвращает `200` (каскадно снимает роль у носителей); несуществующая роль →
`404`; СИСТЕМНАЯ роль → `422` (из `FailedPrecondition`). Все возвращаемые коды
(200/201/400/403/404/409/422) ДОЛЖНЫ (MUST) быть документированы в OpenAPI
(summary + description + operationId + ВСЕ коды) — иначе Spectral и
Schemathesis-конформанс красные. Внутренние ошибки наружу НЕ раскрываются. TS-клиент
и zod, а также `web/public/openapi.yaml` регенерируются (`gen:check`).

#### Scenario: Успешное создание роли

- **GIVEN** субъект с правом `(manage, iam:global)`, роли `reviewers` нет
- **WHEN** выполняется `POST /iam/roles` с телом `{"name":"reviewers"}`
- **THEN** возвращается `201` с `{name:"reviewers", system:false}`

#### Scenario: Дубль имени роли → 409

- **GIVEN** роль `reviewers` уже существует
- **WHEN** выполняется `POST /iam/roles` с `{"name":"reviewers"}`
- **THEN** возвращается `409` (из `AlreadyExists`), без раскрытия внутренних деталей

#### Scenario: Удаление системной роли → 422

- **GIVEN** роль `iam-admin` системная
- **WHEN** выполняется `DELETE /iam/roles/iam-admin`
- **THEN** возвращается `422` (из `FailedPrecondition`), роль не удаляется

#### Scenario: Удаление несуществующей роли → 404

- **WHEN** выполняется `DELETE /iam/roles/no-such-role`
- **THEN** возвращается `404` (из `NotFound`)

#### Scenario: Нет права manage → 403 (fail-closed)

- **GIVEN** субъект без `(manage, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется `POST`/`DELETE /iam/roles*`
- **THEN** gateway отвечает `403`, мутация не проксируется, деталь — только в лог

### Requirement: REST-ручки правки набора прав роли (attach/detach)

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/roles/{role}/permissions` (attach,
тело `{action,resource}`) и `DELETE /iam/roles/{role}/permissions?action=&resource=`
(detach, пара в query-параметрах), проксируемые в
`IamCatalogService.AttachPermission`/`DetachPermission` под `CheckAccess(manage,
iam:global)` (fail-closed → 403). Обе ручки ДОЛЖНЫ (MUST) быть ИДЕМПОТЕНТНЫМИ:
повторный attach уже привязанного и detach непривязанного → `200`. Ответ ДОЛЖЕН
(MUST) нести актуальный набор прав роли (`{role, permissions[]}`) для рантайм-
валидации zod и обновления UI. Несуществующая роль или (для attach) несуществующее
право → `404`; СИСТЕМНАЯ роль → `422`; пустые/битые поля → `400`. Все коды
(200/400/403/404/422) документируются в OpenAPI. Внутренние ошибки наружу НЕ
раскрываются.

#### Scenario: Успешный attach права к роли

- **GIVEN** субъект с `(manage, iam:global)`, пользовательская роль `reviewers` и
  право `(read, iam:global)` существуют
- **WHEN** выполняется `POST /iam/roles/reviewers/permissions` с
  `{"action":"read","resource":"iam:global"}`
- **THEN** возвращается `200` с актуальным набором прав роли `reviewers`

#### Scenario: Идемпотентный повтор attach/detach

- **GIVEN** право уже привязано к роли `reviewers`
- **WHEN** повторно `POST .../permissions`, затем дважды `DELETE
  .../permissions?action=read&resource=iam:global`
- **THEN** каждый вызов возвращает `200` с актуальным набором прав (без ошибки)

#### Scenario: Attach/detach на системную роль → 422

- **GIVEN** роль `iam-admin` системная
- **WHEN** выполняется `POST`/`DELETE /iam/roles/iam-admin/permissions`
- **THEN** возвращается `422` (из `FailedPrecondition`)

#### Scenario: Attach несуществующего права → 404

- **GIVEN** роль `reviewers` существует, права `(x,y)` нет
- **WHEN** выполняется `POST /iam/roles/reviewers/permissions` с `{"action":"x",
  "resource":"y"}`
- **THEN** возвращается `404` (из `NotFound`), право не создаётся неявно

#### Scenario: Невалидное тело attach → 400

- **WHEN** выполняется `POST /iam/roles/reviewers/permissions` с пустыми полями
- **THEN** возвращается `400` (из `InvalidArgument`)

### Requirement: REST-ручки создания и удаления права

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/permissions` (создать право, тело
`{action,resource}`) и `DELETE /iam/permissions?action=&resource=` (удалить),
проксируемые в `IamCatalogService.CreatePermission`/`DeletePermission` под
`CheckAccess(manage, iam:global)` (fail-closed → 403). Создание возвращает `201`
с `{action, resource, system:false}`; дубль пары → `409`; пустые/битые поля →
`400`. Удаление пользовательского права → `200` (каскадно снимает связки с ролями);
несуществующее → `404`; СИСТЕМНОЕ → `422`. Все коды (200/201/400/403/404/409/422)
документируются в OpenAPI. Внутренние ошибки наружу НЕ раскрываются.

#### Scenario: Успешное создание права

- **GIVEN** субъект с `(manage, iam:global)`, пары `(deploy, project:demo)` нет
- **WHEN** выполняется `POST /iam/permissions` с
  `{"action":"deploy","resource":"project:demo"}`
- **THEN** возвращается `201` с `{action, resource, system:false}`

#### Scenario: Дубль пары права → 409

- **GIVEN** право `(read, iam:global)` уже существует
- **WHEN** выполняется `POST /iam/permissions` с `{"action":"read",
  "resource":"iam:global"}`
- **THEN** возвращается `409` (из `AlreadyExists`)

#### Scenario: Удаление системного права → 422

- **GIVEN** право `(read, iam:global)` системное
- **WHEN** выполняется `DELETE /iam/permissions?action=read&resource=iam:global`
- **THEN** возвращается `422` (из `FailedPrecondition`)

#### Scenario: Удаление несуществующего права → 404

- **WHEN** выполняется `DELETE /iam/permissions?action=x&resource=y`
- **THEN** возвращается `404` (из `NotFound`)

### Requirement: REST-ручка поиска субъектов в справочнике

Периметр (ADR-0009) ДОЛЖЕН (MUST) предоставлять `GET /iam/directory/subjects?search=
&cursor=&page_size=` (поиск/листинг пользователей каталога Keycloak), проксируемую в
`IdentityService.SearchSubjects`. gateway ДОЛЖЕН (MUST) ПЕРЕД проксированием вызывать
`CheckAccess` с `resource="iam:directory"`, `action="read"` (fail-closed → 403).
Успех возвращает `200` с `{subjects:[SubjectIdentity], next}`; пустой/слишком
короткий `search` или некорректный `page_size`/`cursor` → `400`; отказ авторизации
ИЛИ недоступность IDM → `403`; недоступность Keycloak → `503` (деградация, retryable).
Курсор периметра ДОЛЖЕН (MUST) быть непрозрачным (поверх offset Keycloak). Все
возвращаемые коды (200/400/403/503) ДОЛЖНЫ (MUST) быть документированы в OpenAPI
(summary + description + operationId + ВСЕ коды) — иначе Spectral и Schemathesis-
конформанс красные. Внутренние ошибки и секреты наружу НЕ раскрываются. TS-клиент,
zod и `web/public/openapi.yaml` регенерируются (`gen:check`).

#### Scenario: Успешный поиск

- **GIVEN** субъект с `(read, iam:directory)`, Keycloak доступен
- **WHEN** выполняется `GET /iam/directory/subjects?search=iv&page_size=20`
- **THEN** возвращается `200` с `{subjects:[...], next}` (идентичности с `found=true`)

#### Scenario: Нет права directory → 403 (fail-closed)

- **GIVEN** субъект без `(read, iam:directory)` ИЛИ IDM недоступен
- **WHEN** выполняется `GET /iam/directory/subjects?search=iv`
- **THEN** gateway отвечает `403`, запрос к справочнику не проксируется, деталь — в лог

#### Scenario: Пустой/битый поисковый ввод → 400

- **WHEN** выполняется `GET /iam/directory/subjects` без `search` или с пустым/
  слишком коротким `search`, либо с некорректным `page_size`
- **THEN** возвращается `400` (из `InvalidArgument`), обращения к Keycloak нет

#### Scenario: Каталог недоступен → 503 (деградация)

- **GIVEN** субъект с `(read, iam:directory)`, но Keycloak недоступен
- **WHEN** выполняется `GET /iam/directory/subjects?search=iv`
- **THEN** возвращается `503` (из `Unavailable`), без сырых деталей; управление
  ролями по сырому subject остаётся доступным

### Requirement: REST-ручка батч-резолва субъектов

Периметр ДОЛЖЕН (MUST) предоставлять `POST /iam/directory/subjects/resolve` (тело
`{subjects:[...]}`), проксируемую в `IdentityService.ResolveSubjects` под
`CheckAccess(read, iam:directory)` (fail-closed → 403). Успех возвращает `200` с
`{subjects:[SubjectIdentity]}`, где отсутствующие в каталоге субъекты помечены
`found=false` (не опускаются). Пустой/битый список → `400`; недоступность Keycloak →
`503`. Все коды (200/400/403/503) документируются в OpenAPI. Внутренние ошибки
наружу НЕ раскрываются.

#### Scenario: Успешный батч-резолв с осиротевшим

- **GIVEN** субъект с `(read, iam:directory)`; `sub1` есть в каталоге, `sub2` — нет
- **WHEN** выполняется `POST /iam/directory/subjects/resolve` с `{subjects:[sub1,sub2]}`
- **THEN** возвращается `200`; `sub1` с `found=true` и именами, `sub2` с `found=false`

#### Scenario: Пустой список → 400

- **WHEN** выполняется `POST /iam/directory/subjects/resolve` с пустым `subjects`
- **THEN** возвращается `400` (из `InvalidArgument`)

#### Scenario: Каталог недоступен → 503

- **GIVEN** субъект с `(read, iam:directory)`, Keycloak недоступен
- **WHEN** выполняется `POST /iam/directory/subjects/resolve` с непустым списком
- **THEN** возвращается `503` (из `Unavailable`)

### Requirement: Обогащение GET /iam/subjects идентичностями (композиция в gateway)

gateway ДОЛЖЕН (MUST) обогащать ответ существующей ручки `GET /iam/subjects`
(subjects-with-roles) идентичностями: после получения списка от
`IamAdminService.ListSubjectsWithRoles` (под `(read, iam:global)`, как ADR-0014) он
ДОЛЖЕН (MUST), ЕСЛИ вызывающий ДОПОЛНИТЕЛЬНО держит `(read, iam:directory)`, вызвать
`IdentityService.ResolveSubjects` и слить идентичности в ответ. Без права
`(read, iam:directory)` ответ ДОЛЖЕН (MUST) оставаться «сырым» (текущее поведение
ADR-0014, без PII). «Осиротевшие» субъекты ДОЛЖНЫ (MUST) помечаться `found=false`.
Недоступность Keycloak при резолве НЕ ДОЛЖНА (MUST NOT) ломать ручку: список ролей
отдаётся `200` без идентичностей (деградация). Поля идентичности в схеме ответа
OpenAPI ДОЛЖНЫ (MUST) быть аддитивными/опциональными; zod-поля — optional.

#### Scenario: Обогащённый список при наличии обоих прав

- **GIVEN** субъект держит `(read, iam:global)` и `(read, iam:directory)`, Keycloak
  доступен
- **WHEN** выполняется `GET /iam/subjects`
- **THEN** возвращается `200`; субъекты с ролями обогащены `username`/`email`/
  `display_name`/`enabled`, `found=true`

#### Scenario: Сырой список без права directory

- **GIVEN** субъект держит только `(read, iam:global)`
- **WHEN** выполняется `GET /iam/subjects`
- **THEN** возвращается `200` без полей идентичности (PII не раскрывается)

#### Scenario: Деградация при недоступном Keycloak

- **GIVEN** субъект держит оба права, но Keycloak недоступен
- **WHEN** выполняется `GET /iam/subjects`
- **THEN** возвращается `200` со списком ролей без идентичностей; ручка не падает в 503

#### Scenario: Осиротевший субъект в списке

- **GIVEN** субъект `sub` есть в `subject_roles`, но в каталоге Keycloak его нет
- **WHEN** выполняется `GET /iam/subjects` (с правом directory)
- **THEN** субъект присутствует как raw `sub` с `found=false`

