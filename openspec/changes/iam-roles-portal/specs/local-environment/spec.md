# local-environment Specification (delta)

## ADDED Requirements

### Requirement: Seed права IAM-админки для сквозного сценария

Локальный стенд (docker-compose) ДОЛЖЕН (MUST) обратимой goose-миграцией IDM
засевать роль `iam-admin` с правами `(read, iam:global)` и `(write, iam:global)` и
выдавать её субъекту `demo-user` (совпадает с `AUTH_DISABLED_SUBJECT`), чтобы
раздел «Роли и доступы» работал при включённом RBAC. Миграция ДОЛЖНА (MUST) быть
идемпотентной (`ON CONFLICT DO NOTHING`) и иметь корректный `Down` (снять привязку,
права и роль). Изменение касается ТОЛЬКО локального стенда (не прод) и НЕ вводит
новых таблиц (используется существующая модель `roles`/`permissions`/
`role_permissions`/`subject_roles`).

#### Scenario: demo-user получает доступ к админке

- **GIVEN** применены миграции IDM, включая seed роли `iam-admin`
- **WHEN** `demo-user` открывает раздел «Роли и доступы»
- **THEN** `CheckAccess(demo-user, "iam:global", "read")` и
  `(write, iam:global)` возвращают `allowed=true`, раздел доступен на чтение и
  мутации

#### Scenario: Обратимость миграции

- **WHEN** выполняется `goose down` для seed-миграции `iam-admin`
- **THEN** привязка `demo-user`↔`iam-admin`, права `(read|write, iam:global)` и
  роль `iam-admin` удаляются, состояние модели возвращается к прежнему

#### Scenario: Идемпотентный повторный прогон

- **WHEN** seed-миграция применяется повторно
- **THEN** дублей роли/прав/привязок не возникает (`ON CONFLICT DO NOTHING`)
