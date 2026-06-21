## Why

IAM-админка (фазы 1–2, ADR-0014/0015) умеет показывать каталог ролей/прав,
назначать роли и редактировать сам каталог. Но СУБЪЕКТ RBAC остаётся непрозрачной
строкой `sub` из JWT: админка ВИДИТ только тех, у кого УЖЕ есть роль (DISTINCT
`subject` из `subject_roles`, ADR-0014), человекочитаемого имени (username/email/
display name) нет, а назначить роль можно лишь вручную введя строку `subject` —
выбрать пользователя из справочника нельзя. Платформенному администратору негде
увидеть реальных пользователей realm и тех, у кого ролей ещё нет. См.
docs/IDP_MVP_plan.md (Этап 3, RBAC/IAM) и ADR-0003/0009/0010/0011/0014/0015.

Дополнительно есть РАССОГЛАСОВАНИЕ ИДЕНТИЧНОСТИ: локально периметр работает с
`AUTH_DISABLED_SUBJECT=demo-user` и RBAC засеян на строку `demo-user`, но реальный
пользователь Keycloak — `dev`, чей `sub` (то, что кладёт `auth.Claims.Subject`) —
UUID, а не `demo-user`. Канонический ключ субъекта и сиды RBAC не совпадают. Этот
change ОБЯЗАН зафиксировать каноническую модель ключа субъекта и свести локалку/сиды
(новый ADR-0016), иначе реальный вход через Keycloak не соответствует засеянным
правам.

## What Changes

- **Канонический ключ субъекта (ADR-0016):** ключ `subject` в `subject_roles` и
  `auth.Claims.Subject` — это `sub` (UUID Keycloak), а НЕ `preferred_username`.
  Реальному пользователю `dev` в realm-файле задаётся детерминированный UUID; локалка
  приводится в соответствие: `AUTH_DISABLED_SUBJECT` = этот UUID, сиды RBAC мигрируют
  со строки `demo-user` на UUID. JWT-правила pkg не ослабляются (без protocol-mapper
  «sub=username»).
- **Контракт `proto/idm/v1` (аддитивно):** новый читающий сервис `IdentityService`:
  `SearchSubjects(query, cursor)` → `repeated SubjectIdentity` + `next_cursor`;
  `ResolveSubjects(subjects[])` → `repeated SubjectIdentity`. Новое сообщение
  `SubjectIdentity{subject, username, email, display_name, enabled, found}`. Читающий
  `IamAdminService`, `RoleAdminService`, `AccessService`, мутирующий `IamCatalogService`
  НЕ изменяются. `buf generate`; `gen:check` зелёный.
- **IDM — исходящая интеграция с Keycloak Admin REST (ЧТЕНИЕ):** новый слой
  `internal/identity` (клиент Keycloak Admin REST: поиск пользователей по
  username/email/имени с offset-пагинацией и резолв набора `sub` → идентичность),
  получение токена сервис-аккаунта по `client_credentials`. ВСЕ исходящие вызовы —
  через `pkg/ssrf` (ValidateURL + GuardedDialContext) и `pkg/httpclient`, как у
  devinfra-worker; `SSRF_DISABLED` только локально. ОТДЕЛЬНЫЙ кэш идентичностей в
  DragonflyDB (TTL, namespace `idm:identity:*`), НЕ трогающий decision-cache RBAC
  (`idm:cache:gen`/`InvalidateSubject`). gRPC-сервер `identityServer`. fail-closed
  при недоступности; сырые ошибки и секрет сервис-аккаунта наружу/в лог не уходят.
- **Авторизация просмотра PII (новый ресурс):** действие `read` на новом
  горизонтальном ресурсе `iam:directory` для листинга/резолва реальных идентичностей
  (PII) — отдельно от `read`/`write`/`manage` на `iam:global` (наименьшие
  привилегии). gateway зовёт `CheckAccess(read, iam:directory)` перед ручками
  справочника (fail-closed → 403).
- **Обогащение `GET /iam/subjects` (subjects-with-roles):** к субъектам с ролями
  добавляются идентичности (резолв через `IdentityService`) ТОЛЬКО когда вызывающий
  держит `(read, iam:directory)` — иначе ответ остаётся «сырым» (текущее поведение,
  без утечки PII). «Осиротевшие» subject (роль есть, в каталоге нет → `found=false`)
  показываются как raw `sub` с пометкой. Недоступность Keycloak — деградация
  (управление ролями по сырому subject не ломается).
- **Периметр (REST, ADR-0009):** `GET /iam/directory/subjects?search=&cursor=`
  (поиск/листинг, курсор поверх offset Keycloak) и `POST
  /iam/directory/subjects/resolve` (батч-резолв) под `(read, iam:directory)`. При
  недоступном Keycloak — `503` (деградация, retryable), пустой/битый ввод → `400`,
  отказ RBAC → `403`. OpenAPI + TS регенерируются; Spectral и Schemathesis-конформанс
  зелёные.
