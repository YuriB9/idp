# ADR (изменение: create-service-workflow)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0008 — Разделение определения и исполнения workflow «Создание сервиса»** (`docs/adr/0008-workflow-definition-execution-split.md`): определение workflow (имя, типы input/output, конструктор детерминированного `WorkflowID`, имена activities) выносится в общий пакет, импортируемый и `services/projects` (запуск), и `services/devinfra-worker` (регистрация/реализация); финальный guarded-CAS-переход статуса выполняется activity на стороне worker'а.

## Реализуемые существующие ADR

- **ADR-0001 — Temporal как оркестратор**: durable workflow «Создание сервиса» на task-queue `devinfra`; внешние вызовы — activities с `RetryPolicy`/таймаутами/heartbeat; non-retryable `ApplicationError` → ветка компенсации.
- **ADR-0004 — Переходы статусов guarded-CAS**: финальные переходы `CREATING→ACTIVE` и `CREATING→FAILED` через `repository.TransitionStatus` с ожидаемым исходным статусом (`RowsAffected==0 → ErrConflict`).
- **ADR-0005 — Полный Saga-откат при недоступности Vault**: компенсации в обратном порядке (удалить Harbor-директорию и GitLab-репозиторий) при окончательной недоступности Vault; при сбое самой компенсации — `FAILED` + alert оператору.
