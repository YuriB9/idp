# access-control Specification (delta)

## ADDED Requirements

### Requirement: Действия transfer и transfer_in в модели RBAC

Модель доступа ДОЛЖНА (MUST) поддерживать ДВА действия переноса как отдельные
права, проверяемые через `CheckAccess`: `transfer` на ресурсе `project:<source>`
(право вынести сервис из исходного проекта) и `transfer_in` на ресурсе
`project:<target>` (право принять сервис в целевой проект). Оба права ДОЛЖНЫ (MUST)
выдаваться роли через существующую модель `permissions`/`role_permissions`
(strict-match, deny-by-default); отсутствие явного гранта ДОЛЖНО (MUST) приводить к
`allowed=false`. Ни одно из действий НЕ ДОЛЖНО (MUST NOT) неявно наследоваться из
других (`read`/`change_owners`/`decommission`/`create` право `transfer`/
`transfer_in` не дают). Для локального сквозного сценария права
`(transfer, project:demo)` и `(transfer_in, project:demo2)` ДОЛЖНЫ (MUST)
сидироваться субъекту `demo-user` обратимой миграцией goose.

#### Scenario: Гранты transfer+transfer_in разрешают операцию

- **GIVEN** субъект `demo-user` имеет гранты `(transfer, project:demo)` и
  `(transfer_in, project:demo2)`
- **WHEN** вызываются `CheckAccess(demo-user, transfer, project:demo)` и
  `CheckAccess(demo-user, transfer_in, project:demo2)`
- **THEN** обе проверки возвращают `allowed=true`

#### Scenario: Нет гранта transfer_in на target — запрет (deny-by-default)

- **GIVEN** субъект имеет `(transfer, project:demo)`, но не имеет
  `(transfer_in, project:demo2)`
- **WHEN** вызывается `CheckAccess(subject, transfer_in, project:demo2)`
- **THEN** возвращается `allowed=false`, без неявного наследования из `transfer`
  или иных действий

#### Scenario: Кэш решений учитывает новые действия

- **GIVEN** решения по `(transfer, project:demo)`/`(transfer_in, project:demo2)`
  закэшированы в DragonflyDB
- **WHEN** грант субъекта меняется и вызывается инвалидация (`InvalidateSubject`/
  поколение)
- **THEN** последующий `CheckAccess` отражает актуальный грант, без устаревшего
  `allow`/`deny`
