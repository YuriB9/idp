## 1. Подготовка и сверка

- [x] 1.1 Сверить объём с docs/IDP_MVP_plan.md (Этап 3 «Изменение владельцев», БЛОК 2/4/5) и ADR-0001/0003/0004/0005/0008/0010/0011; зафиксировать расхождения, если есть
- [x] 1.2 Создать ветку `change/change-service-owners` от актуального master (прямые коммиты в master запрещены)
- [x] 1.3 Убедиться, что пинованные бинарники инструментов собраны (`make tools`, `tools/bin`: buf, protoc-gen-go(-grpc), goose, golangci-lint)

## 2. Контракты (proto + кодоген)

- [x] 2.1 Расширить `proto/projects/v1/projects.proto`: добавить `repeated string owners` и `owners_version` в `Service`/`GetServiceResponse`/элементы `ListServicesResponse` (новые номера полей, комментарии на русском)
- [x] 2.2 Добавить RPC `SetServiceOwners` + сообщения `SetServiceOwnersRequest{project,name,owners,expected_version}`/`SetServiceOwnersResponse{owners,owners_version}` (пометить BREAKING в комментарии)
- [x] 2.3 Расширить `proto/idm/v1/idm.proto`: управляющие RPC `AssignRole`/`RevokeRole` (`subject`,`role`), идемпотентные; комментарии на русском
- [x] 2.4 Прогнать `buf generate` → обновить `pkg/api/projects/v1` и `pkg/api/idm/v1`; убедиться, что сборка модулей проходит
- [x] 2.5 Регенерировать TS-клиент портала (`web/src/api`) и обновить OpenAPI; добиться зелёного `gen:check` (proto+OpenAPI+TS)

## 3. Каталог: схема и repository (services/projects)

- [x] 3.1 Миграция goose `services/projects/migrations` (обратимая): таблица `service_owners(service_id FK ON DELETE CASCADE, owner, PK(service_id,owner))` + колонка `services.owners_version int NOT NULL DEFAULT 0`; проверить `up`/`down`
- [x] 3.2 Расширить `repository/model.go`: добавить `Owners []string` и `OwnersVersion int` в `Service`
- [x] 3.3 Реализовать в repository чтение владельцев в `Get` и батч-загрузку для `List` (без N+1, keyset-пагинация не ломается, детерминированный порядок owners)
- [x] 3.4 Реализовать `SetOwners(ctx, serviceID, desired, expectedVersion)` в транзакции: diff → DELETE/INSERT в `service_owners` + guarded-CAS инкремент `owners_version` (RowsAffected==0 → `errs.ErrConflict`; отсутствие записи → `errs.ErrNotFound`)
- [x] 3.5 Тесты repository: стаб/in-memory в дефолте + pgx под тегом `integration` (DSN из `*_TEST_DSN`, Skip без БД): guarded-CAS-конфликт по версии, уникальность `(service_id,owner)`, каскад FK, идемпотентность

## 4. Каталог: usecase, gRPC, workflow-starter (services/projects)

- [x] 4.1 Usecase: метод смены владельцев (валидация/нормализация набора: непустые, без дублей; вычисление diff против текущего состояния)
- [x] 4.2 Создать публичный пакет `services/projects/changeowners`: имена workflow/activities, детерминированный `WorkflowID = change-owners:<project>:<name>`, входные типы, единые `ActivityOptions` (по образцу `provisioning`)
- [x] 4.3 Реализовать тело `ChangeOwnersWorkflow` (детерминированно): шаги GitLab→Vault→CatalogSetOwners(точка невозврата)→IDMSyncOwnerRoles; компенсации до точки невозврата; алерт оператору после; no-op при пустом diff
- [x] 4.4 Расширить grpcapi `server.go`: реализовать `SetServiceOwners` (валидация, маппинг ошибок: NotFound/FailedPrecondition/InvalidArgument), отразить `owners`/`owners_version` в `GetService`/`ListServices`
- [x] 4.5 Обобщить `authorize(ctx, project, action)` и вызвать с `"change_owners"` перед операцией (defense-in-depth, fail-closed → PermissionDenied)
- [x] 4.6 Реализовать запуск `ChangeOwnersWorkflow` в `wfstarter` (детерминированный WorkflowID, ReusePolicy)
- [x] 4.7 Тесты grpcapi со стабом IDM (без сети): deny/недоступность → PermissionDenied, owners в ответах, маппинг конфликта версии; Temporal testsuite для workflow: happy-path, компенсация до точки невозврата, конфликт guarded-CAS, сбой IDM после точки невозврата, no-op (goleak где есть горутины)

