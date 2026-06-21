## Context

RBAC реализован в IDM (ADR-0010): таблицы `roles(id,name)`,
`permissions(id,action,resource)`, `role_permissions`, `subject_roles(subject,
role_id)`; `AccessService.CheckAccess` (strict-match, deny-by-default,
fail-closed) с кэшем решений в DragonflyDB (поколение `idm:cache:gen` +
`InvalidateSubject`); `RoleAdminService.AssignRole/RevokeRole` (идемпотентны,
с `InvalidateSubject`). Роли/права сидируются миграциями; динамического создания
ролей нет (ADR-0011). Портал (ADR-0009) умеет только project-scoped операции над
сервисами; gateway-helper `authorize(w,r,project,action)` жёстко формирует
`resource="project:"+project`.

Этот change даёт в портале просмотр ролей/прав и управление назначением ролей
субъектам (assign/revoke) и защищает саму админку отдельным RBAC-действием. Это
первый горизонтальный (не project-scoped) сценарий периметра, поэтому требуется
обобщить авторизацию без регресса существующих project-вызовов. Ограничения
обязательны: fail-closed (недоступный/пустой IDM → отказ, не passthrough), без
раскрытия внутренних ошибок клиенту, мутации ролей → `InvalidateSubject`, чтение
без побочных эффектов на кэш, комментарии в коде только на русском, миграции goose
обратимые (пин `./tools`, `GOWORK=off`).

## Goals / Non-Goals

**Goals:**
- Читающий контракт каталога IAM (роли, права, права роли, субъекты с ролями,
  роли субъекта) в `proto/idm/v1` (аддитивно) и на периметре (`/iam/*`).
- Привилегированная защита админки отдельным RBAC-действием (fail-closed,
  `CheckAccess` перед КАЖДОЙ ручкой).
- Управление назначением ролей субъектам (assign/revoke) с идемпотентностью и
  инвалидацией кэша по затронутому субъекту.
- Раздел портала «Роли и доступы» с обработкой 403/404/400 и рантайм-валидацией.
- Обобщение gateway `authorize` до произвольного ресурса без регресса.
- Локальный seed права админки субъекту `demo-user`.

**Non-Goals:**
- Динамическое создание/удаление ролей и прав, редактирование `role_permissions`
  из UI (роли/права сидируются миграциями; UI только ПОКАЗЫВАЕТ их и назначает
  существующие роли субъектам).
- Управление пользователями / реальный OIDC realm / Keycloak admin (субъекты —
  строки `subject` из JWT; «пользователей» как сущности нет).
- Аудит-лог изменений ролей, время/автор назначения (колонок нет — не вводим).
- Богатая модель иерархий/скоупов, wildcard-матчинг ресурсов.

## Decisions

### 1. Модель полномочий админки: раздельные `read`/`write` на `iam:global`

Выбрано: ДВА действия на горизонтальном ресурсе `iam:global` —
`(read, iam:global)` для всех читающих ручек и `(write, iam:global)` для
assign/revoke. Это укладывается в существующую модель (action + resource,
strict-match, deny-by-default): ресурс несёт scope (раньше `project:<p>`, теперь
горизонтальный `iam:global`), действие — операцию.

Обоснование: листинг ВСЕХ субъектов/ролей сам по себе чувствителен, поэтому
выдаётся только с правом (не неявно из project-прав). Раздельность `read`/`write`
даёт наименьшие привилегии: роль аудитора может получить только `read`, не получая
возможности мутировать; `write` строго опаснее и выдаётся уже. Ни одно из действий
не наследуется неявно из project-действий.

Альтернативы (отклонены): одно действие `manage_iam` (грубее — нельзя выдать
только-чтение аудитору; смешивает листинг и мутацию в одно право); привязка к
конкретному ресурсу вместо горизонтального (нет естественного project-scope у
глобальной админки — `iam:global` честно отражает горизонтальность); per-endpoint
действия (`iam:list_roles`, `iam:list_subjects`, …) — избыточная гранулярность для
MVP без пользы.

