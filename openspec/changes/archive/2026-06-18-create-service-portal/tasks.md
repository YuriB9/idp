## 1. Подготовка и сверка

- [x] 1.1 Сверить scope с docs/IDP_MVP_plan.md (Этап 1 портал, Этап 3 «Создание сервиса») и ADR-0002/0003/0004/0009; зафиксировать расхождения, если есть
- [x] 1.2 Создать ветку `change/create-service-portal` от свежего master (прямые коммиты в master запрещены)
- [x] 1.3 Внести канонический ADR-0009 (`docs/adr/0009-perimeter-rest-resource-shape.md`) в индекс ADR проекта, если требуется

## 2. OpenAPI периметра (источник правды)

- [x] 2.1 Расширить `openapi/openapi.yaml`: `POST /projects/{project}/services` (тело `{name}`), ответ 201 `{id, status}`
- [x] 2.2 Добавить `GET /projects/{project}/services/{name}` (ответ `{project, name, status}`, 404 при отсутствии)
- [x] 2.3 Добавить `GET /projects/{project}/services` с keyset-параметрами `page_size`/`page_token` и ответом `{services[], next_page_token}`
- [x] 2.4 Удалить каркасный `GET /services` (**BREAKING**), добавить схемы запросов/ответов/ошибки, согласованные с `projectsv1`
- [x] 2.5 Прогнать `npm run gen` в `./web`; убедиться, что `gen:check` зелёный (пустой `git diff src/api`)

## 3. Gateway (services/gateway): REST↔gRPC

- [x] 3.1 Реализовать функцию маппинга gRPC-кодов в HTTP (`NotFound`→404, `FailedPrecondition`/`AlreadyExists`→409, `InvalidArgument`→400, прочее→500) и единый JSON-ответ об ошибке без `err.Error()`
- [x] 3.2 Подключить клиент `projectsv1` (убрать `_ =`-заглушку) и реализовать handler создания (`POST`), вызывающий `CreateService`, с валидацией тела
- [x] 3.3 Реализовать handler чтения (`GET .../{name}`) поверх `GetService`
- [x] 3.4 Реализовать handler листинга (`GET`) поверх `ListServices` с пробросом keyset-курсора; заменить захардкоженный `[]`
- [x] 3.5 Зарегистрировать маршруты под существующими middlewares (`RequestID`/`Recoverer`/`RateLimit`/`Auth`); оставить точку-заглушку RBAC IDM `CheckAccess`; `RoutePattern()` в метках (если включены)
- [x] 3.6 Table-driven тесты с `t.Parallel()` на стабе projects-клиента (без сети): маппинг кодов, форма JSON, неразглашение внутренних ошибок, happy-path Create/Get/List

## 4. Frontend SPA (./web): bootstrap

- [x] 4.1 Добавить dev-зависимости `vite`, `@vitejs/plugin-react`, `react-router-dom`; npm-скрипты `dev`/`build`/`preview`
- [x] 4.2 Создать `vite.config.ts` с плагином React и dev-прокси `/api` → `http://localhost:8081`; `index.html` и `src/main.tsx` с `QueryClientProvider` и роутером
- [x] 4.3 Создать корневой `App` и обёртку API-клиента, валидирующую ВСЕ ответы через сгенерированную zod-схему (`.parse`)

## 5. Frontend: экраны сценария

- [x] 5.1 Экран списка сервисов проекта (`GET /projects/{project}/services`) с zod-валидацией ответа
- [x] 5.2 Форма создания (react-hook-form + zodResolver из кодогена) → `POST`; обработка клиентской валидации и серверного 409 (конфликт имени)
- [x] 5.3 Экран прогресса: `useQuery` с `refetchInterval`, поллинг `GetService` до `active`/`failed`, останов опроса на терминале и показ исхода
- [x] 5.4 Тесты web: zod-валидация ответов (happy-path и дрейф контракта), happy-path формы (по возможности)

## 6. Локальный стенд и документация

- [x] 6.1 Добавить портал в `docker-compose` (сервис `web`) ИЛИ задокументировать `npm run dev` на `:3000` с прокси на gateway `:8081`; согласовать локальный `AUTH_DISABLED`/oauth2-proxy и CORS
- [x] 6.2 Обновить README: как поднять стенд, где открыть портал, как создать сервис, кросс-проверка через Temporal UI (`:8080`) и логи worker'а
- [x] 6.3 Ручная сквозная проверка: портал → gateway → projects → Temporal → DevInfra worker (моки), наблюдаемый переход `creating`→`active`

## 7. Завершение

- [x] 7.1 Убедиться, что комментарии в коде (Go/TS/TSX/конфиги) только на русском
- [x] 7.2 Прогнать локально lint, тесты модулей, `gen:check`; открыть PR; добиться зелёного CI (gen:check, golangci-lint, govulncheck, integration)
- [x] 7.3 После merge ветки в master — запустить `/opsx:archive`