- **Портал — раздел «Роли и доступы»:** пикер пользователя с поиском (debounce) для
  назначения роли (subject = канонический ключ из справочника), отображение
  username/email рядом с субъектами, обработка «осиротевших» subject, индикация
  «каталог недоступен» без поломки управления ролями по сырому subject. Назначение по
  произвольной строке subject остаётся возможным (совместимость). react-hook-form +
  zod, TanStack + рантайм-валидация zod.
- **Локалка:** в `deploy/keycloak/idp-realm.json` — confidential-клиент сервис-
  аккаунта (realm-management `view-users`/`query-users`), детерминированный UUID для
  `dev`, несколько демо-пользователей. Обратимые goose-миграции IDM: миграция сидов
  RBAC `demo-user` → UUID `dev`, seed `(read, iam:directory)` роли `iam-admin`.
- **Документация:** README services/idm и корневой README — устройство справочника,
  нужный сервис-аккаунт, резолв `sub`→идентичность, требуемое право, поведение UI при
  недоступном Keycloak, проверка поиска/назначения.
- **ADR-0016:** каталог субъектов из OIDC — источник идентичности (живой запрос +
  кэш), канонический ключ субъекта, авторизация PII (`iam:directory`), деградация при
  недоступности Keycloak, отдельный кэш идентичностей, границы scope.

## Capabilities

### New Capabilities
<!-- Новых capability-спеков нет: всё расширяет существующие. -->

### Modified Capabilities
- `service-contracts`: новый читающий сервис `IdentityService`
  (`SearchSubjects`/`ResolveSubjects`) и сообщение `SubjectIdentity` в
  `proto/idm/v1` (аддитивно, wire-совместимо).
- `access-control`: новый ресурс `iam:directory` с действием `read` (просмотр PII,
  отдельно от `iam:global`); канонический ключ субъекта = `sub` (UUID); отдельный кэш
  идентичностей (TTL), не влияющий на инвалидацию решений.
- `iam-administration`: исходящая интеграция IDM с Keycloak Admin REST (чтение:
  поиск/резолв) через SSRF-guard; кэш идентичностей; обогащение
  subjects-with-roles идентичностями и пометка «осиротевших»; fail-closed/деградация.
- `perimeter-rest`: ручки справочника `GET /iam/directory/subjects`,
  `POST /iam/directory/subjects/resolve` под `(read, iam:directory)`; курсор поверх
  offset Keycloak; семантика недоступности (503), валидации (400), отказа (403);
  обогащение `GET /iam/subjects`.
- `portal-ui`: пикер пользователя с поиском (debounce) при назначении роли;
  отображение username/email; «осиротевшие» subject; индикация «каталог недоступен».
- `local-environment`: realm-клиент сервис-аккаунта и демо-пользователи в
  `idp-realm.json`; детерминированный UUID `dev`; миграции сидов `demo-user`→UUID и
  seed `(read, iam:directory)` (обратимые goose).

## Impact

- **Контракты/кодоген:** `proto/idm/v1` (новый `IdentityService`, сообщение
  `SubjectIdentity`), `pkg/api/idm/**` (`buf generate`), `openapi/openapi.yaml` (новые
  `/iam/directory/*` + обогащённый `/iam/subjects`), TS-клиент `web/src/api` +
  `web/public/openapi.yaml` (`gen:check`).
- **services/idm:** новый `internal/identity` (Keycloak Admin REST клиент + токен
  сервис-аккаунта + SSRF-guard), usecase-фасад, кэш идентичностей (отдельный
  namespace в DragonflyDB), `identityServer` (gRPC), регистрация в `main.go`; новые
  env (адрес Keycloak, realm, client id/secret сервис-аккаунта, TTL кэша,
  `SSRF_DISABLED`).
- **services/gateway:** gRPC-клиент `IdentityServiceClient`; обработчики
  `/iam/directory/*` под `authorizeResource(iam:directory, read)`; композиция
  обогащения `GET /iam/subjects` (резолв при наличии права); маппинг кодов (reuse
  `httpFromGRPC` + семантика 503 при `Unavailable` справочника).
- **web:** пикер пользователя (поиск с debounce), отображение имён/почты, обработка
  «осиротевших» subject и «каталог недоступен»; vitest.
- **БД:** обратимые миграции `services/idm/migrations` — миграция сидов
  `demo-user`→UUID `dev`, seed `(read, iam:directory)` для `iam-admin`. Новых таблиц
  нет (источник правды каталога — Keycloak, не проекция).
- **Keycloak/локалка:** `deploy/keycloak/idp-realm.json` — confidential-клиент
  сервис-аккаунта, детерминированный UUID `dev`, демо-пользователи (воспроизводимо
  при импорте realm).
- **Откат/компенсации:** не затрагивает провизию ресурсов (нет Saga/workflow);
  миграции обратимы; контракт аддитивен; справочник не критичен для `CheckAccess`
  (деградирует, не ломая RBAC).
- **Зависимости:** при новых общих зависимостях — `GOWORK=off go mod tidy` во всех
  затронутых модулях. NB: `services/gateway/gateway` — закоммиченный бинарь, после
  сборки не коммитить (`git checkout -- services/gateway/gateway`).
