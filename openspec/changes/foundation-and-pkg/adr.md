# ADR (изменение: foundation-and-pkg)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md`.

## Новые ADR

- **ADR-0006 — Раскладка монорепо на go.work** (`docs/adr/0006-go-work-monorepo-layout.md`): модуль на компонент (`pkg`, каждый сервис, `tests/e2e`) под `go.work` + изолированный `./tools` вне workspace (`GOWORK=off`) для пина инструментов.

## Реализуемые существующие ADR

- **ADR-0002 — gRPC/protobuf для внутренних вызовов**: заводятся каркасы `.proto` (gateway↔idm, gateway↔projects) + кодоген Go-стабов; OpenAPI периметра + кодоген TS-клиента.
- **ADR-0003 — Auth fail-closed (Oauth2-Proxy + Keycloak + per-service JWT)**: `pkg/auth` (строгий JWT/JWKS, пустой `JWKS_URL` → `os.Exit(1)`, `AUTH_DISABLED=true`, `subtle.ConstantTimeCompare`) и локалка с Keycloak + Oauth2-Proxy.
- **ADR-0004 — Guarded-CAS переходов статусов**: заложен в `pkg/db`/`pkg/errs` (пул, `ErrConflict`) как основа для guarded-CAS; сами переходы статусов — в доменных changes.

Решения ADR-0001 (Temporal) и ADR-0005 (политика отката Saga) здесь только закладываются контрактно (скелет worker/клиента), доменной реализации нет.
