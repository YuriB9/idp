## Context

RBAC реализован в IDM (ADR-0010): субъект — строка `subject` в `subject_roles`,
`AccessService.CheckAccess` strict-match deny-by-default fail-closed, кэш решений в
DragonflyDB (поколение `idm:cache:gen` + точечный `InvalidateSubject`). IAM-админка
фаз 1–2 (ADR-0014/0015) дала читающий `IamAdminService` (в т.ч.
`ListSubjectsWithRoles` — keyset по `subject`, DISTINCT из `subject_roles`),
мутирующий `IamCatalogService`, действия `read`/`write`/`manage` на горизонтальном
`iam:global`, gateway-helper `authorizeResource(w,r,resource,action)` и раздел
портала «Роли и доступы».

Ключевой компромисс ADR-0014, который снимает этот change: «субъекты без ролей
системе неизвестны и не видны; человекочитаемого имени нет; назначение роли —
ручной ввод строки subject». В стенде уже есть Keycloak: realm `idp`
(`oidc_issuer http://keycloak:8080/realms/idp`), client портала `idp-portal`,
realm-файл `deploy/keycloak/idp-realm.json` с пользователем `dev`. Но IDM/портал НЕ
обращаются к каталогу пользователей Keycloak, а `auth.Claims.Subject` берётся из
`sub` (UUID), тогда как сиды RBAC и `AUTH_DISABLED_SUBJECT` указывают на строку
`demo-user` — рассогласование канонического ключа.

Ограничения обязательны: fail-closed (недоступный/пустой IDM → отказ); КАЖДЫЙ
исходящий вызов к Keycloak под SSRF-guard (ValidateURL на записи +
GuardedDialContext на отправке), `SSRF_DISABLED` только локально (приватный адрес
keycloak), как у devinfra-worker; секрет сервис-аккаунта не логировать и не отдавать;
без раскрытия внутренних ошибок клиенту; JWT-правила pkg не ослаблять; листинг PII —
под `CheckAccess`; комментарии в коде только на русском; миграции goose обратимые
(пин `./tools`, `GOWORK=off`, `migrate-idm`); правки realm-json воспроизводимы при
импорте realm.

## Goals / Non-Goals

**Goals:**
- Реальный справочник субъектов из Keycloak (realm `idp`): поиск/листинг и резолв
  `sub` → идентичность (`{subject, username, email, display_name, enabled, found}`).
- Зафиксировать каноническую модель ключа субъекта (`sub` = UUID) и свести
  рассогласование `demo-user`/`dev` (новый ADR-0016).
- Назначение роли выбором пользователя из справочника (subject = канонический ключ);
  совместимость с назначением по произвольной строке.
- Обогащение `ListSubjectsWithRoles` идентичностями и корректная обработка
  «осиротевших» subject.
- Деградация при недоступном Keycloak (справочник не критичен для `CheckAccess`).
- Отдельное право на просмотр PII (`read`, `iam:directory`); отдельный кэш
  идентичностей, не влияющий на decision-cache RBAC.

**Non-Goals:**
- ЗАПИСЬ в Keycloak (создание/блокировка/удаление пользователей, сброс пароля,
  управление группами/клиентами) — отдельный change.
- Маппинг групп Keycloak → роли RBAC, автонаследование ролей из групп/claims.
- LDAP/федерация/SCIM, мульти-realm.
- Аудит-лог изменений (кто/когда) — отдельный change.
- Иерархии ролей, wildcard/скоупы в `resource`, ABAC — матчинг остаётся strict.
- Проекционная таблица `users` в IDM (выбран живой запрос + кэш, решение 3).

## Decisions

### 1. Канонический ключ субъекта: `sub` (UUID Keycloak)

Выбрано: канонический ключ субъекта (значение `auth.Claims.Subject` и колонка
`subject_roles.subject`) — это `sub` из JWT, то есть UUID пользователя Keycloak.
`preferred_username` НЕ используется как ключ.

Обоснование (security/стабильность): `sub` стабилен и не переиспользуется при
переименовании пользователя; именно его уже кладёт `auth.Claims.Subject` (pkg/auth,
fail-closed JWT); это OIDC-канон. `preferred_username` изменяем и не гарантирует
уникальности во времени — небезопасен как ключ авторизации. Резолв `sub` →
человекочитаемое имя делается ДЛЯ ОТОБРАЖЕНИЯ, но НЕ для решения доступа
(`CheckAccess` остаётся по `sub`).

