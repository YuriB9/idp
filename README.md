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