### 2. Точки CheckAccess (fail-closed)

`CheckAccess` вызывается в gateway ПЕРЕД проксированием КАЖДОЙ IAM-ручки:
GET-ручки → `CheckAccess(subject, "iam:global", "read")`; `POST`/`DELETE`
assign/revoke → `CheckAccess(subject, "iam:global", "write")`. Отказ ИЛИ
недоступность/ошибка IDM → `403` (fail-closed), запрос в IDM-read/RoleAdmin не
проксируется; деталь — только в лог по ключу slog `err`. `subject` берётся из
claims (`auth.ClaimsFromContext`); пустой subject → IDM ответит отказом.

### 3. Обобщение gateway authorize до произвольного resource

Вводится `authorizeResource(w, r, resource, action)` — обобщённый helper,
формирующий запрос `CheckAccess` с произвольной строкой ресурса. Прежний
`authorize(w, r, project, action)` становится тонкой обёрткой:
`a.authorizeResource(w, r, "project:"+project, action)`. Все существующие
project-вызовы (create/list/read/owners/decommission/transfer) продолжают звать
`authorize` без изменений — нулевой регресс. IAM-ручки зовут
`authorizeResource(w, r, "iam:global", "read"|"write")`.

### 4. Форма читающего контракта (proto/idm/v1, аддитивно)

Новый сервис `IamAdminService` (отдельно от `RoleAdminService`, который остаётся
управляющим контрактом мутаций и переиспользуется):

```
service IamAdminService {
  rpc ListRoles(ListRolesRequest) returns (ListRolesResponse);
  rpc ListPermissions(ListPermissionsRequest) returns (ListPermissionsResponse);
  rpc GetRolePermissions(GetRolePermissionsRequest) returns (GetRolePermissionsResponse);
  rpc ListSubjectsWithRoles(ListSubjectsWithRolesRequest) returns (ListSubjectsWithRolesResponse);
  rpc GetSubjectRoles(GetSubjectRolesRequest) returns (GetSubjectRolesResponse);
}
message Role        { string name = 1; }
message Permission  { string action = 1; string resource = 2; }
message SubjectRoles{ string subject = 1; repeated string roles = 2; }
// ListRoles/ListPermissions — без пагинации (см. решение 5)
// GetRolePermissions(role) — role не найдена → NotFound
// ListSubjectsWithRoles(page_size, page_token) → repeated SubjectRoles + next_page_token
// GetSubjectRoles(subject) → repeated string roles (пусто, не NotFound)
```

Изменения чисто аддитивны: новый сервис + новые сообщения, существующие RPC и
сообщения не трогаются → wire-совместимо. (В отличие от ADR-0010, где добавление
`RoleAdminService` помечалось BREAKING-в-комментарии, здесь то же по сути
аддитивное расширение; помечаем как additive, BREAKING не требуется.) `id` роли
наружу не отдаётся — стабильный идентификатор роли = её `name` (им же оперируют
assign/revoke).

Обоснование выбора `name`, а не `id`: assign/revoke принимают имя роли; UI
оперирует именами; `id` (UUID) — внутренняя деталь хранения, наружу не нужна.

### 5. Пагинация

`ListRoles`, `ListPermissions`, `GetRolePermissions`, `GetSubjectRoles` —
БЕЗ пагинации: это небольшие ограниченные справочники (роли/права сидируются
миграциями; роли субъекта — единицы). Возвращаем полный набор. `next_page_token`
не вводим, чтобы не плодить лишний контракт.

`ListSubjectsWithRoles` — keyset-пагинация по `subject` (ASC), как у services
(ADR-0009): `page_size` (сервер клампит к пределу) + непрозрачный `page_token`.
Обоснование: число субъектов потенциально растёт (каждый, кому когда-либо
назначали роль), поэтому единственный потенциально неограниченный список делаем
страничным и консистентным с существующим keyset-листингом сервисов; offset
отклонён (нестабилен при конкурентных вставках, как и для services).

### 6. Перечисление субъектов: DISTINCT subject из subject_roles