Сведение рассогласования `demo-user`/`dev`: в `deploy/keycloak/idp-realm.json`
пользователю `dev` задаётся ДЕТЕРМИНИРОВАННЫЙ `id` (UUID) — Keycloak при импrealm
realm берёт его как `sub`. Локалка приводится в соответствие:
- `AUTH_DISABLED_SUBJECT` в docker-compose (gateway/projects) = этот UUID (а не
  `demo-user`), чтобы локальный disabled-режим клал тот же `sub`, что выдаст реальный
  вход через Keycloak;
- обратимая goose-миграция переносит сиды RBAC `subject_roles` со строки `demo-user`
  на UUID `dev` (роли `iam-admin`, `project-creator` и пр., засеянные ранее).

Альтернативы (отклонены): protocol-mapper «`sub` = `preferred_username`» — переопределяет
канонический claim, нестандартно, маскирует UUID и ослабляет смысл JWT; оставить
`demo-user` строкой и резолвить её как «локального» субъекта — реальный вход через
Keycloak всё равно даст UUID, рассогласование осталось бы.

### 2. Где живёт клиент Keycloak Admin: IDM

Выбрано: исходящий клиент Keycloak Admin REST живёт в IDM (новый слой
`internal/identity`), хотя сейчас IDM без outbound HTTP.

Обоснование: IDM — владелец субъектов (`subject_roles`) и единственный, кто умеет
сопоставить «есть роль» ↔ «есть в каталоге» (обогащение, «осиротевшие»). Размещение
клиента в IDM держит идентичность в одном слое с RBAC, даёт единый кэш идентичностей
рядом с владельцем данных и позволяет gateway оставаться тонким (только проксирование
+ авторизация). gateway уже умеет звать IDM по gRPC; добавлять ему второй outbound
(в Keycloak) — размывание ответственности. devinfra-worker — образец outbound с
SSRF-guard; IDM повторяет тот же приём (ValidateURL + GuardedDialContext,
`pkg/httpclient`, `SSRF_DISABLED` локально).

Сервис-аккаунт: confidential-client Keycloak с realm-management ролями
`view-users`/`query-users`; токен по `client_credentials`; креды из env локально /
Vault в проде. Секрет не логируется и наружу не отдаётся. Токен кэшируется в памяти
до истечения (с запасом), обновляется лениво.

Альтернативы (отклонены): клиент в gateway (gateway получил бы outbound и знание о
Keycloak — нарушает тонкость периметра, дублировал бы кэш); отдельный новый сервис
(избыточно для MVP, лишний gRPC-хоп и деплой-юнит).

### 3. Источник правды каталога: живой запрос в Keycloak + кэш (TTL)

Выбрано: каталог субъектов — живой запрос в Keycloak Admin REST с кэшированием
результатов в DragonflyDB (TTL), БЕЗ проекционной таблицы `users` в IDM.

Обоснование: Keycloak — источник правды по пользователям; проекция потребовала бы
синхронизации (вебхуки/поллинг), решения о консистентности и ХРАНЕНИЯ PII в нашей БД
надолго. Живой запрос + короткий TTL даёт свежесть, минимизирует хранение PII
(только эфемерный кэш) и проще в эксплуатации. Цена — зависимость доступности
справочника от Keycloak, что приемлемо: справочник НЕ критичен для `CheckAccess`
(решение 5, деградация).

Альтернатива (отклонена): проекционная таблица `users` (sync) — лишняя сложность,
постоянное хранение PII, риск рассинхронизации; не нужна для read-only справочника.

### 4. Авторизация листинга PII: новый ресурс `iam:directory`, действие `read`

Выбрано: листинг/резолв реальных идентичностей (PII: username/email/display name)
авторизуется действием `read` на НОВОМ горизонтальном ресурсе `iam:directory` —
отдельно от `read`/`write`/`manage` на `iam:global`.

Обоснование (наименьшие привилегии): доступ к PII пользователей качественно отличается
от чтения каталога ролей. Отдельный ресурс позволяет выдать «аудитору ролей»
(`read, iam:global`) просмотр RBAC без доступа к персональным данным, а оператору
справочника — `(read, iam:directory)`. Укладывается в существующую модель (action +
resource, strict-match, deny-by-default), новых механизмов не вводит.

