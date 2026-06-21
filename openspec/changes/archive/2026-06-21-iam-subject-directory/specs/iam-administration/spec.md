# iam-administration Specification (delta)

## ADDED Requirements

### Requirement: Исходящая интеграция IDM с Keycloak Admin REST (чтение) под SSRF-guard

Сервис IDM ДОЛЖЕН (MUST) реализовать слой `internal/identity` — клиент Keycloak
Admin REST для ЧТЕНИЯ каталога пользователей realm `idp`: поиск по строке
(username/email/имя) с offset-пагинацией и резолв набора `sub` → идентичность
(`{subject, username, email, display_name, enabled, found}`). Доступ ДОЛЖЕН (MUST)
выполняться от сервис-аккаунта (confidential-client с realm-management ролями
`view-users`/`query-users`), токен — по `client_credentials`. КАЖДЫЙ исходящий вызов
(получение токена и Admin REST) ДОЛЖЕН (MUST) проходить через `pkg/ssrf` (ValidateURL
на конфигурации + GuardedDialContext в транспорте) и `pkg/httpclient`; `SSRF_DISABLED`
ДОЛЖЕН (MUST) включаться ТОЛЬКО локально. Секрет сервис-аккаунта НЕ ДОЛЖЕН
(MUST NOT) логироваться или раскрываться наружу; сырые ошибки Keycloak НЕ ДОЛЖНЫ
(MUST NOT) попадать клиенту (деталь — в лог по ключу slog `err`). При недоступности/
таймауте/5xx Keycloak или ошибке выдачи токена операция ДОЛЖНА (MUST) завершаться
`codes.Unavailable` (fail-closed для ручки).

#### Scenario: Поиск пользователей по строке

- **GIVEN** сервис-аккаунт настроен, Keycloak доступен
- **WHEN** вызывается `SearchSubjects(query="iv", page_size=20)`
- **THEN** возвращается страница `SubjectIdentity[]` совпадений и `next_cursor` для
  следующей страницы (offset Keycloak инкапсулирован в курсоре)

#### Scenario: Резолв набора sub

- **WHEN** вызывается `ResolveSubjects([sub1, sub2])` для существующих пользователей
- **THEN** возвращаются их идентичности с `found=true`, `username`/`email`/
  `display_name`/`enabled` заполнены

#### Scenario: Keycloak недоступен → Unavailable (без сырых деталей)

- **GIVEN** Keycloak недоступен (таймаут/5xx) ИЛИ выдача токена не удалась
- **WHEN** вызывается `SearchSubjects`/`ResolveSubjects`
- **THEN** возвращается `codes.Unavailable`, деталь — только в лог по ключу `err`,
  секрет сервис-аккаунта в сообщении/логе отсутствует

#### Scenario: Невалидный ввод поиска → InvalidArgument

- **WHEN** вызывается `SearchSubjects` с пустым/слишком коротким `query` или с
  `page_size` сверх допустимого максимума
- **THEN** возвращается `codes.InvalidArgument`, обращения к Keycloak не делается

### Requirement: Кэш идентичностей с TTL в отдельном namespace

IDM ДОЛЖЕН (MUST) кэшировать результаты резолва и поиска идентичностей в DragonflyDB
в отдельном namespace `idm:identity:*` (ключ резолва — от `sub`, ключ поиска — от
нормализованных `query`/offset/`page_size`) с коротким TTL. Кэш ДОЛЖЕН (MUST) быть
независим от decision-cache RBAC (см. capability `access-control`): операции
справочника НЕ трогают `idm:cache:gen`/`InvalidateSubject`. Параллельные одинаковые
запросы к Keycloak ДОЛЖНЫ (MUST) объединяться (singleflight) против стампеда.

#### Scenario: Повторный резолв обслуживается из кэша

- **GIVEN** `ResolveSubjects([sub])` уже выполнен, TTL не истёк
- **WHEN** повторно вызывается `ResolveSubjects([sub])`
- **THEN** ответ берётся из `idm:identity:resolve:<sub>` без обращения к Keycloak

#### Scenario: Кэш идентичностей не затрагивает decision-cache

- **WHEN** выполняются поиск/резолв и запись в `idm:identity:*`
- **THEN** поколение `idm:cache:gen` и точечная инвалидация по субъекту не
  затрагиваются (проверяется тестом на miniredis)

### Requirement: Обогащение списка субъектов-с-ролями идентичностями и пометка осиротевших

Существующая выдача субъектов-с-ролями ДОЛЖНА (MUST) обогащаться идентичностями
(резолв через `IdentityService`, ADR-0014 `ListSubjectsWithRoles`) ТОЛЬКО когда
вызывающий держит `(read, iam:directory)`; иначе ответ остаётся «сырым» (только
`subject` + роли). Субъекты, у которых роль есть, но в каталоге Keycloak их нет
(«осиротевшие»), ДОЛЖНЫ (MUST) помечаться `found=false` и отдаваться как raw `sub`
(не опускаться). При недоступности Keycloak обогащение ДОЛЖНО (MUST) ДЕГРАДИРОВАТЬ:
список субъектов-с-ролями отдаётся без идентичностей, управление ролями по сырому
`sub` НЕ ломается. `ListSubjectsWithRoles` на уровне proto НЕ изменяется —
обогащение выполняется композицией на периметре (см. capability `perimeter-rest`).

#### Scenario: Обогащение при наличии права directory

- **GIVEN** вызывающий держит `(read, iam:global)` и `(read, iam:directory)`,
  Keycloak доступен
- **WHEN** запрашивается список субъектов-с-ролями
- **THEN** каждому субъекту с найденной идентичностью добавлены `username`/`email`/
  `display_name`/`enabled`, `found=true`

#### Scenario: Осиротевший субъект помечен found=false

- **GIVEN** субъект `sub` есть в `subject_roles`, но в каталоге Keycloak его нет
- **WHEN** запрашивается обогащённый список
- **THEN** субъект присутствует как raw `sub` с `found=false` (пометка «нет в
  каталоге»), его роли отдаются как обычно

#### Scenario: Деградация при недоступном Keycloak

- **GIVEN** вызывающий держит `(read, iam:directory)`, но Keycloak недоступен
- **WHEN** запрашивается список субъектов-с-ролями
- **THEN** список ролей отдаётся без идентичностей (деградация), управление ролями по
  сырому `sub` остаётся доступным
