## 1. Подготовка

- [x] 1.1 Сверка с docs/IDP_MVP_plan.md (Этап 3, RBAC/IAM) и ADR-0003/0009/0010/0011/0012/0013/0014/0015; зафиксировать решения design.md как ограничения
- [x] 1.2 Создать ветку `change/iam-subject-directory` от `master` (прямые коммиты в master запрещены)

## 2. Контракт proto/idm/v1 (аддитивно)

- [x] 2.1 Добавить в `proto/idm/v1/idm.proto` читающий сервис `IdentityService` с RPC `SearchSubjects(query,cursor,page_size)` и `ResolveSubjects(subjects[])` (комментарии на русском)
- [x] 2.2 Добавить сообщение `SubjectIdentity{subject, username, email, display_name, enabled, found}` и запросы/ответы новых RPC (`SearchSubjectsResponse{subjects[], next_cursor}`); НЕ менять `AccessService`/`RoleAdminService`/`IamAdminService`(вкл. `ListSubjectsWithRoles`)/`IamCatalogService`
- [x] 2.3 `make proto` (`buf`, `GOWORK=off`, пин `./tools`) → обновить `pkg/api/idm/**`; убедиться, что `*.pb.go` перегенерированы и не правлены руками; `gen:check` (proto) зелёный

## 3. БД: миграции (обратимые)

- [x] 3.1 `services/idm/migrations/NNNN_subject_seeds_to_dev_uuid.sql`: перенести сиды `subject_roles` со строки `demo-user` на детерминированный UUID `dev`; `Down` возвращает на `demo-user` (комментарии на русском)
- [x] 3.2 `services/idm/migrations/NNNN_seed_iam_directory_read.sql`: право `(read, iam:directory)` (`system=true`), привязка к роли `iam-admin`; идемпотентно (`ON CONFLICT DO NOTHING`); корректный `Down` (снять привязку, удалить право)
- [x] 3.3 `migrate-idm` (локальная БД может быть недоступна в этой среде — применение при запуске стенда); SQL зеркалит существующие обратимые seed'ы

## 4. IDM: слой identity (клиент Keycloak Admin REST)

- [x] 4.1 `internal/identity`: получение токена сервис-аккаунта по `client_credentials` (кэш в памяти до истечения с запасом); базовый URL/realm/client-id/secret из env; секрет не логировать
- [x] 4.2 Клиент Admin REST: поиск `GET /admin/realms/{realm}/users?search=&first=&max=` (offset→opaque-курсор) и резолв набора `sub` → `SubjectIdentity` (`found=false` для отсутствующих); маппинг полей username/email/display_name/enabled
- [x] 4.3 ВСЕ исходящие вызовы (токен + Admin REST) — через `pkg/ssrf` (ValidateURL на конфиге + GuardedDialContext в транспорте) и `pkg/httpclient`; `SSRF_DISABLED` только локалка; таймауты, ограниченные ретраи на сетевые (без ретрая на 4xx)
- [x] 4.4 Кэш идентичностей в DragonflyDB (отдельный namespace `idm:identity:*`, TTL; ключи resolve:<sub> и search:<хэш>); singleflight против стампеда; НЕ трогать decision-cache (`idm:cache:gen`/`InvalidateSubject`)
- [x] 4.5 Тесты identity-клиента (httptest-стаб Keycloak Admin REST, table-driven + t.Parallel(), goleak): поиск/пагинация, резолв, осиротевший (`found=false`), ошибка/таймаут/5xx → `Unavailable`, выдача токена `client_credentials`, отсутствие секрета в ошибках; кэш (miniredis): hit/TTL и что decision-cache не затронут

## 5. IDM: usecase и gRPC-сервер identity

- [x] 5.1 Usecase-фасад справочника (поиск/резолв поверх клиента + кэш); валидация ввода (пустой/короткий query, пустой список, лимит page_size) → `ErrValidation`
- [x] 5.2 Реализовать `identityServer` (gRPC `IdentityService`) в `services/idm`: маппинг `ErrValidation→InvalidArgument`, недоступность Keycloak/токена → `Unavailable` (fail-closed, деталь в лог по ключу slog `err`, без секрета); зарегистрировать сервер в `main.go`; новые env
- [x] 5.3 Unit/usecase-тесты (table-driven + t.Parallel(), miniredis): happy поиск/резолв, осиротевший, `InvalidArgument`, `Unavailable`; подтвердить, что справочник не вызывает `InvalidateAll`/`InvalidateSubject`; goleak

## 6. gateway: ручки справочника и обогащение