Список субъектов = `SELECT DISTINCT subject FROM subject_roles ORDER BY subject`.
Реестра пользователей нет (субъект — строка `sub` из JWT), поэтому субъект
«существует» для админки ровно тогда, когда у него есть хотя бы одна роль.
Следствие: субъекты без ролей в системе НЕ ВИДНЫ — это принято и задокументировано
(назначить роль можно любому subject через assign по строке, даже если он сейчас
не в списке). Роли субъекта в `ListSubjectsWithRoles` собираются join'ом
`subject_roles → roles` и агрегируются (`array_agg`/группировка по subject) в
рамках одной keyset-страницы.

### 7. Идемпотентность и коды assign/revoke на периметре

Переиспользуем `RoleAdminService.AssignRole/RevokeRole` (идемпотентны).
Периметр:
- `POST /iam/subjects/{subject}/roles/{role}` (assign): повторный assign → `200`;
  несуществующая роль → `NotFound` → `404`; пустые поля → `InvalidArgument` →
  `400`.
- `DELETE /iam/subjects/{subject}/roles/{role}` (revoke): revoke отсутствующей
  связки → `200`; пустые поля → `400`.

Выбрано `200` с телом = актуальный набор ролей субъекта (`{subject, roles[]}`),
а НЕ `204`. Обоснование: клиенту нужен свежий список ролей для рендера и для
рантайм-валидации ответа zod `.parse`, а TanStack-мутации удобнее обновлять кэш
из тела ответа; это согласуется с `setOwners`, который тоже возвращает результат.
`409` для assign/revoke не используется (операции идемпотентны, конкурентного
конфликта состояния нет — `ON CONFLICT DO NOTHING` / `DELETE` без guarded-CAS).

### 8. Инвалидация кэша после мутаций

Мутации идут через существующий `usecase.RoleManager`, который уже после
`AssignRole`/`RevokeRole` вызывает `InvalidateSubject(subject)` (не оставлять
устаревший `allow`/`deny`). Дополнительной инвалидации поколения не требуется
(меняется привязка одного субъекта). Читающие методы НЕ трогают кэш решений
(только SELECT из Postgres) — без побочных эффектов.

### 9. Маппинг ошибок (reuse ADR-0012/0013)

gateway `httpFromGRPC` уже разводит коды: `PermissionDenied→403`, `NotFound→404`,
`InvalidArgument→400`, прочее→500 (без деталей). Для IAM этого достаточно: новых
правил не требуется. `Aborted`/`AlreadyExists→409` и `FailedPrecondition→422` в
IAM-ручках не возникают (read-only + идемпотентные мутации). Внутренние ошибки
наружу не раскрываются — деталь в лог по ключу slog `err`.

### 10. Без новых таблиц; новых guarded-CAS нет

Change read-only по модели IDM: существующих таблиц достаточно. Guarded-CAS не
вводится (нет переходов статусов — данные каталога read-only здесь; мутации
привязок идемпотентны через `ON CONFLICT DO NOTHING`/`DELETE`). Единственная
миграция — обратимый seed роли админки для локалки (решение 11).

### 11. Локальный seed права админки

Обратимая goose-миграция IDM `0006_seed_iam_admin_demo.sql`: роль `iam-admin`,
права `(read, iam:global)` и `(write, iam:global)`, привязка к ней `demo-user`
(совпадает с `AUTH_DISABLED_SUBJECT`). Идемпотентно (`ON CONFLICT DO NOTHING`),
`Down` снимает привязку/права/роль. Только стенд docker-compose, не прод.

## Поток вызовов (read и мутация)

Распределённого workflow/Temporal в этом change НЕТ (нет провизии внешних
ресурсов), поэтому sequence — это синхронные gRPC-вызовы периметр↔IDM.

Чтение (пример — список субъектов с ролями):
```
Портал → gateway: GET /iam/subjects?page_size=...&page_token=...
gateway → IDM(Access): CheckAccess(subject, "iam:global", "read")
  deny/недоступен → 403 (fail-closed), наружу не проксируем
  allow → gateway → IDM(IamAdmin): ListSubjectsWithRoles(page_size, page_token)
            IDM(repo): SELECT subject, array_agg(role.name) ... GROUP BY subject (keyset)
gateway → Портал: 200 { subjects:[{subject, roles[]}], next_page_token }  (zod .parse)
```

