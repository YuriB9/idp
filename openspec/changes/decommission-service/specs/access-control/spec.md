# access-control Specification (delta)

## ADDED Requirements

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
