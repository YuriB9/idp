## 1. Подготовка

- [x] 1.1 Сверка с docs/IDP_MVP_plan.md (Этап 3, RBAC/IAM) и ADR-0003/0009/0010/0011/0012/0013/0014; зафиксировать решения design.md как ограничения
- [x] 1.2 Создать ветку `change/iam-dynamic-roles` от `master` (прямые коммиты в master запрещены)

## 2. Контракт proto/idm/v1 (аддитивно)

- [x] 2.1 Добавить в `proto/idm/v1/idm.proto` сервис `IamCatalogService` с RPC `CreateRole`, `DeleteRole`, `CreatePermission`, `DeletePermission`, `AttachPermission`, `DetachPermission` (комментарии на русском)
- [x] 2.2 Расширить сообщения `Role` (`bool system`) и `Permission` (`bool system`) новыми номерами полей; добавить `RolePermissions{role, permissions[]}` и запросы/ответы новых RPC; НЕ менять `AccessService`/`RoleAdminService`/читающие RPC `IamAdminService`
- [x] 2.3 `make proto` (`buf`, `GOWORK=off`, пин `./tools`) → обновить `pkg/api/idm/**`; убедиться, что `*.pb.go` перегенерированы и не правлены руками; `gen:check` (proto) зелёный

## 3. БД: миграции (обратимые)

- [x] 3.1 `services/idm/migrations/0007_roles_permissions_system_flag.sql`: `ALTER TABLE roles/permissions ADD COLUMN system boolean NOT NULL DEFAULT false`; `UPDATE ... SET system=true` для всех существующих строк; `Down` — `DROP COLUMN system` у обеих таблиц (комментарии на русском)
- [x] 3.2 `services/idm/migrations/0008_seed_iam_manage_demo.sql`: право `(manage, iam:global)` (`system=true`), привязка к роли `iam-admin`; идемпотентно (`ON CONFLICT DO NOTHING`); корректный `Down` (снять привязку, удалить право)
- [x] 3.3 `migrate-idm` (локальная БД может быть недоступна в этой среде — применение при запуске стенда); SQL зеркалит существующие обратимые seed'ы

## 4. IDM: repository (мутации каталога)

- [x] 4.1 `CreateRole(name)` / `CreatePermission(action,resource)`: INSERT; UNIQUE-violation → `errs.ErrConflict`; возврат созданной сущности с `system=false`
- [x] 4.2 `DeleteRole(name)` / `DeletePermission(action,resource)`: в транзакции проверка `system=true` → `errs.ErrPrecondition`; цель не найдена → `errs.ErrNotFound`; DELETE (FK каскад снимает `subject_roles`/`role_permissions`)
- [x] 4.3 `AttachPermission(role,action,resource)` / `DetachPermission(...)`: в транзакции проверка системности РОЛИ → `errs.ErrPrecondition`; роль/право (для attach) не найдены → `errs.ErrNotFound`; `INSERT ... ON CONFLICT DO NOTHING` / `DELETE` (идемпотентно); вернуть актуальный набор прав роли
- [x] 4.4 Читающие методы (`ListRoles`/`ListPermissions`/`GetRolePermissions`) — добавить выдачу `system`
- [x] 4.5 Integration-тесты repository (тег `integration`, DSN из `IDM_TEST_DSN`, Skip без БД): create/delete роли и права, attach/detach, UNIQUE-конфликты, каскад удаления роли, защита системных (`ErrPrecondition`), NotFound

## 5. IDM: usecase и gRPC-сервер

- [x] 5.1 Catalog-manager (usecase): обёртка repo-мутаций, ПОСЛЕ commit — широкая инвалидация `cache.InvalidateAll()` (поколение `idm:cache:gen`); assign/revoke (`RoleManager`) не трогать (точечный остаётся)
- [x] 5.2 Реализовать `iamCatalogServer` (gRPC `IamCatalogService`) в `services/idm`: валидация (пустые/битые поля → `InvalidArgument`), маппинг `ErrConflict→AlreadyExists`, `ErrPrecondition→FailedPrecondition`, `ErrNotFound→NotFound`, `ErrValidation→InvalidArgument`, ошибка БД/кэша → `Unavailable` (fail-closed, деталь в лог по ключу slog `err`); зарегистрировать сервер в `main.go`
- [x] 5.3 Unit/usecase-тесты (table-driven + t.Parallel(), miniredis): структурная мутация бампит поколение; assign/revoke — точечно `InvalidateSubject`; чтение без эффектов; защита системных; коды AlreadyExists/FailedPrecondition/NotFound/InvalidArgument; fail-closed Unavailable; goleak в пакетах с горутинами

