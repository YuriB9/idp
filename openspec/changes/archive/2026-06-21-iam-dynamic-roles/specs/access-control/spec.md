# access-control Specification (delta)

## ADDED Requirements

### Requirement: Привилегированное действие manage на ресурсе iam:global

Модель доступа ДОЛЖНА (MUST) поддерживать ТРЕТЬЕ горизонтальное действие
IAM-админки `manage` на ресурсе `iam:global` как отдельное право, проверяемое
через `CheckAccess` (strict-match, deny-by-default). Действие `manage`
авторизует ВСЕ структурные мутации каталога (создание/удаление ролей и прав,
attach/detach прав роли) и СТРОГО привилегированнее `write` (assign/revoke): менять
сам каталог прав опаснее, чем назначить существующую роль. Право `manage` ДОЛЖНО
(MUST) выдаваться роли через существующую модель `permissions`/`role_permissions`.
Ни `manage`, ни `write`, ни `read` НЕ ДОЛЖНЫ (MUST NOT) неявно наследоваться друг
из друга в модели: `manage` не подразумевает `write`/`read`, и наоборот. Отсутствие
явного гранта `(manage, iam:global)` ДОЛЖНО (MUST) приводить к `allowed=false`.
Гранты роли админки выдаются явно (сидированием).

#### Scenario: Грант manage разрешает структурные мутации

- **GIVEN** субъект имеет грант `(manage, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "manage")`
- **THEN** возвращается `allowed=true`

#### Scenario: Грант write не даёт manage (нет неявного наследования)

- **GIVEN** субъект имеет `(read, iam:global)` и `(write, iam:global)`, но не
  `(manage, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "manage")`
- **THEN** возвращается `allowed=false` — `write` не подразумевает `manage`

#### Scenario: Грант manage не даёт write/read неявно

- **GIVEN** субъект имеет только `(manage, iam:global)`
- **WHEN** вызывается `CheckAccess(subject, "iam:global", "write")` или `"read"`
- **THEN** возвращается `allowed=false` — `manage` не наследует `write`/`read` в
  модели (гранты выдаются явно)

### Requirement: Широкая инвалидация кэша при структурных мутациях каталога

Структурные мутации каталога IAM ДОЛЖНЫ (MUST) инвалидировать кэш решений ШИРОКО
(создание/удаление ролей и прав, attach/detach прав роли) — инкрементом поколения
`idm:cache:gen` (`InvalidateAll`), поскольку правка `role_permissions`/удаление
роли/права затрагивает решения ВСЕХ носителей затронутой роли. Точечная инвалидация
по субъекту (`InvalidateSubject`) для структурных мутаций НЕ ДОЛЖНА (MUST NOT)
использоваться вместо широкой (она оставила бы устаревший `allow`/`deny` у прочих
носителей). Точечная инвалидация остаётся прерогативой `AssignRole`/`RevokeRole`.
Читающие методы каталога НЕ ДОЛЖНЫ (MUST NOT) трогать кэш.

#### Scenario: Правка role_permissions инвалидирует всех носителей

- **GIVEN** роль `reviewers` назначена нескольким субъектам, их решения закэшированы
- **WHEN** к роли attach/detach право
- **THEN** поколение `idm:cache:gen` бампится (широкая инвалидация), у всех
  носителей роли не остаётся устаревший `allow`/`deny`

#### Scenario: Чтение каталога не трогает кэш

- **WHEN** вызываются любые читающие методы каталога IAM
- **THEN** состояние кэша решений (ключи/поколение) не изменяется

### Requirement: Авторизация структурных мутаций каталога в gateway (fail-closed)

gateway ДОЛЖЕН (MUST) ПЕРЕД проксированием КАЖДОЙ структурной мутирующей
`/iam`-ручки вызывать `CheckAccess` с `resource="iam:global"`, `action="manage"`
(через существующий обобщённый helper `authorizeResource`). При отказе ИЛИ
недоступности/ошибке IDM gateway ДОЛЖЕН (MUST) отвечать `403` (fail-closed) и НЕ
проксировать мутацию в `IamCatalogService`; внутренние детали наружу НЕ
раскрываются (деталь — в лог по ключу slog `err`). Существующие read-ручки (под
`read`) и assign/revoke (под `write`) НЕ ДОЛЖНЫ (MUST NOT) затрагиваться (без
регресса ADR-0014).

#### Scenario: Структурная мутация под manage

- **GIVEN** субъект с грантом `(manage, iam:global)`
- **WHEN** выполняется `POST /iam/roles` (или любая структурная мутация)
- **THEN** `CheckAccess(subject, "iam:global", "manage")` зовётся первым, при
  `allowed=true` мутация проксируется в IDM

#### Scenario: Нет права manage → 403 (fail-closed)

- **GIVEN** субъект без `(manage, iam:global)` ИЛИ IDM недоступен
- **WHEN** выполняется любая структурная мутация `/iam`
- **THEN** gateway отвечает `403`, мутация не проксируется, деталь — только в лог

#### Scenario: Нет регресса read/write-ручек

- **GIVEN** субъект с `(read|write, iam:global)`, но без `manage`
- **WHEN** он читает каталог и выполняет assign/revoke
- **THEN** read- и write-ручки работают как прежде (ADR-0014), структурные мутации
  ему недоступны (403)
