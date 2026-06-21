## 1. Подготовка

- [x] 1.1 Сверка с docs/IDP_MVP_plan.md (Этап 3, RBAC/IAM) и ADR-0003/0009/0010/0011/0012/0013; зафиксировать решения design.md как ограничения
- [x] 1.2 Создать ветку `change/iam-roles-portal` от `master` (прямые коммиты в master запрещены)

## 2. Контракт proto/idm/v1 (аддитивно)

- [x] 2.1 Добавить в `proto/idm/v1/idm.proto` сервис `IamAdminService` с RPC `ListRoles`, `ListPermissions`, `GetRolePermissions`, `ListSubjectsWithRoles`, `GetSubjectRoles` (комментарии на русском)
- [x] 2.2 Добавить сообщения `Role{name}`, `Permission{action,resource}`, `SubjectRoles{subject,roles[]}` и запросы/ответы (keyset `page_size`/`page_token`/`next_page_token` для субъектов); НЕ менять существующие `AccessService`/`RoleAdminService`
- [x] 2.3 `buf generate` (GOWORK=off, пин `./tools`) → обновить `pkg/api/idm/**`; убедиться, что `*.pb.go` перегенерированы и не правлены руками

## 3. IDM: repository (только чтение)

- [x] 3.1 `ListRoles`/`ListPermissions`: SELECT всех ролей (по `name`) и прав (`action`,`resource`) из Postgres
- [x] 3.2 `GetRolePermissions(role)`: права роли join'ом; роль не найдена → `errs.ErrNotFound`
- [x] 3.3 `ListSubjectsWithRoles(pageSize, pageToken)`: `DISTINCT subject` с агрегированием ролей (`array_agg`/GROUP BY), keyset по `subject` ASC, без N+1; вернуть `next_page_token`
- [x] 3.4 `GetSubjectRoles(subject)`: роли субъекта (пусто, не ошибка, если ролей нет)
- [x] 3.5 Integration-тесты repository (тег `integration`, DSN из `IDM_TEST_DSN`, Skip без БД): листинг, права роли, NotFound, keyset-страницы, субъект без ролей

## 4. IDM: usecase и gRPC-сервер

- [x] 4.1 Read-путь: repository как `catalogReader` транспорта (без побочных эффектов на кэш); reuse существующего `RoleManager` для assign/revoke (уже делает `InvalidateSubject`) — отдельный usecase-обёртка не нужна (нет логики/кэша)
- [x] 4.2 Реализовать `iamAdminServer` (gRPC `IamAdminService`) в `services/idm/iam_admin_server.go`: валидация (пустые поля → `InvalidArgument`), маппинг `ErrNotFound→NotFound`, `ErrValidation→InvalidArgument`, fail-closed (ошибка БД → `Unavailable`, деталь в лог по ключу slog `err`); зарегистрировать сервер
- [x] 4.3 Unit-тесты сервера (table-driven + t.Parallel(), стаб-каталог без БД): чтение, NotFound, InvalidArgument, fail-closed Unavailable, keyset

## 5. gateway: обобщённый authorize и ручки /iam

- [x] 5.1 Ввести `authorizeResource(w,r,resource,action)`; переписать `authorize(w,r,project,action)` как обёртку (`resource="project:"+project`) — без регресса project-вызовов
- [x] 5.2 Добавить gRPC-клиенты `IamAdminServiceClient` и `RoleAdminServiceClient` в `main.go`; ручки IAM на `servicesAPI` (iam_handlers.go), регистрация маршрутов под `/api`
- [x] 5.3 GET-ручки `/iam/roles`, `/iam/permissions`, `/iam/roles/{role}/permissions`, `/iam/subjects` (keyset), `/iam/subjects/{subject}/roles`: `CheckAccess(read, iam:global)` перед проксированием; маппинг через `httpFromGRPC`
- [x] 5.4 Мутации `POST`/`DELETE /iam/subjects/{subject}/roles/{role}`: `CheckAccess(write, iam:global)`; assign/revoke идемпотентны; ответ `200` с актуальным набором ролей субъекта (после мутации — `GetSubjectRoles`)
- [x] 5.5 Тесты gateway со стабом IDM (без сети, table-driven + t.Parallel()): deny→403, IDM недоступен→403, успех чтения/мутации, идемпотентный повтор, NotFound→404, BadPageSize→400; регресс project-ручек отсутствует (старые тесты зелёные)
- [x] 5.6 `go build` gateway, затем `git checkout -- services/gateway/gateway` (не коммитить бинарь)

## 6. Периметр OpenAPI + TS-клиент

- [x] 6.1 Добавить в `openapi/openapi.yaml` пути `/iam/roles`, `/iam/permissions`, `/iam/roles/{role}/permissions`, `/iam/subjects` (keyset), `/iam/subjects/{subject}/roles`, `POST`/`DELETE /iam/subjects/{subject}/roles/{role}`; схемы Role/Permission/SubjectRoles/списки; коды 200/400/403/404
- [x] 6.2 `web npm run gen` → TS-клиент/zod регенерированы (listRoles/listPermissions/getRolePermissions/listSubjects/getSubjectRoles/assignRole/revokeRole)

## 7. Портал «Роли и доступы»

- [x] 7.1 Новый маршрут react-router `/iam` и страница раздела (горизонтальный, не в project-scope); ссылка в каркасе (GlobalLayout)
- [x] 7.2 Read-only таблицы ролей с правами (выбор роли → её права) и субъектов с ролями (useInfiniteQuery, keyset «показать ещё» по `next_page_token`, рантайм-валидация zod `.parse`)
- [x] 7.3 Форма назначить/снять роль (react-hook-form + zod), TanStack-мутация + invalidate `["iam","subjects"]`; индикация результата
- [x] 7.4 Обработка 403 (раздел заблокирован/отказ, без содержимого), 404, 400; без раскрытия сырых внутренних ошибок
- [x] 7.5 vitest-тесты: happy (просмотр), 403 (нет права), назначение-снятие (идемпотентность), валидация формы (5 тестов зелёные)

## 8. Локалка (seed)

- [x] 8.1 Обратимая goose-миграция `services/idm/migrations/0006_seed_iam_admin_demo.sql`: роль `iam-admin`, права `(read, iam:global)` и `(write, iam:global)`, привязка `demo-user`; идемпотентно (`ON CONFLICT DO NOTHING`), корректный `Down`; комментарии на русском. Добавлен Makefile-таргет `migrate-idm` (отсутствовал)
- [x] 8.2 `migrate-idm` доступен (локальная БД недоступна в этой среде — применение при запуске стенда); SQL зеркалит существующие обратимые seed'ы

## 9. Документация и финализация

- [x] 9.1 README IDM: раздел про IamAdminService, авторизацию (`read`/`write` на `iam:global`), REST-ручки, идемпотентность, локальный seed (`make migrate-idm`), проверку отказа/успеха и инвалидацию кэша
- [x] 9.2 Опубликовать ADR-0014 в `docs/adr/0014-iam-admin-authorization-and-read-contract.md` (вне openspec/)
- [x] 9.3 Зависимости без изменений (go mod tidy — без дрейфа); тесты (idm/gateway, -race), golangci-lint (0 issues), кодоген идемпотентен (proto+TS стабильны). govulncheck/integration — в CI (локальная БД недоступна)
- [ ] 9.4 PR с зелёным CI; после merge — отдельный PR sync+archive (`/opsx:archive`, образец #35/#37)