Обогащение `GET /iam/subjects` идентичностями выполняется ТОЛЬКО когда вызывающий
держит `(read, iam:directory)`; иначе ответ остаётся «сырым» (только `subject` +
роли, текущее поведение ADR-0014) — PII не утекает носителю лишь `(read, iam:global)`.

Альтернатива (отклонена): переиспользовать `(read, iam:global)` для PII — смешало бы
просмотр RBAC и персональных данных, нарушив наименьшие привилегии.

### 5. Семантика недоступности Keycloak: деградация (не строгий fail-closed RBAC)

Выбрано: справочник деградирует при недоступном/ошибочном Keycloak, НЕ ломая
управление ролями по сырому subject. Но листинг/резолв всё равно ПОД `CheckAccess`
(deny-by-default) ДО запроса в Keycloak.

- Ручки справочника (`/iam/directory/*`) при недоступном Keycloak → `503`
  (`Unavailable`, retryable) с машинной причиной без сырых деталей. UI показывает
  «каталог недоступен».
- Обогащение `GET /iam/subjects`: если резолв в Keycloak не удался — ответ
  отдаётся БЕЗ идентичностей (raw `sub`), управление ролями не ломается.
- «Осиротевший» subject (роль есть, в каталоге Keycloak нет) — НЕ ошибка:
  `SubjectIdentity{found=false}`, UI показывает raw `sub` с пометкой «нет в каталоге».

Обоснование: `CheckAccess` не зависит от имён — недоступность справочника не должна
блокировать назначение/отзыв ролей по `sub`. Это осознанная асимметрия: для РЕШЕНИЯ
доступа сохраняется fail-closed (IDM/decision-cache), а для ОТОБРАЖЕНИЯ имён —
graceful degradation. `503` (а не `403`): вызывающий авторизован, отказывает не
авторизация, а внешняя зависимость; `503` retryable и честно сигналит «попробуйте
позже».

Альтернатива (отклонена): строгий fail-closed справочника (любая недоступность
Keycloak → отказ всей админке) — заблокировал бы управление ролями из-за внешней
системы, нарушив принцип «справочник не критичен для RBAC».

### 6. Форма контракта (proto/idm/v1, аддитивно)

Новый ЧИТАЮЩИЙ сервис `IdentityService` (отдельно от `IamAdminService`/
`IamCatalogService`/`RoleAdminService`/`AccessService`):

```
service IdentityService {
  // Поиск пользователей в каталоге Keycloak по строке (username/email/имя).
  rpc SearchSubjects(SearchSubjectsRequest) returns (SearchSubjectsResponse);
  // Резолв набора канонических ключей (sub) в идентичности (батч).
  rpc ResolveSubjects(ResolveSubjectsRequest) returns (ResolveSubjectsResponse);
}
message SubjectIdentity {
  string subject      = 1; // канонический ключ = sub (UUID Keycloak)
  string username     = 2;
  string email        = 3;
  string display_name = 4;
  bool   enabled      = 5;
  bool   found        = 6; // false → «осиротевший»: в каталоге нет
}
message SearchSubjectsRequest  { string query = 1; string cursor = 2; int32 page_size = 3; }
message SearchSubjectsResponse { repeated SubjectIdentity subjects = 1; string next_cursor = 2; }
message ResolveSubjectsRequest  { repeated string subjects = 1; }
message ResolveSubjectsResponse { repeated SubjectIdentity subjects = 1; }
```

Выбор НОВОГО сервиса (а не расширения `IamAdminService`): идентичность — отдельная
ответственность (внешний источник Keycloak, отдельный кэш, отдельное право
`iam:directory`), и иной режим доступности (деградация vs fail-closed чтения каталога
ролей). Отдельный сервис делает границы (источник, право, blast-radius) явными на
уровне контракта; читающий `IamAdminService` остаётся чистым чтением Postgres.
`ListSubjectsWithRoles` НЕ меняется на уровне proto — обогащение делает gateway
композицией (решение 8), чтобы не тащить outbound-семантику в контракт чтения
Postgres. `ResolveSubjects` для отсутствующих в каталоге возвращает
`SubjectIdentity{subject, found=false}` (а не опускает), чтобы вызывающий различал
«осиротевших».

