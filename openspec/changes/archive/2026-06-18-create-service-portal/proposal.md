## Why

Бэкенд сквозного сценария «Создание сервиса» уже в master (доменный каталог,
gRPC `CreateService`/`GetService`/`ListServices`, Temporal-workflow провизии с
Saga), но запустить и наблюдать его можно только из тестов/Temporal UI. Периметр
и портал — пока каркас: `services/gateway` отдаёт захардкоженный `[]`, OpenAPI
без доменных операций, `./web` — только кодоген без приложения. Нужен видимый в
браузере вертикальный срез (форма → создание → статус `CREATING` →
`ACTIVE`/`FAILED`), чтобы закрыть Этап 1 (портал) и Этап 3 («Создание сервиса»)
docs/IDP_MVP_plan.md и подтвердить контур периметра по ADR-0002.

## What Changes

- **OpenAPI периметра** (`openapi/openapi.yaml`) — расширяется доменными
  операциями поверх существующего каркаса:
  - `POST /projects/{project}/services` (тело `{name}`) — запуск создания
    сервиса, ответ с идентификатором записи и стартовым статусом `creating`.
  - `GET /projects/{project}/services/{name}` — чтение статуса одного сервиса.
  - `GET /projects/{project}/services` — листинг сервисов проекта с
    keyset-пагинацией (`page_size`/`page_token` → `next_page_token`).
  - Формы согласованы с gRPC-контрактом `projectsv1`. OpenAPI остаётся
    единственным источником правды; после правок — `npm run gen` (schema.ts +
    zod client.ts), `gen:check` в CI зелёный.
  - **BREAKING**: каркасный `GET /services` заменяется на проектно-скоупленный
    ресурс `GET /projects/{project}/services` (меняется форма пути и ответа).
- **Gateway** (`services/gateway`) — реализуются REST-ручки периметра поверх уже
  подключённого клиента `projectsv1` (Create/Get/List): маппинг доменных запросов
  в gRPC и ответов в JSON по форме OpenAPI; чёткий маппинг gRPC-кодов в HTTP
  (`NotFound`→404, `FailedPrecondition`→409, `InvalidArgument`→400, прочее→500);
  внутренние ошибки наружу не раскрываются (никогда не `err.Error()`);
  используются существующие middlewares и `/readyz`; `RoutePattern()` в метках.
- **Frontend** (`./web`) — поднимается реальное SPA: Vite +
  `@vitejs/plugin-react` + react-router + TanStack Query (`index.html`,
  `main.tsx`, `App`). Экран списка сервисов проекта; форма создания
  (react-hook-form + zod-схема из кодогена как источник правды валидации); экран
  прогресса, который поллит `GetService` до `active`/`failed`. Все ответы API
  валидируются рантайм через zod `.parse` (дрейф контракта падает явно). Скрипты
  `dev`/`build`/`preview`, dev-прокси `/api` → gateway.
- **Локалка** (`docker-compose`) — портал добавляется в стенд (или документируется
  `npm run dev` на `:3000` с прокси на gateway `:8081`); согласуются локальная
  аутентификация (`AUTH_DISABLED` у gateway / oauth2-proxy) и CORS. Работает
  сквозной путь: портал → gateway(REST) → projects(gRPC) → Temporal → DevInfra
  worker (моки), статус в портале меняется `CREATING`→`ACTIVE`.
- **README/инструкция** — как поднять стенд, где открыть портал, как создать
  сервис и увидеть результат (кросс-проверка через Temporal UI `:8080`).

Соответствие: ADR-0002 (gRPC внутри / OpenAPI на периметре), ADR-0003 (auth),
ADR-0004 (статусы `creating/active/failed`); docs/IDP_MVP_plan.md — Этап 1
(портал) и Этап 3 («Создание сервиса»). Планируется новый ADR по форме REST-
ресурсов периметра (см. adr.md).

## Откат / компенсации

Change провизию ресурсов не выполняет — это тонкий периметр поверх уже
смерженного gRPC/Temporal. Создание остаётся идемпотентным на стороне
`projects` (детерминированный WorkflowID), а Saga-откат при недоступности Vault
(failed + alert) уже реализован в master и здесь не меняется. Портал лишь
отражает финальный статус (`active`/`failed`), полученный поллингом.

## Capabilities

### New Capabilities
- `perimeter-rest`: доменные REST-ручки API-шлюза (Create/Get/List сервисов)
  поверх gRPC `projectsv1`, маппинг gRPC-кодов в HTTP и неразглашение внутренних
  ошибок на периметре.
- `portal-ui`: SPA-портал (список сервисов, форма создания, экран прогресса с
  поллингом статуса), рантайм-валидация ответов через zod.

### Modified Capabilities
- `service-contracts`: требование «OpenAPI периметра и TS-клиент» расширяется
  доменными операциями создания/чтения/листинга сервисов (формы согласованы с
  gRPC); каркасный `GET /services` заменяется проектно-скоупленным ресурсом
  (**BREAKING**).
- `local-environment`: локальный стенд расширяется порталом и согласованием
  AUTH/CORS для сквозной визуальной проверки.

## Impact

- **Затрагиваемые сервисы/границы**: `services/gateway` (REST↔gRPC периметр,
  клиент `projectsv1`), `./web` (SPA), `openapi/openapi.yaml` (контракт
  периметра), `docker-compose`/локальный стенд, README. Граница RBAC IDM
  `CheckAccess` на периметре — заглушка, как в gRPC `CreateService`.
- **Не затрагивается**: `.proto` и доменная логика workflow/каталога (уже в
  master), реальные GitLab/Vault/Harbor (моки), эндпоинты владельцев/переноса/
  decommission, реальный OIDC-логин, WebSocket-стриминг прогресса (вне scope).
- **Зависимости** (`./web`): добавляются `vite`, `@vitejs/plugin-react`,
  `react-router-dom`; npm-скрипты `dev`/`build`/`preview`. Go-зависимости
  gateway не добавляются (используются существующие `pkg/*` и `projectsv1`).
