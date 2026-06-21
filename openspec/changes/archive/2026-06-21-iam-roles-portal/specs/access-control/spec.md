# access-control Specification (delta)

## ADDED Requirements

### Requirement: Действия read и write на ресурсе iam:global

Модель доступа ДОЛЖНА (MUST) поддерживать ДВА горизонтальных действия IAM-админки
как отдельные права, проверяемые через `CheckAccess`: `read` на ресурсе
`iam:global` (просмотр каталога ролей/прав/субъектов) и `write` на ресурсе
`iam:global` (назначение/снятие ролей субъектам). Оба права ДОЛЖНЫ (MUST)
выдаваться роли через существующую модель `permissions`/`role_permissions`
(strict-match, deny-by-default); отсутствие явного гранта ДОЛЖНО (MUST) приводить к
`allowed=false`. Ни одно из действий НЕ ДОЛЖНО (MUST NOT) неявно наследоваться:
`write` не подразумевает `read`, project-действия (`create`/`read`/`list`/
`transfer`/…) на `project:<p>` не дают прав на `iam:global` и наоборот. Ресурс
`iam:global` — горизонтальный (не привязан к проекту), отражает глобальный характер
админки.

#### Scenario: Грант read разрешает чтение каталога IAM

- **GIVEN** субъект имеет грант `(read, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "read")`
- **THEN** возвращается `allowed=true`

#### Scenario: Нет гранта write — мутация запрещена

- **GIVEN** субъект имеет только `(read, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "write")`
- **THEN** возвращается `allowed=false`, без неявного наследования из `read`

#### Scenario: Кэш решений учитывает действия iam:global

- **GIVEN** решения по `(read|write, iam:global)` закэшированы в DragonflyDB
- **WHEN** грант субъекта меняется и вызывается `InvalidateSubject`
- **THEN** последующий `CheckAccess` отражает актуальный грант, без устаревшего
  `allow`/`deny`

### Requirement: Обобщённый authorize по произвольному ресурсу в gateway

gateway ДОЛЖЕН (MUST) предоставлять обобщённый helper авторизации, принимающий
произвольную строку `resource` (а не только `project:<p>`), и вызывать `CheckAccess`
с этим ресурсом ПЕРЕД доменной обработкой. Существующий project-scoped helper
`authorize(project, action)` ДОЛЖЕН (MUST) сохраниться как тонкая обёртка над
обобщённым (`resource = "project:" + project`), чтобы НЕ было регресса
существующих вызовов (create/list/read/owners/decommission/transfer). При отказе
ИЛИ недоступности/ошибке IDM helper ДОЛЖЕН (MUST) писать `403` (fail-closed) и НЕ
проксировать запрос; внутренние детали наружу НЕ раскрываются (деталь — в лог по
ключу slog `err`).

#### Scenario: Обобщённый helper авторизует по iam:global

- **GIVEN** субъект с грантом `(read, iam:global)`
- **WHEN** IAM-ручка вызывает обобщённый helper с `resource="iam:global"`,
  `action="read"`
- **THEN** `CheckAccess` зовётся именно с `iam:global`, при `allowed=true` обработка
  продолжается

#### Scenario: Нет регресса project-вызовов

- **GIVEN** субъект с грантом `(create, project:demo)`
- **WHEN** ручка создания сервиса вызывает `authorize("demo", "create")`
- **THEN** обёртка формирует `resource="project:demo"`, поведение идентично
  прежнему (без регресса)

#### Scenario: Недоступность IDM при авторизации → 403 (fail-closed)

- **GIVEN** IDM недоступен/возвращает ошибку
- **WHEN** любая ручка вызывает helper авторизации
- **THEN** helper отвечает `403`, запрос не проксируется, деталь — только в лог

### Requirement: Чтение каталога IAM без побочных эффектов на кэш

Читающие методы каталога IAM ДОЛЖНЫ (MUST) выполнять только чтение Postgres и НЕ
ДОЛЖНЫ (MUST NOT) изменять кэш решений DragonflyDB. Это касается `ListRoles`,
`ListPermissions`, `GetRolePermissions`, `ListSubjectsWithRoles`,
`GetSubjectRoles` (ни записи решений, ни инкремента поколения, ни инвалидации).
Инвалидация кэша остаётся прерогативой мутаций ролей (`AssignRole`/`RevokeRole` →
`InvalidateSubject` по затронутому субъекту).

#### Scenario: Чтение не трогает кэш

- **WHEN** вызываются любые читающие методы каталога IAM
- **THEN** состояние кэша решений (ключи/поколение) не изменяется

#### Scenario: Мутация инвалидирует кэш субъекта

- **WHEN** субъекту назначается/снимается роль через `RoleAdminService`
- **THEN** кэш решений по затронутому субъекту инвалидируется (`InvalidateSubject`),
  устаревший `allow`/`deny` не остаётся
