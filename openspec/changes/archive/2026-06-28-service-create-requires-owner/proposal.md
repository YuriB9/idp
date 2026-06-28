## Why

Сейчас создание сервиса принимает только `name`: запись каталога вставляется со
статусом `creating` и **без владельцев**, а владельцы назначаются отдельной
операцией смены владельцев уже после создания. Между созданием и первой сменой
владельцев сервис существует «без владельца» — нет ответственного субъекта ни в
каталоге, ни в GitLab/Vault/IDM. Это противоречит контракту владения (ADR-0011),
где у каждого сервиса должен быть владелец. Делаем владельцев обязательной частью
запроса создания и устанавливаем их атомарно с созданием — устранив окно
«бесхозного» сервиса.

Решение по глубине (закреплено в design.md и adr.md): **вариант B — полное
установление владельца**. Owners фиксируются в записи каталога атомарно при
вставке (`owners_version` стартует с 1), И воркфлоу создания устанавливает
реальные роли владельца во внешних системах (GitLab members → Vault policies →
IDM owner roles), переиспользуя activities из `changeowners`, с Saga-компенсацией
(ADR-0005). Владелец реально владеет «с первой секунды», консистентно с ADR-0011.
Вариант A (только запись в каталог) отвергнут: он оставляет владельца без
фактического доступа во внешних системах до отдельного `ChangeOwners`, то есть
воспроизводит ровно ту проблему «бесхозного сервиса», которую задача устраняет.

Соответствие: docs/IDP_MVP_План.md (DevInfra-провизия, владение сервисом), ADR-0001
(Temporal), 0004 (guarded-CAS), 0005 (Saga), 0008 (split воркфлоу), 0009 (форма
периметра), 0011 (owners/role-sync), 0016 (справочник субъектов IDM), 0017
(дизайн-система), 0018 (E2E), 0022 (единый прогресс).

## What Changes

- **BREAKING (контракт):** `CreateServiceRequest` получает обязательное поле
  `owners` на ОБОИХ контрактах:
  - proto: `repeated string owners = 3;`
  - openapi: `required: [name, owners]`, `owners` — массив `minItems: 1`,
    `items.minLength: 1`.
  - Перегенерация чистая: buf (proto→Go) и web-кодоген
    (`openapi-typescript` + `openapi-zod-client` + копия `public/openapi.yaml`);
    `gen:check` зелёный.
- **Каталог/usecase:** `CreateService`/`CreateRecord` принимают `owners`,
  нормализуют (`normalizeOwners`) и вставляют запись каталога ВМЕСТЕ с владельцами
  в одной транзакции (`owners_version = 1`). Строгий порядок «запись фиксируется
  первой» и best-effort→FAILED при сбое запуска воркфлоу сохранены.
- **Репозиторий:** вставка записи становится транзакционной — строка `services`
  + строки `service_owners` + `owners_version = 1` в одном `INSERT`-сценарии.
- **Воркфлоу провизии (`CreateServiceWorkflow`, вариант B):** после создания
  соответствующего ресурса устанавливаются роли владельца — GitLab members (после
  репозитория), Vault policies (после AppRole), IDM owner roles (после активации).
  Реюз activities `changeowners` (`GitLabSyncMembers`/`VaultSyncOwners`/
  `IDMSyncOwnerRoles`) с `add = owners`, `remove = []`. Компенсации членства/политик
  поглощаются существующими компенсациями удаления ресурса (delete repo / teardown
  Vault); IDM-роли — ПОСЛЕ точки активации, best-effort с алертом (как в
  `changeowners`).
- **Gateway:** проброс `owners`; синхронная валидация (пусто/пустые строки/дубли)
  → 400 через существующий маппинг, без сырых деталей (ADR-0009).
- **Портал (форма создания):** обязательное поле владельцев (reuse `parseOwners`),
  zod из кодогена как источник валидации, ошибка пустых владельцев — inline/тостом
  (`lib/errors`); по успеху — прежний переход на единый прогресс (ADR-0022).
  Обновляются подписи `OPERATION_STEPS.create` (добавлены шаги назначения
  владельцев в GitLab/Vault/IDM).
- **Тесты:** unit (usecase/провизия/валидация), gateway, web (CreateServicePage —
  обязательность владельцев, happy-path), temporal-тесты воркфлоу + компенсаций
  (вариант B), e2e (создание под owners, поллинг без `sleep`).
- **Авто-владелец:** создатель НЕ добавляется неявно (требуется явное указание);
  форма МОЖЕТ предлагать текущего пользователя как дефолт (UX), но не делает его
  обязательным.

## Capabilities

### New Capabilities
<!-- Новых capability не вводим — расширяем существующие. -->

### Modified Capabilities
- `service-contracts`: `CreateServiceRequest` (proto+openapi) получает
  обязательное поле `owners` (`minItems 1`, непустые элементы).
- `service-provisioning`: создание требует владельцев и устанавливает их атомарно
  в каталоге; воркфлоу создания устанавливает реальные роли владельца во внешних
  системах с Saga-компенсацией (вариант B).
- `service-ownership`: владелец сервиса устанавливается уже при создании (не
  только последующей сменой владельцев); инвариант «сервис без владельца
  невозможен».
- `perimeter-rest`: `POST /projects/{project}/services` требует `owners`;
  синхронная валидация пустых владельцев → 400.
- `portal-ui`: форма создания сервиса получает обязательное поле владельцев и
  обновлённые подписи шагов единого прогресса создания.
- `e2e-portal-testing`: сценарии создания сервиса передают владельцев.

## Impact

- **Контракт:** `proto/projects/v1/projects.proto`, `openapi/openapi.yaml`
  (+ перегенерация Go `pkg/api/projects/v1` и web-кодогена `web/src/api/*`,
  `web/public/openapi.yaml`). BREAKING для всех вызывающих создание.
- **Сервисы:** `services/projects` (usecase, репозиторий, provisioning,
  wfstarter), `services/devinfra-worker` (регистрация activities владельцев в
  воркфлоу создания при необходимости), `services/gateway` (handlers).
- **Границы:** gRPC `ProjectsService.CreateService` (новое поле в запросе),
  Temporal `CreateServiceWorkflow` (новые шаги + компенсации).
- **Портал:** `web/src/pages/CreateServicePage.tsx`,
  `web/src/lib/workflow-steps.ts` (подписи шагов), переиспользование
  `parseOwners` из `web/src/components/OwnersCard.tsx`.
- **Тесты-вызыватели (BREAKING):** `tests/e2e/stories_test.go`,
  `tests/e2e/errors_test.go`, `tests/e2e/harness_test.go` (`mustCreateActive`),
  `tests/e2e/{gitlab,vault,harbor}_integration_test.go`.
- **План отката/компенсаций:** при фатальном сбое до активации — Saga-откат
  (удаление GitLab/Harbor/Vault ресурсов поглощает и назначенное членство/политики),
  запись → FAILED. После активации сбой IDM-ролей — алерт оператору без молчаливого
  отката (каталог — источник правды, ADR-0005/0008).
