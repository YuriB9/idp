## Context

Бэкенд сценария «Создание сервиса» уже в master: gRPC `CreateService` фиксирует
запись `status=CREATING` и запускает Temporal-workflow с детерминированным
WorkflowID; DevInfra worker исполняет провизию (GitLab→Harbor→Vault→инъекция
секретов→ACTIVE) с Saga-компенсациями (ADR-0005) и финальными guarded-CAS
переходами `CREATING→ACTIVE/FAILED` (ADR-0004). Периметр и портал — каркас:
gateway отдаёт `[]`, OpenAPI без доменных операций, `./web` — только кодоген.

Этот change добавляет тонкий вертикальный срез поверх готового бэкенда: доменные
REST-ручки периметра (ADR-0002 — OpenAPI на границе, gRPC внутри) и реальное SPA.
Никакой доменной логики и провизии здесь не появляется — gateway проксирует к
`projects`, портал визуализирует статус поллингом.

## Goals / Non-Goals

**Goals:**
- Сквозной путь, видимый в браузере: форма → создание → `CREATING` →
  `ACTIVE`/`FAILED`.
- OpenAPI как единственный источник правды доменных операций периметра; кодоген
  TS/zod без diff (`gen:check`).
- Детерминированный маппинг gRPC-кодов в HTTP и неразглашение внутренних ошибок.
- Поднятие SPA (Vite/react-router/TanStack Query) и локальный стенд с порталом.

**Non-Goals:**
- Изменение `.proto`, доменной логики каталога/workflow (уже в master).
- Реальный RBAC IDM `CheckAccess`, реальный OIDC-логин (заглушка / oauth2-proxy).
- Эндпоинты владельцев/переноса/decommission, реальные GitLab/Vault/Harbor.
- WebSocket/стриминг прогресса — для MVP достаточно polling.

## Поток создания (портал ↔ gateway ↔ projects ↔ Temporal/worker)

```
Браузер(SPA)        gateway(REST)        projects(gRPC+API)     Temporal        DevInfra worker(моки)
   |                     |                     |                   |                   |
   |  POST /api/projects/{p}/services {name}   |                   |                   |
   |-------------------->|                     |                   |                   |
   |                     | CreateService(p,name) (gRPC)            |                   |
   |                     |-------------------->|                   |                   |
   |                     |                     | INSERT status=CREATING (guarded)      |
   |                     |                     | StartWorkflow(детерм. WorkflowID)     |
   |                     |                     |------------------>|                   |
   |  201 {id, status=creating}                |                   |  poll task-queue "devinfra"
   |<--------------------|<--------------------|                   |------------------>|
   |                     |                     |                   | GitLab→Harbor→Vault→inject (activities,
   |                     |                     |                   |   RetryPolicy/таймауты/heartbeat; Saga)
   |  --- поллинг ---     |                     |                   |                   |
   |  GET /api/projects/{p}/services/{name}    |                   |                   |
   |-------------------->| GetService(p,name)  |                   |                   |
   |                     |-------------------->| SELECT status     |                   |
   |  200 {status: creating|active|failed}     | (CAS CREATING→ACTIVE/FAILED по завершении workflow)
   |<--------------------|<--------------------|                   |<------------------|
   |  (повтор до active/failed, затем стоп)     |                   |                   |
```

Идемпотентность и ретраи — на стороне уже смерженного бэкенда: `CreateService`
использует детерминированный WorkflowID (повторный запуск не плодит провизию),
activities имеют RetryPolicy/таймауты/heartbeat, при окончательной недоступности
Vault — Saga-откат в `FAILED` + alert. Периметр/портал ретраи провизии не
выполняют; повторный `POST` опирается на идемпотентность бэкенда, портал лишь
опрашивает статус.

## Decisions

### 1. Форма REST-ресурсов периметра — проектно-скоупленные пути
`POST /projects/{project}/services` + `GET /projects/{project}/services/{name}`
+ `GET /projects/{project}/services`. Каркасный `GET /services` удаляется
(**BREAKING**). Альтернатива — плоский `/services?project=` — отвергнута: вложенный
ресурс точнее отражает владение проектом и единообразен для будущих операций
(владельцы/перенос/decommission). Фиксируется отдельным ADR (см. adr.md).

