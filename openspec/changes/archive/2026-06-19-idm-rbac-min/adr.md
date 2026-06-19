# ADR (изменение: idm-rbac-min)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0010 — Модель RBAC сервиса IDM и стратегия кэширования решений**
  (`docs/adr/0010-idm-rbac-model-and-cache.md`): нормализованный минимальный
  RBAC в Postgres (роли/права/связи/привязки, deny-by-default) и кэш решений в
  DragonflyDB с TTL, singleflight против stampede и инвалидацией версионным
  префиксом; поведение fail-closed.

## Реализуемые существующие ADR

- **ADR-0003 — Модель аутентификации (fail-closed)**: воплощается RBAC-часть —
  IDM `CheckAccess` перестаёт быть заглушкой, gateway/projects вызывают его
  перед доменными операциями fail-closed.
- **ADR-0007 — Инструмент миграций БД (goose)**: миграции IDM реализуются goose
  из `./tools` (`GOWORK=off`, обратимые `Up`/`Down`) и одноразовым мигратором
  `migrate-idm` по образцу `migrate-projects`.
