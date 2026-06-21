# ADR (изменение: iam-subject-directory)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0016 — Каталог субъектов из OIDC: канонический ключ, авторизация PII, кэш и
  деградация**
  (`docs/adr/0016-iam-subject-directory-from-oidc.md`): субъект RBAC получает реальный
  справочник из Keycloak (realm `idp`); канонический ключ субъекта — `sub` (UUID),
  совпадающий с `auth.Claims.Subject`, а не `preferred_username`; рассогласование
  `demo-user`/`dev` сводится детерминированным UUID `dev` в realm-json, выставлением
  `AUTH_DISABLED_SUBJECT`=UUID и обратимой миграцией сидов; исходящий клиент Keycloak
  Admin REST (ЧТЕНИЕ: поиск/резолв) живёт в IDM (владелец субъектов) под SSRF-guard и
  service-account (`view-users`/`query-users`, `client_credentials`); источник правды
  — живой запрос + кэш с TTL (без проекционной таблицы); просмотр PII авторизуется
  новым ресурсом `iam:directory` действием `read` (наименьшие привилегии, отдельно от
  `iam:global`); при недоступном Keycloak справочник ДЕГРАДИРУЕТ (ручки `/iam/directory/*`
  → 503, обогащение списка субъектов опускает PII), не ломая управление ролями по
  сырому `sub`, но листинг всё равно под `CheckAccess`; кэш идентичностей —
  ОТДЕЛЬНЫЙ namespace `idm:identity:*` (TTL), не трогающий decision-cache RBAC; новый
  читающий сервис `IdentityService` (`SearchSubjects`/`ResolveSubjects`) в
  `proto/idm/v1`; opaque-курсор периметра поверх offset Keycloak; scope MVP исключает
  ЗАПИСЬ в Keycloak, маппинг групп→роли, LDAP/SCIM/мульти-realm, аудит-лог.

> При apply ADR-0016 публикуется как
> `docs/adr/0016-iam-subject-directory-from-oidc.md` (вне openspec/), Status:
> Accepted, Date: 2026-06-21, Change: iam-subject-directory.

Канонический ADR-0016 (для публикации при apply):

```markdown
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
```

## Реализуемые существующие ADR

- **ADR-0003 — Аутентификация/авторизация (fail-closed) и SSRF-guard:** листинг/резолв
  PII под `CheckAccess(read, iam:directory)`; недоступность IDM → 403; КАЖДЫЙ
  исходящий к Keycloak под ValidateURL + GuardedDialContext; `SSRF_DISABLED` только
  локалка; секрет сервис-аккаунта и внутренние ошибки наружу не раскрываются;
  JWT-правила pkg не ослабляются (канонический ключ = `sub`).
- **ADR-0009 — Форма ресурсов периметра (REST/OpenAPI/TS):** новые `/iam/directory/*`
  ручки и обогащённый `/iam/subjects`; OpenAPI — источник правды; TS/zod +
  `public/openapi.yaml` регенерируются (`gen:check`); Spectral + Schemathesis-
  конформанс зелёные; opaque-курсор поверх offset Keycloak.
- **ADR-0010 — Модель RBAC и кэш решений:** новый ресурс `iam:directory` в той же
  модели (strict-match, deny-by-default); кэш идентичностей ОТДЕЛЬНЫЙ, decision-cache
  не затрагивается.
- **ADR-0011 — Контракт ролей и синхронизация:** assign/revoke по subject
  переиспользуются (subject = канонический `sub` из справочника); назначение по
  строке сохраняется.
- **ADR-0012/0013 — Маппинг ошибок:** `PermissionDenied→403`, `InvalidArgument→400`,
  `Unavailable`→503 (деградация справочника) через `httpFromGRPC`.
- **ADR-0014 — IAM-админка (фаза 1):** переиспользуются `authorizeResource`,
  `ListSubjectsWithRoles` (обогащается композицией в gateway), раздел портала «Роли
  и доступы».
- **ADR-0015 — Динамический каталог IAM:** модель `read`/`write`/`manage` на
  `iam:global` не изменяется; `iam:directory` добавляется рядом как отдельный ресурс.
