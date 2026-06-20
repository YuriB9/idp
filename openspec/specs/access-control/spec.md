# access-control Specification

## Purpose
Минимальный RBAC сервиса IDM: модель ролей/прав в Postgres, решение
`CheckAccess` (deny-by-default), кэш решений в DragonflyDB (TTL, singleflight,
инвалидация), поведение fail-closed и content-aware `/readyz`. Источник —
ADR-0003, ADR-0010, docs/IDP_MVP_plan.md (Этап 1, БЛОК 2, БЛОК 5).

## Requirements
### Requirement: Модель ролей и прав RBAC в Postgres

Сервис IDM ДОЛЖЕН (MUST) хранить модель RBAC в Postgres в виде нормализованных
таблиц: каталог ролей, каталог атомарных прав `(action, resource)`, связь
ролей с правами (many-to-many) и привязка субъектов к ролям. Схема ДОЛЖНА (MUST)
создаваться обратимыми миграциями goose (пара `Up`/`Down` в одном файле,
запуск `GOWORK=off`), а уникальность `(action, resource)` и пар связей ДОЛЖНА
(MUST) обеспечиваться на уровне БД (а не check-then-act). Право над ресурсом
ДОЛЖНО (MUST) сопоставляться по точному совпадению строки `resource` (без
wildcard в MVP).

#### Scenario: Применение и откат миграции

- **GIVEN** пустая база IDM
- **WHEN** применяется `goose up`, а затем `goose down`
- **THEN** `up` создаёт таблицы ролей/прав/связей/привязок, а `down` полностью
  снимает схему без остаточных объектов

#### Scenario: Уникальность права на уровне БД

- **GIVEN** право `(action=create, resource=project:demo)` уже существует
- **WHEN** предпринимается вставка такого же права
- **THEN** БД отклоняет дубликат по уникальному ограничению, без молчаливого
  дублирования

### Requirement: Решение CheckAccess по модели с deny-by-default

Сервис IDM ДОЛЖЕН (MUST) реализовать gRPC `AccessService.CheckAccess(subject,
resource, action)` так, что `allowed=true` возвращается ТОЛЬКО при наличии
цепочки «субъект → роль → право», где право соответствует запрошенным
`action` и `resource`. При отсутствии такой цепочки решение ДОЛЖНО (MUST) быть
`allowed=false` (deny-by-default). Пустые `subject`/`resource`/`action`
ДОЛЖНЫ (MUST) приводить к `codes.InvalidArgument`. Контракт `proto/idm/v1`
менять НЕ требуется.

#### Scenario: Happy-path — право выдано

- **GIVEN** субъект `u1` привязан к роли с правом `(create, project:demo)`
- **WHEN** вызывается `CheckAccess(u1, project:demo, create)`
- **THEN** ответ `allowed=true`

#### Scenario: Отказ — нет роли/права

- **GIVEN** субъект `u2` без привязок к ролям с правом `(create, project:demo)`
- **WHEN** вызывается `CheckAccess(u2, project:demo, create)`
- **THEN** ответ `allowed=false` с машинной `reason`, без раскрытия внутренних
  деталей

#### Scenario: Невалидный запрос

- **WHEN** вызывается `CheckAccess` с пустым `subject`, `resource` или `action`
- **THEN** возвращается `codes.InvalidArgument`, обращения к БД/кэшу не делается

### Requirement: Кэш решений в DragonflyDB с singleflight и инвалидацией

Сервис IDM ДОЛЖЕН (MUST) кэшировать результат `CheckAccess` в DragonflyDB по
ключу, производному от `(subject, resource, action)`, с конечным TTL (включая
negative caching отказов). Чтение БД при промахе кэша ДОЛЖНО (MUST) быть
защищено `singleflight` так, что N одновременных одинаковых промахов порождают
РОВНО один запрос в Postgres (anti-stampede, БЛОК 5). При изменении ролей/прав/
привязок кэш ДОЛЖЕН (MUST) инвалидироваться так, чтобы устаревшие решения
становились недостижимыми.

#### Scenario: Кэш-промах заполняет кэш

- **GIVEN** пустой кэш и право, разрешающее `CheckAccess(u1, project:demo, create)`
- **WHEN** выполняется первый вызов
- **THEN** решение читается из Postgres и записывается в кэш с TTL, ответ
  `allowed=true`