## 6. gateway: структурные ручки /iam под manage

- [x] 6.1 Добавить gRPC-клиент `IamCatalogServiceClient` в `main.go`; обработчики структурных ручек; `CheckAccess(manage, iam:global)` через существующий `authorizeResource` перед КАЖДОЙ мутацией (fail-closed → 403)
- [x] 6.2 `POST /iam/roles` (201/409/400), `DELETE /iam/roles/{role}` (200/404/422)
- [x] 6.3 `POST /iam/roles/{role}/permissions` (attach, тело `{action,resource}`, 200/404/422/400), `DELETE /iam/roles/{role}/permissions?action=&resource=` (detach, 200/404/422/400); идемпотентны; ответ — актуальный набор прав роли
- [x] 6.4 `POST /iam/permissions` (201/409/400), `DELETE /iam/permissions?action=&resource=` (200/404/422); маппинг через `httpFromGRPC` (reuse)
- [x] 6.5 Тесты gateway со стабом IDM (без сети, table-driven + t.Parallel()): deny→403, IDM недоступен→403, успех, дубль→409, системная→422, NotFound→404, валидация→400, идемпотентный attach/detach; регресс read/write-ручек отсутствует (старые тесты зелёные)
- [x] 6.6 `go build` gateway, затем `git checkout -- services/gateway/gateway` (не коммитить бинарь)

## 7. Периметр OpenAPI + TS-клиент

- [x] 7.1 Добавить в `openapi/openapi.yaml` пути `POST/DELETE /iam/roles`, `POST/DELETE /iam/roles/{role}/permissions`, `POST/DELETE /iam/permissions`; схемы `Role` (с `system`), `Permission` (с `system`), `RolePermissions`, тела запросов; для КАЖДОЙ операции — summary + description + operationId + ВСЕ коды (200/201/400/403/404/409/422)
- [x] 7.2 `web npm run gen` (gen:types+gen:zod+gen:spec → копия в `web/public/openapi.yaml`); `gen:check` (src/api + public) зелёный
- [x] 7.3 `npm run lint:openapi` (Spectral) и `make conformance` (Schemathesis против стенда) — конформны: все коды документированы, мутации идемпотентны или с явными кодами конфликта

## 8. Портал «Роли и доступы» (расширение)

- [x] 8.1 Формы создания роли и создания права (react-hook-form + zod), TanStack-мутации + invalidate `["iam","roles"]`/`["iam","permissions"]`
- [x] 8.2 Удаление пользовательской роли/права; attach/detach прав роли из списка прав (обновление набора прав роли из тела ответа `RolePermissions`)
- [x] 8.3 Системные роли/права (`system=true`) — read-only: бейдж «системная», кнопки удаления/правки скрыты/заблокированы
- [x] 8.4 Обработка 403 (нет `manage` — формы скрыты/заблокированы), 404, 409 (дубль), 422 (системная); рантайм-валидация ответов zod `.parse`; без раскрытия сырых внутренних ошибок
- [x] 8.5 vitest-тесты: happy (просмотр), 403 (нет права), create-delete роли, attach-detach права, системная роль read-only, валидация формы

## 9. Документация и финализация

- [x] 9.1 README services/idm: что теперь можно создавать/удалять роли и права, защита системных ролей, какое право нужно (`read`/`write`/`manage`), широкая инвалидация кэша после структурной правки, проверка отказа/успеха, локальный seed (`make migrate-idm`)
- [x] 9.2 Опубликовать ADR-0015 в `docs/adr/0015-iam-dynamic-catalog-manage-and-system-protection.md` (вне openspec/)
- [x] 9.3 `GOWORK=off go mod tidy` в затронутых модулях при новых общих зависимостях; тесты (idm/gateway, -race), golangci-lint [errname/paralleltest], govulncheck, кодоген идемпотентен (proto+OpenAPI+TS+public). govulncheck/integration/conformance — в CI при недоступной локальной БД
- [x] 9.4 PR с зелёным CI (go test всех модулей, golangci-lint, govulncheck, gen:check, openapi-lint [Spectral], web-test [tsc+vitest], integration) — PR #46 смержен; sync+archive — этот PR