### 7. Пагинация/поиск: offset Keycloak ↔ opaque-курсор периметра

Keycloak Admin `GET /admin/realms/{realm}/users?search=&first=&max=` использует
offset (`first`/`max`). Периметр проекта использует opaque-курсор (ADR-0009, keyset у
`ListSubjectsWithRoles`). Сведение: справочник тоже отдаёт opaque `next_cursor`, но
ВНУТРИ него кодируется offset Keycloak (`first` следующей страницы), а не keyset —
keyset по каталогу Keycloak невозможен. Форма курсора — непрозрачная для клиента
строка (base64 от `first`); пустой `next_cursor` → страниц больше нет. `page_size`
ограничен разумным максимумом (например, 50) и валидируется (иначе `400`).

Обоснование: внешний контракт (opaque `next_cursor`) единообразен с остальным
периметром; внутреннее offset-устройство скрыто и заменяемо. Поиск по пустой/слишком
короткой строке — `400` (`InvalidArgument`), чтобы не выгружать весь realm.

### 8. Обогащение `GET /iam/subjects` — композиция в gateway

`ListSubjectsWithRoles` (proto) НЕ меняется. gateway после получения списка
субъектов-с-ролями из `IamAdminService`:
1. проверяет `(read, iam:global)` (как сейчас, ADR-0014) для самой выдачи ролей;
2. если вызывающий ДОПОЛНИТЕЛЬНО держит `(read, iam:directory)` — вызывает
   `IdentityService.ResolveSubjects` по списку `sub` и мёрджит идентичности в ответ
   (поля `username`/`email`/`display_name`/`enabled`/`found`); «осиротевшие» →
   `found=false`; ошибка/недоступность Keycloak → ответ без идентичностей
   (деградация);
3. без `(read, iam:directory)` — ответ «сырой» (как ADR-0014), PII не добавляется.

Ответ `GET /iam/subjects` в OpenAPI расширяется аддитивно опциональными полями
идентичности (отсутствуют, если нет права/каталог недоступен). zod-схема — поля
optional. Обоснование: композиция в gateway не тащит outbound в read-контракт IDM и
держит право на PII в одной точке авторизации.

### 9. Кэш идентичностей — ОТДЕЛЬНЫЙ от decision-cache

Выбрано: кэш идентичностей в DragonflyDB в ОТДЕЛЬНОМ namespace `idm:identity:*` с
TTL, полностью независимый от decision-cache RBAC (`idm:cache:gen`,
`InvalidateSubject`).

- Ключи: `idm:identity:resolve:<sub>` (одиночная идентичность по `sub`),
  `idm:identity:search:<хэш(query|first|page_size)>` (страница поиска).
- TTL короткий (например, 300s) — баланс свежести и нагрузки на Keycloak; PII живёт
  в кэше не дольше TTL.
- Инвалидация: только по TTL (нет вебхуков из Keycloak в MVP). Записи RBAC
  (assign/revoke, структурные мутации) кэш идентичностей НЕ трогают и НЕ трогаются
  им: справочник чисто читающий, decision-cache не зависит от имён.
- singleflight на резолв/поиск (как decision-cache) против стампеда к Keycloak.

Зафиксировано (инвариант): операции справочника НЕ вызывают `InvalidateAll`/
`InvalidateSubject` и не читают/не пишут `idm:cache:gen`. Тест (miniredis)
подтверждает, что поиск/резолв не затрагивают ключи decision-cache.

Обоснование: разные жизненные циклы (решения инвалидируются мутациями RBAC;
идентичности — по TTL из внешнего источника). Смешение namespace грозило бы ложной
инвалидацией решений при обновлении имён и наоборот.

### 10. Маппинг ошибок (как ADR-0012/0013) + семантика 503

`identityServer` (gRPC):
- пустой/слишком короткий `query`, пустой `subjects`, превышен `page_size` →
  `InvalidArgument`;
- Keycloak недоступен/таймаут/5xx/ошибка токена → `Unavailable` (fail-closed для
  ручки, деталь в лог по slog `err`, секрет не логируется);
- «осиротевший» subject — НЕ ошибка (`found=false`).