Мутация (пример — назначить роль):
```
Портал → gateway: POST /iam/subjects/{subject}/roles/{role}
gateway → IDM(Access): CheckAccess(caller, "iam:global", "write")
  deny/недоступен → 403 (fail-closed)
  allow → gateway → IDM(RoleAdmin): AssignRole(subject, role)
            usecase.RoleManager: repo.AssignRole (ON CONFLICT DO NOTHING)
                                  → cache.InvalidateSubject(subject)
            роль не найдена → NotFound → 404
gateway → IDM(IamAdmin): GetSubjectRoles(subject)   // свежий набор для тела ответа
gateway → Портал: 200 { subject, roles[] }  (zod .parse)
```

## Risks / Trade-offs

- **[Привилегированный листинг утечёт без права]** → КАЖДАЯ ручка под
  `CheckAccess` (fail-closed); недоступность IDM → 403, не passthrough; тесты
  gateway покрывают deny→403 и IDM-недоступен→403.
- **[Субъекты без ролей невидимы в списке]** → принято и задокументировано;
  назначить роль можно любому subject по строке (assign не требует присутствия в
  списке). Согласуется с отсутствием реестра пользователей.
- **[Дрейф контракта proto↔OpenAPI↔TS]** → `gen:check` (proto buf + OpenAPI + TS)
  в CI; рантайм-валидация ответов zod `.parse` на клиенте ловит расхождение на
  границе.
- **[Устаревший allow в кэше после мутации]** → мутации только через
  `RoleManager` с обязательной `InvalidateSubject`; ошибка инвалидации
  возвращается вызывающему (идемпотентный ретрай).
- **[Регресс project-авторизации при обобщении authorize]** → `authorize`
  сохраняется как обёртка над `authorizeResource("project:"+project, action)`;
  существующие тесты gateway остаются зелёными.
- **[N+1 при сборке ролей субъектов]** → агрегирование в одном SQL
  (`array_agg`/join + GROUP BY) на keyset-страницу, без отдельного запроса на
  субъект.

## Migration Plan

1. Ветка `change/iam-roles-portal` от `master` (прямые коммиты в master
   запрещены).
2. `proto/idm/v1`: добавить `IamAdminService` + сообщения; `buf generate`
   (`*.pb.go`).
3. IDM: repository (read-методы), usecase (read-фасад; reuse `RoleManager`),
   `main.go` (`iamAdminServer`; reuse `roleAdminServer`).
4. gateway: `authorizeResource`, новый `iamAPI`, gRPC-клиенты IamAdmin/RoleAdmin,
   маршруты `/iam/*`.
5. OpenAPI: новые `/iam/*` пути; `web npm run gen`; `gen:check` зелёный.
6. web: раздел «Роли и доступы», формы/таблицы, vitest.
7. goose-миграция `0006_seed_iam_admin_demo.sql` (обратимая); `migrate-idm`.
8. README/инструкция; `GOWORK=off go mod tidy` в затронутых модулях при новых
   общих зависимостях; `git checkout -- services/gateway/gateway` после сборки.
9. PR с зелёным CI (тесты, golangci-lint [errname/paralleltest], govulncheck,
   gen:check, integration); merge → отдельный PR sync+archive (образец #35/#37).

Откат: миграция обратима (`goose down`); контрактные изменения аддитивны (старые
клиенты не ломаются); UI-раздел изолирован (новый маршрут).

## Open Questions

Ключевые вопросы закрыты в решениях 1–11 и фиксируются ADR-0014 (модель полномочий
`read`/`write` на `iam:global`, контракт чтения и пагинация, идемпотентность/коды
assign/revoke, перечисление субъектов `DISTINCT`, обобщение `authorize`,
fail-closed, границы scope). Открытых вопросов на момент дизайна нет.
