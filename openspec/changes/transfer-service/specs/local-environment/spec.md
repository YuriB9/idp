# local-environment Specification (delta)

## ADDED Requirements

### Requirement: Seed второго проекта и прав переноса для сквозного сценария

Локальное окружение ДОЛЖНО (MUST) сидировать ресурсы для сквозного переноса
`demo→demo2` при включённом RBAC обратимой миграцией goose IDM: роль
`owner:project:demo2` для второго демо-проекта `project:demo2`, право
`(transfer, project:demo)` и право `(transfer_in, project:demo2)` субъекту
`demo-user` (= `AUTH_DISABLED_SUBJECT`). Миграции ДОЛЖНЫ (MUST) применяться через
`migrate-idm` (goose, `GOWORK=off`, пин `./tools`); `Down` ДОЛЖЕН (MUST) снимать
добавленные гранты/роль без остаточных объектов.

#### Scenario: Сквозной перенос при включённом RBAC

- **GIVEN** локальный стенд (docker-compose) с включённым RBAC и применёнными
  миграциями IDM (`project:demo2`, права `transfer`/`transfer_in`)
- **WHEN** `demo-user` переносит активный сервис из `project:demo` в `project:demo2`
  через портал/REST
- **THEN** `CheckAccess(transfer, project:demo)` и `CheckAccess(transfer_in,
  project:demo2)` возвращают `allowed=true`, и сценарий проходит до завершения
  переноса (сервис в `project:demo2`, статус `active`)

#### Scenario: Откат seed-миграции

- **GIVEN** применённая seed-миграция прав переноса и проекта `project:demo2`
- **WHEN** выполняется `goose down`
- **THEN** гранты `(transfer, project:demo)`/`(transfer_in, project:demo2)` и роль
  `owner:project:demo2` снимаются без остаточных объектов