#### Scenario: Кэш-попадание не идёт в БД

- **GIVEN** решение для `(u1, project:demo, create)` уже в кэше
- **WHEN** выполняется повторный вызов до истечения TTL
- **THEN** ответ берётся из кэша без запроса в Postgres

#### Scenario: Singleflight против stampede

- **GIVEN** пустой кэш
- **WHEN** одновременно приходит N одинаковых вызовов `CheckAccess` (одинаковый
  ключ)
- **THEN** к Postgres уходит ровно один запрос, и все вызовы получают одинаковое
  решение

#### Scenario: Инвалидация при изменении ролей

- **GIVEN** в кэше закэшировано решение для субъекта `u1`
- **WHEN** меняется состав ролей/прав, затрагивающий `u1`
- **THEN** ранее закэшированное решение становится недостижимым, и следующий
  вызов пересчитывается по актуальной модели

### Requirement: Поведение fail-closed при недоступности зависимостей

Сервис IDM ДОЛЖЕН (MUST) быть fail-closed: при недоступности Postgres или иной
невозможности установить разрешение решение ДОЛЖНО (MUST) быть отказом
(`allowed=false`), а не молчаливым разрешением. Ошибка кэша НЕ ДОЛЖНА (MUST NOT)
приводить к разрешению: при недоступном DragonflyDB сервис ДОЛЖЕН (MUST)
деградировать к чтению Postgres (корректность), и только при недоступной БД —
отказывать. Внутренние ошибки НЕ ДОЛЖНЫ (MUST NOT) раскрываться клиенту
(никакого `err.Error()` наружу); детали пишутся в лог по ключу slog `err`.

#### Scenario: Недоступна БД — отказ

- **GIVEN** Postgres недоступен и решения нет в кэше
- **WHEN** вызывается `CheckAccess`
- **THEN** возвращается отказ (`allowed=false` либо ошибка-статус), доступ не
  предоставляется молча, детали ошибки наружу не раскрываются

#### Scenario: Недоступен кэш — деградация к БД, не обход

- **GIVEN** DragonflyDB недоступен, но Postgres доступен
- **WHEN** вызывается `CheckAccess`
- **THEN** решение вычисляется напрямую по Postgres (без молчаливого
  разрешения), результат корректен

### Requirement: Content-aware готовность IDM

Сервис IDM ДОЛЖЕН (MUST) предоставлять content-aware `/readyz`, выполняющий
реальный пинг Postgres И DragonflyDB. Если любая из зависимостей недоступна,
`/readyz` ДОЛЖЕН (MUST) возвращать неуспех (чтобы k8s не направлял трафик), а
`/healthz` ДОЛЖЕН (MUST) отражать живость процесса независимо от зависимостей.

#### Scenario: Зависимость недоступна — not ready

- **GIVEN** Postgres или DragonflyDB недоступны
- **WHEN** опрашивается `/readyz`
- **THEN** ответ неуспешен, и трафик на сервис не направляется

#### Scenario: Все зависимости доступны — ready

- **GIVEN** Postgres и DragonflyDB доступны
- **WHEN** опрашивается `/readyz`
- **THEN** ответ успешен

### Requirement: Программная выдача и отзыв ролей с инвалидацией кэша

Сервис IDM ДОЛЖЕН (MUST) предоставлять программный путь выдачи и отзыва роли
субъекту через управляющий gRPC (`AssignRole`/`RevokeRole`), записывающий/
снимающий привязку в таблице `subject_roles`. Операции ДОЛЖНЫ (MUST) быть
идемпотентными (повторная выдача имеющейся роли и отзыв отсутствующей —
успешны, без дублей; уникальность пары `(subject, role)` обеспечивается БД).
После любого изменения привязок IDM ДОЛЖЕН (MUST) инвалидировать кэш решений по
затронутому субъекту (`InvalidateSubject`) или поколением, чтобы устаревшие
allow/deny стали недостижимыми. Несуществующая роль ДОЛЖНА (MUST) приводить к
`codes.NotFound` (без создания «висячих» привязок), пустые `subject`/`role` —
к `codes.InvalidArgument`. Управляющий путь ДОЛЖЕН (MUST) быть защищён
(не публичный периметр; вызывается доменным потоком/админ-путём).

