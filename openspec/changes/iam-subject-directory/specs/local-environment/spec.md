# local-environment Specification (delta)

## ADDED Requirements

### Requirement: Сервис-аккаунт и демо-пользователи в realm-файле Keycloak

Локальный realm-файл `deploy/keycloak/idp-realm.json` ДОЛЖЕН (MUST) содержать
confidential-client сервис-аккаунта IDM с realm-management ролями `view-users` и
`query-users` (для чтения каталога) и несколько демо-пользователей (для проверки
поиска/резолва). Пользователю `dev` ДОЛЖЕН (MUST) быть задан ДЕТЕРМИНИРОВАННЫЙ `id`
(UUID), чтобы его `sub` был воспроизводим между импортами realm. Правки realm-файла
ДОЛЖНЫ (MUST) быть воспроизводимы при импорте realm на старте Keycloak. Секрет
сервис-аккаунта в локалке берётся из env (`SSRF_DISABLED` локально, адрес `keycloak`
приватный) и НЕ ДОЛЖЕН (MUST NOT) логироваться.

#### Scenario: Импорт realm воспроизводим

- **GIVEN** обновлённый `idp-realm.json` с клиентом сервис-аккаунта,
  детерминированным UUID `dev` и демо-пользователями
- **WHEN** Keycloak стартует и импортирует realm
- **THEN** появляются клиент сервис-аккаунта (с `view-users`/`query-users`),
  пользователь `dev` с фиксированным `sub` (UUID) и демо-пользователи; повторный
  импорт даёт тот же результат

#### Scenario: Сервис-аккаунт может читать каталог

- **GIVEN** IDM настроен на клиент сервис-аккаунта
- **WHEN** IDM получает токен по `client_credentials` и вызывает Admin REST
- **THEN** доступны поиск и резолв пользователей realm `idp`, секрет в логи не
  попадает

### Requirement: Сведение канонического ключа субъекта в локалке (миграции и env)

Локальное окружение ДОЛЖНО (MUST) быть приведено к каноническому ключу субъекта =
`sub` (UUID `dev`): `AUTH_DISABLED_SUBJECT` в docker-compose (gateway/projects)
ДОЛЖЕН (MUST) быть равен UUID `dev` (а не `demo-user`), а обратимая goose-миграция
IDM ДОЛЖНА (MUST) перенести сиды `subject_roles` со строки `demo-user` на UUID `dev`.
Отдельная обратимая миграция ДОЛЖНА (MUST) засеять право `(read, iam:directory)` роли
`iam-admin`. Миграции ДОЛЖНЫ (MUST) применяться через `migrate-idm` (пин `./tools`,
`GOWORK=off`) и иметь корректный `Down`.

#### Scenario: Локалка и Keycloak дают один subject

- **GIVEN** `AUTH_DISABLED_SUBJECT`=UUID `dev` и сиды RBAC мигрированы на этот UUID
- **WHEN** запрос идёт в disabled-режиме ИЛИ через реальный вход `dev` в Keycloak
- **THEN** `auth.Claims.Subject` в обоих случаях один и тот же UUID, под который
  засеяны роли `iam-admin`/`project-creator`

#### Scenario: Применение и откат миграций сидов

- **GIVEN** база IDM с сидами на `demo-user`
- **WHEN** применяется `goose up`, затем `goose down`
- **THEN** `up` переносит сиды на UUID `dev` и добавляет `(read, iam:directory)`
  роли `iam-admin`; `down` возвращает сиды на `demo-user` и снимает право, без
  остаточных объектов

#### Scenario: iam-admin получает доступ к справочнику

- **GIVEN** применена миграция seed `(read, iam:directory)`
- **WHEN** субъект с ролью `iam-admin` вызывает справочник
- **THEN** `CheckAccess(subject, iam:directory, read)` = allow, поиск/резолв доступны
