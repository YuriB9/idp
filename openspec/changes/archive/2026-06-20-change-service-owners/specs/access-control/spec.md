# access-control Specification (delta)

## ADDED Requirements

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
