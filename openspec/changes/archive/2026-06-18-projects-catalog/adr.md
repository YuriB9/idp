# ADR (изменение: projects-catalog)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0007 — Инструмент миграций БД — goose** (`docs/adr/0007-migration-tool-goose.md`): миграции каталога — goose (`.sql`, пин в `./tools`, `GOWORK=off`, Makefile-таргет), вместо golang-migrate.

## Реализуемые существующие ADR

- **ADR-0004 — Переходы статусов через guarded-CAS**: repository выполняет переходы статуса сервиса как `UPDATE ... WHERE id=$id AND status=$expected` с `RowsAffected==0 → errs.ErrConflict`; многошаговые записи — через `withTx`, публикация — после commit.
- **ADR-0002 — gRPC как внутренний транспорт**: доменная реализация `ProjectsService.GetService` и добавление `ListServices` в `.proto`-контракт (BREAKING) с регенерацией стабов.
- **ADR-0006 — Раскладка монорепо на go.work**: новый инструмент миграций изолируется в `./tools` (`GOWORK=off`); код сервиса остаётся в модуле `services/projects` с `replace ../../pkg`.