## 5. DevInfra worker: интеграции и activities (services/devinfra-worker)

- [x] 5.1 Расширить моки/интерфейсы `integrations`: GitLab sync/restore members, Vault sync/restore policies владельцев (SSRF-guard на исходящих, секреты не логировать)
- [x] 5.2 Реализовать activities владельцев + идемпотентные компенсации; `CatalogSetOwners` (guarded-CAS, конфликт → non-retryable); `IDMSyncOwnerRoles` (Assign/Revoke + InvalidateSubject), классификация неустранимых ошибок в non-retryable ApplicationError
- [x] 5.3 Зарегистрировать новые activities под именами из `changeowners` (по образцу `register.go`)
- [x] 5.4 Тесты activities: happy-path, идемпотентность, классификация ошибок (table-driven, t.Parallel)

## 6. IDM: управляющие RPC ролей и инвалидация (services/idm)

- [x] 6.1 Реализовать gRPC `AssignRole`/`RevokeRole` поверх `subject_roles` (идемпотентно; пустые поля → InvalidArgument; несуществующая роль → NotFound; защитить путь от публичного доступа)
- [x] 6.2 После изменения привязок вызывать `InvalidateSubject` (или инвалидацию поколением); не оставлять устаревшие allow/deny
- [x] 6.3 Миграция/seed: per-project роль `owner:project:demo` с правами (`read`/`list`/`change_owners` на `project:demo`); право `(change_owners, project:demo)` субъекту `demo-user`; обратимая миграция goose, идемпотентный seed
- [x] 6.4 Тесты IDM на стабах/miniredis: идемпотентность Assign/Revoke, инвалидация кэша затронутого субъекта, NotFound/InvalidArgument

## 7. Периметр (services/gateway)

- [x] 7.1 Добавить маршрут `PUT /projects/{project}/services/{name}/owners` → gRPC `SetServiceOwners`; валидация тела (owners непустые/без дублей, обязательный `owners_version`)
- [x] 7.2 RBAC: `authorize(w,r,project,"change_owners")` до проксирования (fail-closed → 403)
- [x] 7.3 Отразить `owners`/`owners_version` в ответах `GET`/`LIST`; убедиться в маппинге `httpFromGRPC` (FailedPrecondition→409, NotFound→404, InvalidArgument→400, PermissionDenied→403)
- [x] 7.4 Тесты handlers со стабами клиентов (без сети): 200/202 happy, 400/409/404/403, owners в ответах

## 8. Портал (web)

- [x] 8.1 Обновить zod-схемы/типы API (owners, owners_version) и рантайм-валидацию `.parse`
- [x] 8.2 Отобразить владельцев на экранах чтения (список/карточка)
- [x] 8.3 Форма изменения владельцев (react-hook-form + zod) + мутация TanStack Query на `PUT .../owners`; обработка 403 и 409 (понятные сообщения, без сырых ошибок)
- [x] 8.4 Тесты компонентов/формы (happy, отказ 403, конфликт 409)

## 9. Локалка и документация

- [x] 9.1 Проверить применение миграций владельцев мигратором `migrate-projects`; при необходимости расширить демо-данные (сервис с начальным владельцем)
- [x] 9.2 Прогнать сквозной сценарий локально при включённом RBAC (`demo-user`): смена владельца → синхронизация ролей IDM и доступа, проверка отказа без права
- [x] 9.3 Обновить README `services/projects`/`services/idm` и инструкцию: модель владельцев, как сменить владельца, влияние на роли/доступ, как проверить отказ/разрешение

## 10. Качество и CI

- [x] 10.1 `GOWORK=off go mod tidy` во всех затронутых модулях (projects, devinfra-worker, idm, gateway, e2e) — tidy-check/govulncheck
- [x] 10.2 Прогнать тесты модулей (+`integration` при доступной БД), golangci-lint (errname/paralleltest), govulncheck, `gen:check`
- [x] 10.3 Открыть PR с зелёным CI; после merge в master — `/opsx:archive` (только после merge)
