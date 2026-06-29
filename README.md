# IDP — Internal Developer Platform (MVP)

Платформа самообслуживания для команды DevInfra. MVP реализует сквозной сценарий
**«Создание сервиса»**: портал → API-шлюз (REST) → сервис проектов (gRPC) →
Temporal-workflow провизии → DevInfra worker (GitLab/Vault/Harbor).

Внутри платформы — gRPC/protobuf, на периметре (портал ↔ шлюз) — OpenAPI/JSON
(ADR-0002, ADR-0009). Обзор архитектуры (компоненты, потоки, границы доверия) —
[docs/architecture.md](docs/architecture.md); решения — в [docs/adr](docs/adr/).

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
