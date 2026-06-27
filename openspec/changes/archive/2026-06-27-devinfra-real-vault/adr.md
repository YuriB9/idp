# ADR (изменение: devinfra-real-vault)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0020 — Модель аутентификации worker→Vault и раскладка secret-engine**
  (`docs/adr/0020-vault-auth-and-secret-engine-layout.md`): аутентификация `X-Vault-Token` со
  статическим dev root-токеном (фикстура стенда, не протекает на GitLab/Harbor); KV v2 на `secret/`
  (dev-дефолт) + сид только `approle enable`; немедленный отзыв через перечисление и destroy
  secret-id-accessors (нет «destroy all»); маппинг владелец→Vault-идентичность entity по фикстуре с
  safe-skip; идемпотентность по кодам Vault; выбор реального клиента по наличию `VAULT_TOKEN`.

## Реализуемые существующие ADR

- **ADR-0001 — Temporal как оркестратор**: Vault-activity исполняются в воркфлоу провизии/смены
  владельцев/decommission/transfer на task-queue worker-а.
- **ADR-0005 — Saga-откат создания**: компенсация `TeardownAppRole` (откат создания AppRole) наблюдаема
  против реального Vault.
- **ADR-0011 — Owners + Vault SyncOwners**: смена владельцев отражается в identity/policies реального
  Vault; компенсация `RestoreOwners`.
- **ADR-0012 — Decommission, точка невозврата**: `RevokeSecretID` необратим — реализован как
  перечисление + destroy secret-id-accessors с наблюдаемым результатом.
- **ADR-0013 — Transfer, частичная необратимость**: `MigratePaths` переносит секреты source→target по
  KV v2.
- **ADR-0018 — E2E-харнесс и стенд-override**: интеграционный набор переиспользует харнесс `tests/e2e`.
- **ADR-0019 — Реальный GitLab (образец)**: паттерны per-client заголовка, идемпотентности по кодам,
  выбора клиента по токену, отдельного профиля и сида перенесены на Vault-плечо.