- [x] 6.1 Добавить gRPC-клиент `IdentityServiceClient` в `main.go`; обработчики `/iam/directory/*` под `authorizeResource(iam:directory, read)` (fail-closed → 403)
- [x] 6.2 `GET /iam/directory/subjects?search=&cursor=&page_size=` → 200 `{subjects[], next}` / 400 / 403 / 503 (Keycloak недоступен)
- [x] 6.3 `POST /iam/directory/subjects/resolve` (тело `{subjects[]}`) → 200 `{subjects[]}` (осиротевшие `found=false`) / 400 / 403 / 503
- [x] 6.4 Обогащение `GET /iam/subjects`: после `ListSubjectsWithRoles` (под `(read,iam:global)`) — при наличии `(read,iam:directory)` вызвать `ResolveSubjects` и слить идентичности; без права — «сырой» ответ; недоступность Keycloak при резолве → деградация (200 без идентичностей)
- [x] 6.5 Маппинг кодов: reuse `httpFromGRPC` + `Unavailable` от справочника → `503` (а не 500); внутренние ошибки/секреты наружу не раскрывать
- [x] 6.6 Тесты gateway со стабом IDM (без сети, table-driven + t.Parallel()): deny→403, IDM недоступен→403, успех поиска/резолва, Keycloak недоступен→503, обогащение `GET /iam/subjects` (с правом/без права/деградация), осиротевший субъект, пустой/битый ввод→400; регресс старых /iam-ручек отсутствует
- [x] 6.7 `go build` gateway, затем `git checkout -- services/gateway/gateway` (не коммитить бинарь)

## 7. Периметр OpenAPI + TS-клиент

- [x] 7.1 Добавить в `openapi/openapi.yaml` пути `GET /iam/directory/subjects`, `POST /iam/directory/subjects/resolve`; схему `SubjectIdentity` и обёртки ответов; для КАЖДОЙ операции — summary + description + operationId + ВСЕ коды (200/400/403/503)
- [x] 7.2 Обновить схему ответа `GET /iam/subjects` — аддитивно/опционально добавить поля идентичности (`username`/`email`/`display_name`/`enabled`/`found`); не ломать существующие коды
- [x] 7.3 `web npm run gen` (gen:types+gen:zod+gen:spec → копия в `web/public/openapi.yaml`); `gen:check` (src/api + public) зелёный; zod-поля идентичности optional
- [x] 7.4 `npm run lint:openapi` (Spectral) и `make conformance` (Schemathesis против стенда) — конформны: все коды документированы, GET-ручки справочника конформны

## 8. Портал «Роли и доступы» (расширение)

- [x] 8.1 Пикер пользователя с поиском (debounce) → `GET /iam/directory/subjects`; выбор пользователя подставляет `subject` = канонический `sub`; назначение в UI ТОЛЬКО через пикер (поле ручного ввода subject убрано; периметр строки по-прежнему принимает) (react-hook-form + zod, TanStack)
- [x] 8.2 Отображение `username`/`email` рядом с субъектами в списке (из обогащённого `GET /iam/subjects`); «осиротевший» (`found=false`) — raw `sub` с пометкой «нет в каталоге»
- [x] 8.3 Индикация «каталог недоступен» (503/отсутствие идентичностей) без поломки управления ролями по сырому subject; при 403 (нет `iam:directory`) — пикер/поиск скрыт, имена не показываются, назначение по строке работает
- [x] 8.4 Рантайм-валидация ответов zod `.parse` (поля идентичности optional); без раскрытия сырых внутренних ошибок
- [x] 8.5 vitest-тесты: поиск/пикер с debounce, happy-назначение по пикеру, отображение имён, осиротевший субъект, 403 (нет права), «каталог недоступен» (503), валидация ввода

## 9. Локалка: Keycloak realm и compose

- [x] 9.1 `deploy/keycloak/idp-realm.json`: confidential-клиент сервис-аккаунта (realm-management `view-users`/`query-users`), детерминированный UUID для `dev`, несколько демо-пользователей (комментарии/правки воспроизводимы при импорте)
- [x] 9.2 `deploy/compose/docker-compose.yml`: env Keycloak Admin для IDM (адрес/realm/client-id/secret, TTL кэша, `SSRF_DISABLED` локально); `AUTH_DISABLED_SUBJECT`=UUID `dev` (gateway/projects); секрет не в логи

## 10. Документация и финализация

- [x] 10.1 README services/idm и корневой README: устройство справочника субъектов, нужный сервис-аккаунт, резолв `sub`→идентичность, требуемое право (`read, iam:directory`), поведение UI при недоступном Keycloak, проверка поиска/назначения, канонический ключ `sub` и сведение demo-user/dev
- [x] 10.2 Опубликовать ADR-0016 в `docs/adr/0016-iam-subject-directory-from-oidc.md` (вне openspec/)
- [x] 10.3 `GOWORK=off go mod tidy` в затронутых модулях при новых общих зависимостях; тесты (idm/gateway, -race), golangci-lint [errname/paralleltest], govulncheck, кодоген идемпотентен (proto+OpenAPI+TS+public). govulncheck/integration/conformance — в CI при недоступной локальной БД
- [ ] 10.4 PR с зелёным CI (go test всех модулей, golangci-lint, govulncheck, gen:check, openapi-lint [Spectral], web-test [tsc+vitest], integration, conformance); merge → отдельный PR sync+archive (образец #46/#47)