#### Scenario: Выдача роли инвалидирует кэш субъекта

- **GIVEN** субъект `bob` без роли владельца и закэшированное решение
  `CheckAccess(bob, project:demo, read) = deny`
- **WHEN** вызывается `AssignRole(bob, owner:project:demo)`
- **THEN** привязка появляется в `subject_roles`, кэш по `bob` инвалидируется, и
  следующий `CheckAccess(bob, project:demo, read)` пересчитывается как `allow`

#### Scenario: Отзыв роли инвалидирует кэш субъекта

- **GIVEN** субъект `carol` с ролью `owner:project:demo` и закэшированным `allow`
- **WHEN** вызывается `RevokeRole(carol, owner:project:demo)`
- **THEN** привязка снимается, кэш по `carol` инвалидируется, следующий
  `CheckAccess` пересчитывается как `deny`

#### Scenario: Идемпотентность управляющих операций

- **GIVEN** субъект `bob` уже имеет роль `owner:project:demo`
- **WHEN** повторно вызывается `AssignRole(bob, owner:project:demo)`
- **THEN** результат успешен, дубликат привязки не создаётся (уникальность БД)

#### Scenario: Невалидные аргументы и несуществующая роль

- **WHEN** вызывается `AssignRole` с пустым `subject`/`role` или с несуществующей
  ролью
- **THEN** возвращается `codes.InvalidArgument` (пустые поля) либо
  `codes.NotFound` (роль отсутствует), привязка не создаётся

### Requirement: RBAC-операция change_owners

Модель RBAC ДОЛЖНА (MUST) поддерживать действие `change_owners` над ресурсом
`project:<project>` как самостоятельное атомарное право `(change_owners,
project:<project>)`. Решение `CheckAccess(subject, project:<project>,
change_owners)` ДОЛЖНО (MUST) подчиняться deny-by-default и strict-match
ресурса (как и прочие действия); наличие права на иное действие НЕ ДОЛЖНО (MUST
NOT) давать неявно право `change_owners`.

#### Scenario: Право change_owners выдано

- **GIVEN** субъект `demo-user` привязан к роли с правом `(change_owners,
  project:demo)`
- **WHEN** вызывается `CheckAccess(demo-user, project:demo, change_owners)`
- **THEN** ответ `allowed=true`

#### Scenario: Нет права change_owners — отказ

- **GIVEN** субъект имеет только право `(create, project:demo)`
- **WHEN** вызывается `CheckAccess(subject, project:demo, change_owners)`
- **THEN** ответ `allowed=false` (право `create` не даёт `change_owners`)

### Requirement: Операция decommission в модели RBAC

Модель доступа ДОЛЖНА (MUST) поддерживать действие `decommission` на ресурсе
`project:<project>` как отдельное право, проверяемое через `CheckAccess`. Право
ДОЛЖНО (MUST) выдаваться роли через существующую модель `permissions`/
`role_permissions` (strict-match, deny-by-default); отсутствие явного гранта
`decommission` ДОЛЖНО (MUST) приводить к `allowed=false`. Для локального сквозного
сценария право `(decommission, project:demo)` ДОЛЖНО (MUST) сидироваться субъекту
`demo-user` обратимой миграцией goose.

#### Scenario: Грант decommission разрешает операцию

- **GIVEN** субъект `demo-user` имеет грант `(decommission, project:demo)`
- **WHEN** вызывается `CheckAccess(demo-user, decommission, project:demo)`
- **THEN** возвращается `allowed=true`

#### Scenario: Отсутствие гранта запрещает операцию (deny-by-default)

- **GIVEN** субъект без гранта `(decommission, project:demo)`
- **WHEN** вызывается `CheckAccess(subject, decommission, project:demo)`
- **THEN** возвращается `allowed=false`, без неявного наследования из других
  действий (`read`/`change_owners` право `decommission` не дают)

#### Scenario: Кэш решений учитывает новое действие

- **GIVEN** решение по `(decommission, project:demo)` закэшировано в DragonflyDB
- **WHEN** грант субъекта меняется и вызывается инвалидация (`InvalidateSubject`/
  поколение)
- **THEN** последующий `CheckAccess` отражает актуальный грант, без устаревшего
  `allow`/`deny`
