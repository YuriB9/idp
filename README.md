# IDP — Internal Developer Platform (MVP)

Платформа самообслуживания для команды DevInfra. MVP реализует сквозной сценарий
**«Создание сервиса»**: портал → API-шлюз (REST) → сервис проектов (gRPC) →
Temporal-workflow провизии → DevInfra worker (GitLab/Vault/Harbor).

Внутри платформы — gRPC/protobuf, на периметре (портал ↔ шлюз) — OpenAPI/JSON
(ADR-0002, ADR-0009). Архитектурные решения — в [docs/adr](docs/adr/).

## Локальный стенд

Полный стенд (включая портал) поднимается одной командой из корня репозитория:

```bash
docker compose -f deploy/compose/docker-compose.yml up --build
```

Поднимаются: Keycloak, Oauth2-Proxy, Postgres ×2, DragonflyDB, Temporal + UI,
сервисы платформы (gateway/idm/projects/devinfra-worker), **портал (SPA)** и моки
GitLab/Vault/Harbor.

| Компонент | Адрес | Назначение |
|-----------|-------|------------|
| **Портал** | http://localhost:3000 | UI создания и наблюдения за сервисами |
| API-шлюз (REST) | http://localhost:8081/api | Периметр поверх gRPC `projects` |
| Temporal UI | http://localhost:8080 | Кросс-проверка workflow провизии |
| Keycloak | http://localhost:8088 | OIDC (для проверки oauth2-proxy) |

Локально периметр работает с `AUTH_DISABLED=true` (единственный разрешённый
способ отключения проверки JWT; в проде — реальный JWKS, fail-closed, ADR-0003).
Портал ходит на шлюз через same-origin прокси `/api`, поэтому CORS в браузере не
возникает.

### Альтернатива: портал в dev-режиме без контейнера

Если стенд бэкенда уже поднят (как минимум `gateway` на `:8081`), портал можно
запустить локально с горячей перезагрузкой:

```bash
cd web
npm install
npm run dev   # http://localhost:3000, прокси /api → http://localhost:8081
```

Цель прокси переопределяется переменной `GATEWAY_URL`.

## Как создать сервис и увидеть результат

1. Откройте портал: http://localhost:3000 (проект по умолчанию — `demo`).
2. Нажмите **«+ Создать сервис»**, введите имя и отправьте форму.
3. Портал перейдёт на экран прогресса и начнёт **поллить статус** сервиса:
   `creating` → `active` (успех) или `failed` (откат Saga при недоступности
   Vault, ADR-0005). Опрос останавливается на терминальном статусе.
4. Кросс-проверка: ход workflow «Создание сервиса» виден в **Temporal UI**
   (http://localhost:8080), а шаги провизии — в логах `devinfra-worker`:

   ```bash
   docker compose -f deploy/compose/docker-compose.yml logs -f devinfra-worker
   ```

## Как вывести сервис из эксплуатации (decommission / soft delete)

Decommission — это **soft delete**: запись каталога и владельцы **сохраняются**
(возможен будущий restore), статус переводится `active → decommissioned`, а во
внешних системах **необратимо отзывается доступ** (GitLab archive, Harbor →
read-only + отзыв Robot, Vault → отзыв активных SecretID/токенов). Это НЕ
физическое удаление (не purge). См. ADR-0012.

1. На странице сервиса (экран прогресса) в карточке **«Вывод из эксплуатации»**
   отметьте, что **нагрузка снята из K8s**, и введите имя сервиса для
   подтверждения (необратимость доступа!).
2. Нажмите **«Вывести из эксплуатации»** → запускается Temporal-workflow «Вывод
   из эксплуатации» (`decommission:<project>:<name>`): предусловие снятой нагрузки
   → отзыв доступа GitLab/Harbor/Vault → guarded-CAS `active → decommissioned`.
   Статус в портале станет `decommissioned`.
3. Кросс-проверка хода — в **Temporal UI** и в логах `devinfra-worker`.

**Проверка снятой нагрузки K8s (ADR-0012).** В MVP K8s-worker отсутствует, поэтому
снятие нагрузки подтверждается **явным предусловием** (`load_drained` в запросе),
проверяемым предварительным шагом workflow за интерфейсом `LoadChecker` — без
имитации запроса к несуществующему кластеру; граница оставлена под будущий
K8s-worker.

**Доступ и роли.** Доступ к сервису прекращается немедленно на уровне внешних
систем (сервис-скоупно). Per-project роль владельцев при выводе одного сервиса
**не отзывается** (иначе пострадал бы доступ к другим активным сервисам проекта),
см. ADR-0012.

**Как проверить отказ/предусловие/разрешение** (при включённом RBAC):

- **Разрешение:** субъект `demo-user` имеет право `decommission@project:demo`
  (seed `services/idm/migrations/0004_seed_decommission_demo.sql`) — операция
  проходит.
- **Отказ RBAC:** без права (или при недоступном IDM) периметр и `projects`
  отвечают `403`/`PermissionDenied` (fail-closed).
- **Предусловие не выполнено:** без отметки снятой нагрузки (`load_drained=false`)
  или для сервиса в статусе `creating`/`failed` — `422` (`FailedPrecondition`),
  без побочных эффектов.
- **Конкурентный конфликт:** при гонке статусов — `409` (`Aborted`).

## Контракты и кодоген

- Периметр: [openapi/openapi.yaml](openapi/openapi.yaml) — источник правды для
  TS-клиента и zod-схем портала. После правок:

  ```bash
  cd web && npm run gen        # перегенерация src/api (schema.ts + client.ts)
  npm run gen:check            # проверяет отсутствие расхождений (как в CI)
  ```

- Внутренние вызовы: `.proto` в [proto/](proto/) (кодоген Go-стабов из `tools`).

## Тесты

```bash
go test ./...        # из каталога нужного модуля (go.work)
cd web && npm test   # vitest: zod-валидация ответов и happy-path формы
```
