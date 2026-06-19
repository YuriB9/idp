# ADR (изменение: change-service-owners)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0011 — Семантика смены владельцев сервиса и синхронизация ролей IDM**
  (`docs/adr/0011-service-owners-contract-and-role-sync.md`): декларативный
  контракт `SetServiceOwners` (полный набор, идемпотентность), guarded-CAS по
  `owners_version`, управляющие RPC IDM `AssignRole`/`RevokeRole` с инвалидацией
  кэша, действие `change_owners`, и Saga-workflow «Изменение владельцев» с точкой
  невозврата на commit владельцев в каталог.

## Реализуемые существующие ADR

- **ADR-0001 — Temporal как оркестратор**: отдельный workflow «Изменение
  владельцев» исполняется DevInfra worker'ом.
- **ADR-0004 — Переходы/изменения через guarded-CAS**: замена набора владельцев
  guarded-CAS по `owners_version` (RowsAffected==0 → конфликт/409).
- **ADR-0005 — Политика отката Saga**: идемпотентные компенсации до точки
  невозврата; после — алерт оператору, без молчаливого отката.
- **ADR-0008 — Разделение определения и исполнения workflow**: публичный пакет
  `services/projects/changeowners` (контракт) + реализация activities в worker.
- **ADR-0003 / ADR-0010 — Модель auth/RBAC и кэш IDM**: действие `change_owners`,
  две точки `CheckAccess` (fail-closed), инвалидация кэша по затронутым субъектам.
