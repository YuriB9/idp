# local-environment Specification

## Purpose
TBD - created by archiving change foundation-and-pkg. Update Purpose after archive.
## Requirements
### Requirement: docker-compose локалка платформы

`docker-compose` ДОЛЖЕН (MUST) поднимать полный локальный стенд: Keycloak,
Oauth2-Proxy, два экземпляра PostgreSQL (для projects и idm), DragonflyDB,
Temporal Server + UI, скелеты сервисов (gateway, idm, projects,
devinfra-worker), моки внешних систем GitLab/Vault/Harbor и портал (`./web`).
Стенд ДОЛЖЕН (MUST) обеспечивать сквозную визуальную проверку сценария
«Создание сервиса»: портал → gateway(REST) → projects(gRPC) → Temporal →
DevInfra worker (моки), с наблюдаемым переходом статуса `CREATING`→`ACTIVE`.
Портал ДОЛЖЕН (MUST) быть доступен в браузере (сервис `web` в compose ИЛИ явно
задокументированный `npm run dev` на `:3000` с прокси `/api` на gateway
`:8081`), а локальная аутентификация (`AUTH_DISABLED` у gateway / oauth2-proxy)
и CORS — согласованы так, чтобы запросы портала доходили до периметра.

#### Scenario: Полный стенд поднимается одной командой

- **WHEN** выполняется `docker compose up`
- **THEN** стартуют все перечисленные компоненты, и сервисы-скелеты становятся доступны для проверки health-эндпоинтов

#### Scenario: Отдельные базы для projects и idm

- **WHEN** инспектируется конфигурация compose
- **THEN** заданы два независимых PostgreSQL-инстанса — для каталога проектов и для IDM

#### Scenario: Портал доступен и проксирует периметр

- **GIVEN** поднятый локальный стенд
- **WHEN** открывается портал в браузере и отправляется доменный запрос на `/api/...`
- **THEN** запрос доходит до gateway (через прокси/oauth2-proxy) без CORS-ошибок,
  и портал получает ответ периметра

#### Scenario: Сквозное создание сервиса наблюдается визуально

- **GIVEN** поднятый стенд с моками GitLab/Vault/Harbor и DevInfra worker
- **WHEN** через портал создаётся сервис
- **THEN** статус в портале меняется `creating`→`active`, что кросс-проверяется в
  Temporal UI (`localhost:8080`) и логах worker'а

### Requirement: Локальная аутентификация через Keycloak + Oauth2-Proxy

Локалка ДОЛЖНА (MUST) включать преднастроенный Keycloak realm (клиент портала, базовые роли) и Oauth2-Proxy перед gateway, чтобы OIDC-поток периметра проверялся end-to-end локально.

#### Scenario: Трафик к gateway идёт через Oauth2-Proxy

- **WHEN** запрос портала направляется на периметр
- **THEN** он проходит через Oauth2-Proxy с OIDC-аутентификацией в Keycloak, и только авторизованный трафик доходит до gateway

### Requirement: Моки управляемых систем

Локалка ДОЛЖНА (MUST) предоставлять mock-серверы GitLab, Vault и Harbor, чтобы DevInfra worker и сервисы могли работать против контрактных заглушек без реальных внешних систем.

#### Scenario: Скелеты обращаются к мокам, а не к реальным системам

- **WHEN** компонент в локалке обращается к GitLab/Vault/Harbor
- **THEN** запрос направляется на соответствующий mock-сервер из compose

### Requirement: Миграции и сидинг IDM в локальном стенде

Локальный стенд ДОЛЖЕН (MUST) применять миграции IDM одноразовым мигратором по
образцу `migrate-projects`: `services/idm/migrate.Dockerfile` (goose из
пинованного модуля `./tools`, контекст сборки — корень репозитория) и сервис
`migrate-idm` в docker-compose; сервис `idm` ДОЛЖЕН (MUST) зависеть от
`migrate-idm` через `service_completed_successfully`. Стенд ДОЛЖЕН (MUST)
идемпотентно засевать базовые демо-роли (как минимум право `create` над
`project:demo`, привязанное к демо-субъекту), чтобы сквозной сценарий портала
«Создание сервиса» проходил при включённом RBAC. Сид-данные ДОЛЖНЫ (MUST) быть
явно помечены как локальные/демо и НЕ применяться вне локального профиля.

