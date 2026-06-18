# ADR-0003: Модель аутентификации — Oauth2-Proxy + Keycloak + per-service JWT (fail-closed)

**Status:** Accepted
**Date:** 2026-06-18

## Context

В целевой архитектуре identity provider — Keycloak, перед API стоит Oauth2-Proxy (OIDC), портал — SPA без BFF. Нужно решить, кто валидирует токены и как ведём себя при отсутствии конфигурации auth. Урок прошлого проекта: пустой `JWKS_URL` → passthrough — это дыра; auth должен быть fail-closed.

## Decision

- Портал (SPA) шлёт https/json на **Oauth2-Proxy**, который аутентифицирует через **Keycloak** по OIDC и проксирует авторизованный трафик на API-шлюз. SPA не управляет токенами вручную.
- Сервисы за прокси **сами валидируют JWT** (defense-in-depth): `WithAudience/WithIssuer/WithValidMethods/WithExpirationRequired`, JWKS по https.
- **Fail-closed:** пустой `JWKS_URL` → `os.Exit(1)`. Отключение только явным `AUTH_DISABLED=true` (локалка). admin/god-key — `subtle.ConstantTimeCompare`.
- Авторизация прав (RBAC) — отдельный сервис **IDM** (gRPC `CheckAccess`), Postgres + DragonflyDB-кэш.

## Consequences

**Положительные:** снимается вопрос «токен в браузере без BFF» — за это отвечает Oauth2-Proxy; defense-in-depth за счёт валидации в сервисах; нет небезопасного passthrough.

**Отрицательные:** Keycloak и Oauth2-Proxy — обязательные компоненты даже в локалке; в MVP набор ролей IDM минимален (расширяется позже без смены интерфейса).
