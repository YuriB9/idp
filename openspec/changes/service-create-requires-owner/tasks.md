## 1. Контракт и кодоген (BREAKING)

- [x] 1.1 proto: добавить `repeated string owners = 3;` в `CreateServiceRequest` (`proto/projects/v1/projects.proto`) с русским комментарием; пометить добавление поля BREAKING в комментарии.
- [x] 1.2 openapi: в `CreateServiceRequest` (`openapi/openapi.yaml`) сделать `required:[name, owners]`, добавить `owners: {type: array, minItems: 1, items: {type: string, minLength: 1}}` с русским описанием.
- [x] 1.3 Перегенерировать Go-стабы buf (`pkg/api/projects/v1`) инструментами из `./tools` (GOWORK=off); сгенерированное НЕ править руками.
- [x] 1.4 Перегенерировать web-кодоген: `openapi-typescript` + `openapi-zod-client` + обновить копию `web/public/openapi.yaml`.
- [x] 1.5 Прогнать `gen:check` — убедиться, что diff после перегенерации пуст (зелёный).

## 2. Репозиторий: атомарная вставка владельцев

- [x] 2.1 Сделать вставку записи транзакционной: строка `services` (status=`creating`) + строки `service_owners` + `owners_version = 1` в одной транзакции; принимать нормализованных владельцев на вход (`insertService`/`Repo.Create`/`TxRepo.Create`).
- [x] 2.2 Гарантировать `owners_version = 1` при непустых владельцах (консистентно с первой `SetOwners` 0→1); пустой набор на этом слое недопустим (защита, основная валидация — в usecase).
- [x] 2.3 Интеграционный тест репозитория: вставка с владельцами возвращает запись с непустыми owners и `owners_version=1`; повторная вставка дубля `(project,name)` → `ErrConflict`.

## 3. Usecase каталога

- [x] 3.1 `CreateRecord` принимает `owners []string` и прокидывает их в транзакционную вставку.
- [x] 3.2 `CreateService` принимает `owners`, нормализует через `normalizeOwners`, отклоняет пустой набор после нормализации (`InvalidArgument`/`errs`) ДО вставки и запуска воркфлоу.
- [x] 3.3 Сохранить строгий порядок «запись первой» и best-effort guarded-CAS `creating→failed` при сбое запуска воркфлоу.
- [x] 3.4 Расширить интерфейс starter: `StartCreateService(..., owners []string)`; обновить реализацию `wfstarter` (прокинуть в `CreateServiceInput.Owners`).
- [x] 3.5 Unit-тесты usecase: happy-path с владельцами (вставка + старт), отказ при пустых владельцах (нет вставки/старта), сбой запуска → FAILED.

## 4. Воркфлоу провизии (вариант B)

- [x] 4.1 Добавить поле `Owners []string` в `provisioning.CreateServiceInput` (русский комментарий).
- [x] 4.2 Встроить шаги владельцев в `CreateServiceWorkflow` в порядке: GitLab create → GitLab sync members (`add=owners, remove=[]`) → Harbor → Vault setup → Vault sync policies (`add=owners`) → inject secrets → активация → IDM sync owner roles (`add=owners`).
- [x] 4.3 Переиспользовать типы/имена activities `changeowners` (`SyncMembersInput`, `IDMSyncInput`); компенсации членства/политик НЕ добавлять отдельно (поглощаются delete repo / teardown Vault).
- [x] 4.4 IDM-шаг — ПОСЛЕ активации, best-effort: при исчерпании ретраев алерт оператору (структурный лог `error`), без молчаливого отката.
- [x] 4.5 Проверить регистрацию activities владельцев (`WithOwners`/`WithIDMRoles`) на worker'е, исполняющем `CreateServiceWorkflow` (`services/devinfra-worker`); подключить опциями, если не подключены.
- [x] 4.6 Temporal-тесты: happy-path (порядок шагов + назначение владельцев), сбой до активации → компенсации в обратном порядке + FAILED, сбой IDM после активации → сервис остаётся active + алерт.

## 5. Gateway (периметр)

- [x] 5.1 `createServiceBody`: добавить `Owners []string` (`json:"owners"`).
- [x] 5.2 Валидация в `create`: непустой массив, без пустых строк, без дублей → 400 через существующий маппинг (как в `setOwners`), без сырых деталей.
- [x] 5.3 Проброс `Owners` в `projectsv1.CreateServiceRequest`.
- [x] 5.4 Тесты gateway: happy-path с владельцами; 400 при пустых/дублирующихся/пустых-строках владельцах.

## 6. Портал (форма создания + степпер)

- [x] 6.1 `CreateServicePage`: добавить обязательное поле владельцев (textarea), разбор через `parseOwners` (экспорт из `OwnersCard`); маппинг `text→owners[]` перед `mutate`.
- [x] 6.2 Источник валидации — zod-схема `CreateServiceRequest` из кодогена (owners как массив); ошибка пустых владельцев — inline + тост (`lib/errors`).
- [x] 6.3 По успеху — прежний переход на единый прогресс (`ServiceProgressPage`), без изменений механики поллинга.
- [~] 6.4 (UX, опционально) предлагать текущего пользователя как дефолт владельцев — ОТЛОЖЕНО (требует прокидывания личности текущего пользователя в форму; не входит в обязательность, см. design D5).
- [x] 6.5 `web/src/lib/workflow-steps.ts`: обновить подписи `OPERATION_STEPS.create` под порядок п.4.2 (добавить шаги назначения владельцев GitLab/Vault/IDM); грубая гранулярность не меняется.
- [x] 6.6 Web-тесты (vitest): обязательность владельцев (нет запроса при пустых), happy-path с владельцами и переход на прогресс; тест на обновлённые `OPERATION_STEPS.create`.

## 7. E2E и обновление вызывающих (BREAKING)

- [x] 7.1 Обновить `tests/e2e/harness_test.go` (`mustCreateActive`) — передавать владельцев в `createService`.
- [x] 7.2 Обновить `tests/e2e/stories_test.go` (`TestStoryCreateService`) — создание с владельцами; проверить, что `getService` отражает непустой набор владельцев.
- [x] 7.3 Обновить `tests/e2e/errors_test.go` и `{gitlab,vault,harbor}_integration_test.go` — все вызовы создания передают владельцев; добавить сценарий «создание без владельцев → 400».
- [x] 7.4 Поллинг без `sleep`, уникальные имена — реюз харнесса (ADR-0018).

## 8. Финал и CI

- [ ] 8.1 Все комментарии в новом/изменённом коде — только на русском.
- [ ] 8.2 Локально зелёные: go test всех модулей `-race -shuffle`, golangci-lint, govulncheck, `gen:check`, openapi-lint, web-test (vitest + typecheck), integration.
- [ ] 8.3 PR из ветки `change/service-create-requires-owner` в master с зелёным CI; после merge — `/opsx:archive` отдельным PR sync+archive.