gateway (`httpFromGRPC`, reuse + одно дополнение):
- `PermissionDenied → 403` (нет `iam:directory` или недоступен IDM — fail-closed);
- `InvalidArgument → 400`;
- `Unavailable` от `IdentityService` → `503` (деградация справочника), а не `500`;
- прочее → `500` (без деталей).
Для обогащения `GET /iam/subjects`: `Unavailable` от резолва НЕ превращается в 503
всей ручки — ответ деградирует (идентичности опускаются), сам список ролей отдаётся
`200`.

### 11. SSRF-guard и сеть (образец devinfra-worker)

Все исходящие к Keycloak (токен `client_credentials` + Admin REST) — через
`pkg/ssrf` (ValidateURL базового URL на конфигурации + GuardedDialContext в
транспорте `pkg/httpclient`). `SSRF_DISABLED=true` только локально (адрес `keycloak`
приватный). Таймауты и ограниченные ретраи на сетевые ошибки (без ретрая на 4xx).
Базовый URL Keycloak, realm, client-id/secret сервис-аккаунта, TTL кэша — из env
(локально) / Vault (прод). Секрет в логи/ответы не попадает.

## Поток вызовов

Распределённого workflow/Temporal нет (нет провизии ресурсов) — синхронные
gRPC-вызовы периметр↔IDM и исходящий HTTP IDM→Keycloak под SSRF-guard.

Поиск пользователя (пикер):
```
Портал → gateway: GET /iam/directory/subjects?search=iv&cursor=...
gateway → IDM(Access): CheckAccess(caller, "iam:directory", "read")
  deny/недоступен → 403 (fail-closed)
  allow → gateway → IDM(Identity): SearchSubjects(query, cursor, page_size)
            identity: кэш idm:identity:search:<хэш>?  hit → отдать
              miss → токен сервис-аккаунта (client_credentials, SSRF-guard) →
                     Keycloak Admin GET /users?search=&first=&max= (SSRF-guard) →
                     map → SubjectIdentity[]; запись в кэш (TTL); next_cursor=offset
              Keycloak недоступен → Unavailable
gateway → Портал: 200 {subjects[], next} | 503 (каталог недоступен) | 400 | 403
```

Назначение роли по пикеру:
```
Портал: выбран пользователь → subject = SubjectIdentity.subject (UUID)
Портал → gateway: POST /iam/subjects/{subject}/roles/{role}   (как ADR-0014)
gateway → IDM(Access): CheckAccess(caller,"iam:global","write") → allow
gateway → IDM(RoleAdmin): AssignRole(subject, role)  // точечный InvalidateSubject
gateway → Портал: 200 (идемпотентно)
```

Обогащение списка субъектов-с-ролями:
```
Портал → gateway: GET /iam/subjects
gateway → IDM(Access): CheckAccess(caller,"iam:global","read") → allow
gateway → IDM(IamAdmin): ListSubjectsWithRoles(...) → subjects[]
gateway → IDM(Access): CheckAccess(caller,"iam:directory","read")?
  есть → gateway → IDM(Identity): ResolveSubjects(subjects[].sub)
            found=true → username/email; found=false → «осиротевший»
            Keycloak недоступен → опустить идентичности (деградация)
  нет  → ответ «сырой» (как ADR-0014)
gateway → Портал: 200 {subjects[ {subject, roles, [identity...] } ], next}
```

## Risks / Trade-offs

- **[Утечка PII без права]** → листинг/резолв под `CheckAccess(read,
  iam:directory)` (fail-closed → 403); обогащение `GET /iam/subjects` только при
  наличии права; тесты gateway: deny→403, без права → ответ без PII.
- **[Секрет сервис-аккаунта в логах/ответах]** → секрет только из env/Vault, не
  логируется (логируем по ключу `err` без значения), наружу не отдаётся; тест клиента
  проверяет отсутствие секрета в сообщениях ошибок.
- **[SSRF через адрес Keycloak]** → каждый исходящий вызов под ValidateURL +
  GuardedDialContext; `SSRF_DISABLED` только локалка; образец devinfra-worker.
- **[Недоступность Keycloak ломает админку]** → деградация (решение 5): ручки
  справочника 503, обогащение опускает PII, управление ролями по сырому subject не
  ломается; тесты: Keycloak down → 503 на /directory и деградация /iam/subjects.
