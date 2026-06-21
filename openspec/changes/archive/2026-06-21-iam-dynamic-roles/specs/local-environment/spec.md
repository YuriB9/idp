# local-environment Specification (delta)

## ADDED Requirements

### Requirement: Признак system для существующих сидированных ролей/прав

Локальный стенд (и любая среда) ДОЛЖЕН (MUST) обратимой goose-миграцией IDM ввести
колонки `roles.system` и `permissions.system` (`boolean NOT NULL DEFAULT false`) и
backfill'ом пометить `system=true` ВСЕ роли и права, существующие на момент
миграции (они сидированы миграциями: `project-creator`, `owner:project:*`,
`iam-admin` и их права). Миграция ДОЛЖНА (MUST) иметь корректный `Down`
(`DROP COLUMN system` у обеих таблиц), возвращающий схему к прежнему виду. Новые
роли/права, создаваемые через API после миграции, получают `system=false`.

#### Scenario: Backfill помечает сидированные системными

- **GIVEN** до миграции существуют роли `iam-admin`/`project-creator` и их права
- **WHEN** применяется миграция `0007_roles_permissions_system_flag.sql`
- **THEN** колонки `system` добавлены, все существующие роли/права получают
  `system=true`

#### Scenario: Обратимость миграции признака system

- **WHEN** выполняется `goose down` для миграции признака `system`
- **THEN** колонки `system` удаляются у `roles` и `permissions`, схема возвращается
  к прежнему виду

### Requirement: Seed права manage для роли iam-admin

Локальный стенд ДОЛЖЕН (MUST) обратимой goose-миграцией IDM засеять право
`(manage, iam:global)` (`system=true`) и привязать его к роли `iam-admin` (которую
уже имеет `demo-user` из миграции `0006`), чтобы раздел «Роли и доступы» позволял
структурные мутации каталога при включённом RBAC. Миграция ДОЛЖНА (MUST) быть
идемпотентной (`ON CONFLICT DO NOTHING`) и иметь корректный `Down` (снять привязку
и удалить право). Изменение касается ТОЛЬКО локального стенда (не прод) и НЕ вводит
новых таблиц.

#### Scenario: demo-user получает доступ к структурным мутациям

- **GIVEN** применены миграции IDM, включая seed права `(manage, iam:global)` роли
  `iam-admin`
- **WHEN** `demo-user` открывает раздел «Роли и доступы»
- **THEN** `CheckAccess(demo-user, "iam:global", "manage")` возвращает
  `allowed=true`, структурные мутации каталога доступны

#### Scenario: Обратимость seed права manage

- **WHEN** выполняется `goose down` для seed-миграции `manage`
- **THEN** привязка `(manage, iam:global)`↔`iam-admin` и само право удаляются,
  состояние модели возвращается к прежнему

#### Scenario: Идемпотентный повторный прогон

- **WHEN** seed-миграция применяется повторно
- **THEN** дублей права/привязки не возникает (`ON CONFLICT DO NOTHING`)