### 2. Маппинг gRPC→HTTP в gateway, без раскрытия ошибок
Единая функция маппинга: `NotFound`→404, `FailedPrecondition`/`AlreadyExists`→
409, `InvalidArgument`→400, прочее→500. Наружу — стабильное JSON-тело ошибки по
форме OpenAPI; `err.Error()`/детали gRPC только в лог (slog ключ `err`). Метрики
(если включены) маркируются `RoutePattern()`. Альтернатива — пробрасывать
gRPC-сообщение — отвергнута по безопасности (fail-closed, неразглашение).

### 3. OpenAPI — источник правды, согласованный с gRPC
Схемы тел/ответов выводятся из `projectsv1` (`CreateServiceResponse{id,status}`,
`Service{project,name,status}`, keyset `page_size`/`page_token`/
`next_page_token`). `ServiceStatus` периметра остаётся `creating/active/
decommissioned/failed` (lowercase) и маппится из enum gRPC. После правок —
`npm run gen`, `gen:check` зелёный.

### 4. SPA: Vite + react-router + TanStack Query; zod как источник валидации
Формы — react-hook-form с zodResolver поверх сгенерированной zod-схемы. ВСЕ
ответы API проходят `.parse` (дрейф контракта падает явно). Прогресс — через
`useQuery` с `refetchInterval`, который отключается (`false`) при терминальном
статусе `active`/`failed`. Альтернатива poll — WebSocket — вне scope MVP.

### 5. Локальная аутентификация и CORS
Локально gateway с `AUTH_DISABLED=true`; портал ходит на периметр через
dev-прокси Vite (`/api` → `http://localhost:8081`), что снимает CORS на этапе
браузера (запросы идут same-origin на dev-сервер). Для compose-варианта портал
за oauth2-proxy/прокси, same-origin сохраняется. `AUTH_DISABLED` — только
локалка; на периметре auth остаётся fail-closed.

### 6. Стратегия поллинга статуса
Интервал ~1–2 c, останов при `active`/`failed`; на ошибках сети — стандартный
ретрай TanStack Query с backoff; видимый индикатор «создаётся». Терминальный
статус кэшируется, лишних запросов после исхода нет.

## Risks / Trade-offs

- **[BREAKING `GET /services`]** ломает каркасный контракт → потребителей у
  каркаса нет (был заглушкой `[]`); помечено BREAKING, кодоген перегенерирован.
- **[Дрейф OpenAPI ↔ gRPC]** формы могут разойтись с `projectsv1` → согласование
  на ревью + table-driven тесты маппинга gateway (стаб projects-клиента).
- **[Поллинг создаёт нагрузку]** частый `GetService` → умеренный интервал и
  останов на терминале; для MVP приемлемо.
- **[Утечка внутренних ошибок]** случайный проброс `err.Error()` → единая
  функция ответа об ошибке + тест на неразглашение.
- **[CORS/AUTH рассинхрон локально]** → выбран same-origin dev-прокси, документ
  в README; `AUTH_DISABLED` строго локально.

## Migration Plan

1. Ветка `change/create-service-portal` от master (прямые коммиты в master
   запрещены).
2. Расширить `openapi/openapi.yaml`; `npm run gen`; `gen:check` зелёный.
3. Реализовать REST-ручки gateway + table-driven тесты (стаб projects, без сети).
4. Поднять SPA (`./web`): vite-конфиг, прокси, страницы, zod-валидация, тесты.
5. Добавить портал в docker-compose / задокументировать `npm run dev`.
6. README + ручная сквозная проверка (Temporal UI, логи worker).
7. PR с зелёным CI (gen:check, lint, govulncheck, integration); merge → затем
   `/opsx:archive`.

**Rollback**: change аддитивен на уровне рантайма (новый код периметра/портала);
откат — revert PR. Состояние бэкенда/БД не меняется.

## Open Questions

- Нужен ли отдельный compose-сервис `web` или достаточно документированного
  `npm run dev` для MVP-демо? (Решение зафиксировать в tasks; по умолчанию —
  документированный `npm run dev`, compose-сервис опционально.)