- **[Рассогласование `sub`↔`subject`]** → канонический ключ = `sub` (UUID),
  детерминированный UUID `dev` в realm-json, `AUTH_DISABLED_SUBJECT` = UUID,
  обратимая миграция сидов `demo-user`→UUID; тесты: вход `dev` даёт тот же `sub`,
  что засеян.
- **[PII в кэше]** → отдельный namespace `idm:identity:*`, короткий TTL, инвалидация
  по TTL; кэш не затрагивает decision-cache; тест (miniredis) подтверждает
  изоляцию.
- **[Стампед к Keycloak]** → singleflight на резолв/поиск; кэш страниц поиска;
  ограниченные ретраи без ретрая на 4xx.
- **[Дрейф контракта proto↔OpenAPI↔TS]** → `gen:check` (buf + OpenAPI + TS +
  public-копия); Spectral (description/operationId/коды) + Schemathesis-конформанс;
  рантайм zod `.parse` на границе.
- **[Регресс существующих /iam-ручек]** → `authorizeResource` и `httpFromGRPC`
  переиспользуются; `ListSubjectsWithRoles` proto не меняется; старые тесты зелёные.

## Migration Plan

1. Ветка `change/iam-subject-directory` от `master` (прямые коммиты в master
   запрещены).
2. `proto/idm/v1`: `IdentityService` + `SubjectIdentity`; `make proto` (`buf`,
   `GOWORK=off`) → `pkg/api/idm/**`.
3. БД: обратимые миграции — сиды `subject_roles` `demo-user`→UUID `dev`; seed
   `(read, iam:directory)` роли `iam-admin`; `migrate-idm`.
4. IDM: `internal/identity` (Keycloak Admin REST клиент + токен сервис-аккаунта +
   SSRF-guard + `pkg/httpclient`), usecase-фасад, кэш идентичностей (отдельный
   namespace), `identityServer`; новые env; регистрация в `main.go`.
5. gateway: `IdentityServiceClient`; ручки `/iam/directory/*` под
   `authorizeResource(iam:directory, read)`; обогащение `GET /iam/subjects`
   композицией; маппинг (`httpFromGRPC` + 503 на `Unavailable` справочника).
6. OpenAPI: `GET /iam/directory/subjects`, `POST /iam/directory/subjects/resolve`,
   обогащённый `GET /iam/subjects`; схемы `SubjectIdentity` и обёртки; для КАЖДОЙ
   операции summary+description+operationId+ВСЕ коды (200/400/403/503 и т.д.);
   `web npm run gen` (вкл. gen:spec + public-копия); `gen:check` + Spectral зелёные.
7. web: пикер пользователя (поиск с debounce), отображение имён/почты, «осиротевшие»
   subject, «каталог недоступен»; vitest.
8. Локалка: `idp-realm.json` — confidential-клиент сервис-аккаунта
   (`view-users`/`query-users`), детерминированный UUID `dev`, демо-пользователи;
   docker-compose: env Keycloak Admin для IDM, `AUTH_DISABLED_SUBJECT`=UUID,
   `SSRF_DISABLED` локально.
9. README services/idm и корневой; `GOWORK=off go mod tidy` в затронутых модулях при
   новых общих зависимостях; `git checkout -- services/gateway/gateway` после сборки.
10. Опубликовать ADR-0016 (`docs/adr/0016-*.md`, вне openspec/).
11. PR с зелёным CI (go test всех модулей, golangci-lint [errname/paralleltest],
    govulncheck, gen:check, openapi-lint [Spectral], web-test [tsc+vitest],
    integration, conformance); merge → отдельный PR sync+archive (образец #46/#47).

Откат: миграции обратимы (`goose down` — вернуть сиды на `demo-user`, снять
`(read, iam:directory)`); контракт аддитивен; справочник деградирует, не ломая RBAC;
realm-json воспроизводим при импорте.

## Open Questions

Ключевые вопросы закрыты в решениях 1–11 и фиксируются ADR-0016 (канонический ключ
`sub` и сведение `demo-user`/`dev`; клиент Keycloak Admin в IDM с service-account/
SSRF/fail-closed; живой запрос + кэш вместо проекции; авторизация PII ресурсом
`iam:directory`; деградация при недоступном Keycloak с 503 на справочнике; opaque-
курсор поверх offset Keycloak; отдельный кэш идентичностей; маппинг ошибок как
ADR-0012/0013 + 503). Открытых вопросов на момент дизайна нет.
