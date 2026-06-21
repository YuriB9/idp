# ADR-0016: Каталог субъектов из OIDC — канонический ключ, авторизация PII, кэш и деградация

**Status:** Accepted
**Date:** 2026-06-21
**Change:** iam-subject-directory

## Context

IAM-админка фаз 1–2 (ADR-0014/0015) даёт просмотр каталога ролей/прав, назначение
ролей и правку каталога, но субъект RBAC — непрозрачная строка `sub` из JWT: видны
лишь субъекты, у кого УЖЕ есть роль (DISTINCT из `subject_roles`), человекочитаемых
имён нет, назначение роли — ручной ввод строки. Keycloak уже в стенде (realm `idp`,
пользователь `dev`), но IDM/портал не обращаются к каталогу пользователей. Есть
рассогласование: `auth.Claims.Subject` берётся из `sub` (UUID), а сиды RBAC и
`AUTH_DISABLED_SUBJECT` указывают на `demo-user`. Опирается на ADR-0003 (fail-closed,
SSRF-guard), ADR-0009 (периметр/OpenAPI/TS), ADR-0010 (RBAC и кэш решений), ADR-0011
(контракт ролей), ADR-0012/0013 (маппинг ошибок), ADR-0014 (модель админки на
`iam:global`, `ListSubjectsWithRoles`, `authorizeResource`), ADR-0015 (`manage`,
динамический каталог).

## Decision

- **Канонический ключ субъекта = `sub` (UUID Keycloak).** Колонка
  `subject_roles.subject` и `auth.Claims.Subject` хранят `sub`, не
  `preferred_username` (стабилен, не переиспользуется, OIDC-канон, уже кладётся
  pkg/auth). Резолв `sub`→имя — только для отображения, не для решения доступа
  (`CheckAccess` остаётся по `sub`). Рассогласование `demo-user`/`dev` сводится:
  детерминированный `id` (UUID) пользователю `dev` в realm-json,
  `AUTH_DISABLED_SUBJECT`=этот UUID, обратимая goose-миграция переносит сиды
  `subject_roles` с `demo-user` на UUID. Без protocol-mapper «sub=username»
  (не ослабляем JWT).
- **Клиент Keycloak Admin REST живёт в IDM.** IDM — владелец субъектов; новый слой
  `internal/identity` (поиск/резолв, токен сервис-аккаунта по `client_credentials`).
  Confidential-client с realm-management `view-users`/`query-users`; креды из
  env/Vault, секрет не логируется и наружу не отдаётся. gateway остаётся тонким.
- **Источник правды — живой запрос + кэш (TTL), без проекции.** Keycloak —
  источник правды; короткий TTL даёт свежесть и минимизирует хранение PII; проекция
  `users` (sync) отвергнута как избыточная.
- **Авторизация PII — новый ресурс `iam:directory`, действие `read`.** Листинг/резолв
  идентичностей (PII) отделены от `read`/`write`/`manage` на `iam:global` (наименьшие
  привилегии). Обогащение `ListSubjectsWithRoles` идентичностями — только при наличии
  `(read, iam:directory)`; иначе ответ «сырой» (PII не утекает).
- **Деградация при недоступном Keycloak.** Справочник не критичен для `CheckAccess`:
  ручки `/iam/directory/*` → `503` (retryable), обогащение списка опускает PII,
  управление ролями по сырому `sub` не ломается. Листинг всё равно под `CheckAccess`
  (deny-by-default) ДО запроса в Keycloak. «Осиротевший» subject (роль есть, в
  каталоге нет) — `SubjectIdentity{found=false}`, не ошибка.
- **Контракт — новый читающий сервис `IdentityService`** (`SearchSubjects`,
  `ResolveSubjects`) и сообщение `SubjectIdentity{subject,username,email,
  display_name,enabled,found}` в `proto/idm/v1` (аддитивно, wire-совместимо).
  `ListSubjectsWithRoles` (proto) не меняется — обогащение делает gateway композицией.
- **Пагинация.** Keycloak Admin использует offset (`first`/`max`); периметр отдаёт
  opaque `next_cursor`, внутри которого закодирован offset; внешний контракт
  единообразен с остальным периметром. Пустой/слишком короткий поиск → `400`.
- **Кэш идентичностей — отдельный namespace `idm:identity:*` (TTL).** Не трогает
  decision-cache RBAC (`idm:cache:gen`/`InvalidateSubject`) и не трогается им;
  инвалидация по TTL; singleflight против стампеда. Операции справочника не влияют
  на инвалидацию решений.
- **Маппинг ошибок (reuse ADR-0012/0013) + 503.** `PermissionDenied→403`,
  `InvalidArgument→400`, `Unavailable` справочника→`503` (деградация), прочее→500
  (без деталей). Все исходящие к Keycloak под SSRF-guard (ValidateURL +
  GuardedDialContext), `SSRF_DISABLED` только локалка.

## Consequences

**Положительные:** реальный справочник пользователей (поиск/имена/почта); назначение
роли выбором из справочника при сохранении назначения по строке; «осиротевшие»
subject видны и обрабатываются; PII защищён отдельным правом (наименьшие привилегии);
справочник деградирует, не ломая RBAC; канонический ключ зафиксирован, локалка
согласована; PII не хранится постоянно (эфемерный кэш с TTL); decision-cache
изолирован; контракт аддитивен.

**Отрицательные:** IDM получает первый outbound HTTP (к Keycloak) — новая зона
отказа и SSRF-поверхность (закрыта guard'ом); зависимость свежести имён от TTL;
горизонтальный `iam:directory` — грубая точка полномочий на весь каталог; opaque-
курсор поверх offset не даёт стабильности keyset при изменении каталога между
страницами (приемлемо для справочника); добавление сервиса требует синхронной
регенерации Go/TS.

**Альтернативы (отклонены):** `preferred_username` как канонический ключ (изменяем,
небезопасен); protocol-mapper «sub=username» (переопределяет claim, ослабляет JWT);
клиент Keycloak в gateway (размывает тонкость периметра, дублирует кэш) или в новом
сервисе (избыточно для MVP); проекционная таблица `users` (sync-сложность, хранение
PII); переиспользовать `(read, iam:global)` для PII (смешивает RBAC и персональные
данные); строгий fail-closed справочника (внешняя система блокировала бы управление
ролями); общий namespace кэша (ложная инвалидация решений); запись в Keycloak,
группы→роли, LDAP/SCIM/мульти-realm, аудит-лог (вне MVP-scope).

**Связь:** реализует Этап 3 docs/IDP_MVP_plan.md (RBAC/IAM); опирается на
ADR-0003/0009/0010/0011/0012/0013/0014/0015; goose из `./tools` (ADR-0007).
