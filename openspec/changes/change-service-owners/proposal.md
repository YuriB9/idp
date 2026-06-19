## Why

После «Создания сервиса» и минимального RBAC третий сквозной сценарий MVP —
«Изменение владельцев сервиса» (docs/IDP_MVP_plan.md, Этап 3, строки ~153–160).
Сегодня у записи каталога нет владельцев вообще: контракт `proto/projects/v1`,
схема `services` и портал не знают про owners, а в IDM нет программного пути
выдачи/отзыва ролей. Без этого сценария нельзя передать сервис команде и
синхронно открыть/закрыть доступ в GitLab/Vault и права в IDM. Сценарий
переиспользует уже готовые моки GitLab/Vault и инфраструктуру Temporal/Saga
(ADR-0001/0005/0008), guarded-CAS каталога (ADR-0004) и fail-closed RBAC
(ADR-0003/0010), поэтому логично делать его до более рискованных «Удаления» и
«Переноса».

## What Changes

- **BREAKING (контракт projects):** в `proto/projects/v1` добавляется поле
  `repeated string owners` в `Service`/ответы чтения и новый RPC изменения
  владельцев `SetServiceOwners` (декларативный: клиент шлёт желаемый полный
  набор владельцев, сервер вычисляет diff add/remove — идемпотентно). Требует
  `buf generate` (`*.pb.go`) и регенерации TS-клиента портала; `gen:check`
  должен быть зелёным.
- **BREAKING (контракт idm):** в `proto/idm/v1` добавляется управляющий RPC
  выдачи/отзыва роли субъекту (`AssignRole`/`RevokeRole`, идемпотентные), чтобы
  доменный поток смены владельцев мог программно синхронизировать роли и
  инвалидировать кэш решений по затронутым субъектам.
- **Каталог:** модель владельцев в Postgres (новая таблица `service_owners` +
  колонка версии `owners_version` на `services`), обратимая миграция goose;
  repository + usecase обновления владельцев с guarded-CAS на конкурентные
  изменения; отражение `owners` в `GetService`/`ListServices`.
- **Workflow «Изменение владельцев»** (Temporal, отдельный от провижна): новый
  пакет `services/projects/changeowners` + activities в DevInfra worker
  синхронизации участников GitLab и политик Vault (моки), записи owners в
  каталог, синхронизации ролей в IDM и инвалидации кэша IDM по затронутым
  субъектам. Saga с идемпотентными компенсациями до точки невозврата (commit
  owners в каталоге) и алертом оператору после неё (ADR-0005/0008).
- **Авторизация:** новая RBAC-операция `change_owners` на ресурсе
  `project:<project>`; gateway и projects вызывают IDM `CheckAccess` перед
  операцией (defense-in-depth, fail-closed).
- **Периметр (REST, ADR-0009):** `PUT /projects/{project}/services/{name}/owners`
  (идемпотентная замена набора владельцев, optimistic concurrency по версии),
  отражение `owners` в ответах `GET`/`LIST`; маппинг gRPC→HTTP кодов.
- **Портал:** отображение владельцев и форма их изменения (react-hook-form +
  zod, TanStack Query), обработка 403 и конфликтов 409.
- **Локалка:** расширение seed IDM (роль владельца для `project:demo` + право
  `change_owners@project:demo` субъекту `demo-user`) и демо-владельцев, чтобы
  сквозной сценарий проходил при включённом RBAC.
- **Документация:** README projects/idm и инструкция — модель владельцев, как
  сменить владельца, влияние на роли/доступ, как проверить отказ/разрешение.
- **ADR-0011:** семантика контракта смены владельцев (декларативный набор vs
  дельта, идемпотентность) и стратегия синхронизации ролей IDM + инвалидации
  кэша.

## Capabilities

### New Capabilities
- `service-ownership`: модель владельцев сервиса в каталоге (схема, repository
  guarded-CAS, usecase, gRPC `SetServiceOwners` + owners в чтении) и workflow
  «Изменение владельцев» (Temporal Saga: GitLab/Vault/каталог/IDM-синк +
  инвалидация кэша, компенсации, точка невозврата).

### Modified Capabilities
- `service-contracts`: расширение `proto/projects/v1` (owners + `SetServiceOwners`)
  и `proto/idm/v1` (управляющий RPC выдачи/отзыва роли); регенерация Go/TS.
- `access-control`: программный путь выдачи/отзыва ролей субъектам и инвалидация
  кэша решений по затронутым субъектам из доменного потока; новая операция
  `change_owners`.
- `perimeter-rest`: REST-ручка изменения владельцев и `owners` в ответах
  чтения; RBAC `change_owners`; маппинг конфликтов.
- `service-provisioning`: добавление activities синхронизации владельцев
  (GitLab members / Vault policies / каталог / IDM) в DevInfra worker.
- `portal-ui`: UI отображения и изменения владельцев.
- `local-environment`: расширение seed (роль владельца, демо-владельцы) для
  сквозного сценария при включённом RBAC.

## Impact

- **Контракты/кодоген:** `proto/projects/v1`, `proto/idm/v1`, `pkg/api/**`
  (`buf generate`), TS-клиент `web/src/api` (`gen:check`).
- **services/projects:** `repository` (модель owners, guarded-CAS, миграция),
  `usecase`, `grpcapi` (`SetServiceOwners`, обобщённый `authorize`), новый
  `changeowners` (workflow + starter).
- **services/devinfra-worker:** новые activities GitLab/Vault владельцев + их
  компенсации; интеграционные моки `integrations`.
- **services/idm:** управляющий RPC ролей, инвалидация кэша по субъектам,
  миграция/seed роли владельца.
- **services/gateway:** маршрут `PUT .../owners`, owners в ответах, RBAC
  `change_owners`.
- **web:** экраны/форма владельцев, zod-схемы, TanStack-мутации.
- **БД:** миграция `services/projects/migrations` (owners), seed IDM.
- **Откат/компенсации:** Saga workflow — компенсации идемпотентны до точки
  невозврата (commit owners), после — алерт оператору, без молчаливого отката
  (ADR-0005/0008).
- **Зависимости:** при новых общих зависимостях — `GOWORK=off go mod tidy` во
  всех затронутых модулях (tidy-check/govulncheck).