#### Scenario: Поднятие стенда применяет миграции до старта IDM

- **GIVEN** чистый локальный стенд
- **WHEN** выполняется запуск docker-compose
- **THEN** `migrate-idm` применяет схему IDM и успешно завершается, и только
  после этого стартует сервис `idm`

#### Scenario: Сквозной сценарий портала проходит при включённом RBAC

- **GIVEN** засеянная демо-роль с правом `(create, project:demo)` для
  демо-субъекта
- **WHEN** через портал запускается создание сервиса в проекте `demo`
- **THEN** RBAC разрешает операцию, и сценарий «Создание сервиса» проходит
  сквозь периметр и сервис проектов

#### Scenario: Идемпотентный повторный запуск миграций/сидинга

- **GIVEN** уже применённые миграции и сид
- **WHEN** `migrate-idm` запускается повторно
- **THEN** повторное применение не приводит к ошибке и не дублирует данные
  (`ON CONFLICT DO NOTHING` / версионирование goose)

### Requirement: Seed и миграции для сквозного сценария смены владельцев

Локальное окружение ДОЛЖНО (MUST) обеспечивать прохождение сквозного сценария
«Изменение владельцев» при включённом RBAC: миграции владельцев каталога
применяются мигратором `migrate-projects` (goose, `GOWORK=off`), а seed IDM
ДОЛЖЕН (MUST) расширяться так, чтобы у субъекта `demo-user` было право
`(change_owners, project:demo)` и существовала роль владельца для `project:demo`
(`owner:project:demo`) с правами доступа к ресурсу, выдаваемая/отзываемая
доменным потоком. Демо-данные ДОЛЖНЫ (MUST) включать сервис с начальным
владельцем, чтобы изменение состава было наблюдаемо. Seed ДОЛЖЕН (MUST)
оставаться идемпотентным (повторное применение без дублей).

#### Scenario: Сценарий смены владельцев проходит локально при RBAC

- **GIVEN** поднятое docker-compose окружение с применёнными миграциями и seed
- **WHEN** `demo-user` (как `AUTH_DISABLED_SUBJECT`) меняет владельцев демо-сервиса
- **THEN** право `change_owners` разрешает операцию, workflow завершается,
  владельцы в каталоге и роли в IDM синхронизируются

#### Scenario: Идемпотентность seed

- **WHEN** seed IDM применяется повторно
- **THEN** право `change_owners`, роль владельца и демо-данные не дублируются

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


### Requirement: Артефакты Kubernetes-деплоя и production-образ портала

Репозиторий SHALL содержать артефакты развёртывания в Kubernetes под
`deploy/helm/` (umbrella-чарт `idp`, library-chart `idp-lib`, `values-<env>.yaml`,
`values.example.yaml`). Портал SHALL иметь воспроизводимый production-образ
(`web/Dockerfile`, multi-stage `vite build` → nginx-unprivileged, non-root),
отдельный от dev-образа compose; локальный compose-стенд SHALL продолжать
работать без изменений поведения.

#### Scenario: артефакты деплоя присутствуют и не ломают локалку

- **WHEN** в репозитории есть `deploy/helm/**` и финализированный `web/Dockerfile`
- **THEN** `deploy/compose/docker-compose.yml` поднимает локальный стенд как
  прежде, а production-образ портала собирается отдельно

#### Scenario: dev- и prod-портал сохраняют same-origin /api

- **WHEN** портал обращается к `/api`
- **THEN** в dev (Vite) запрос проксируется на gateway, в Kubernetes — маршрутится
  Istio на gateway, в обоих случаях same-origin (ADR-0009)
