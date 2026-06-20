# local-environment Specification (delta)

## ADDED Requirements

### Requirement: Seed права decommission для сквозного сценария

Локальное окружение ДОЛЖНО (MUST) сидировать право `(decommission, project:demo)`
субъекту `demo-user` (= `AUTH_DISABLED_SUBJECT`) обратимой миграцией goose IDM,
чтобы сквозной сценарий вывода из эксплуатации проходил при включённом RBAC.
Миграции ДОЛЖНЫ (MUST) применяться через `migrate-idm` (goose, `GOWORK=off`, пин
`./tools`); `Down` ДОЛЖЕН (MUST) снимать добавленный грант.

#### Scenario: Сквозной сценарий при включённом RBAC

- **GIVEN** локальный стенд (docker-compose) с включённым RBAC и применёнными
  миграциями IDM
- **WHEN** `demo-user` выводит активный сервис `project:demo` из эксплуатации через
  портал/REST
- **THEN** `CheckAccess(decommission, project:demo)` возвращает `allowed=true`, и
  сценарий проходит до статуса `decommissioned`

#### Scenario: Откат seed-миграции

- **GIVEN** применённая seed-миграция права `decommission`
- **WHEN** выполняется `goose down`
- **THEN** грант `(decommission, project:demo)` для `demo-user` снимается без
  остаточных объектов
