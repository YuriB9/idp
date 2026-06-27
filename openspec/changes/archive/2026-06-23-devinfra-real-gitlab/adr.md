# ADR (изменение: devinfra-real-gitlab)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0019 — Модель аутентификации worker→GitLab и маппинг namespace/owner в GitLab**
  (`docs/adr/0019-gitlab-auth-and-namespace-owner-mapping.md`): worker→GitLab по
  `PRIVATE-TOKEN` (статический root PAT из `GITLAB_TOKEN`), namespace→`group_id` через
  предсозданные группы и lookup с кэшем, владелец→`user_id` через тест-пользователей и
  фикстуру-маппинг, идемпотентность по кодам/GET-then-act, выбор реализации по наличию токена.

## Реализуемые существующие ADR

- **ADR-0001 — Temporal как оркестратор**: GitLab-вызовы остаются activities воркфлоу; меняется
  только реализация клиента, не модель исполнения.
- **ADR-0005 — Политика отката создания (Saga)**: компенсация `DeleteRepo` теперь наблюдается
  против реального GitLab (после `failed` репозитория в группе нет).
- **ADR-0012 — Семантика decommission**: `Archive` репозитория проверяется через реальный
  GitLab (`archived=true`).
- **ADR-0013 — Семантика transfer и точка невозврата**: `TransferRepo` проверяется против
  реального GitLab (репо сменил namespace), частичная необратимость не откатывается.
- **ADR-0018 — Путь аутентификации E2E и детерминизм воркфлоу**: переиспользуются стенд-
  override-паттерн, харнесс `tests/e2e` и Makefile-цели по образцу `e2e-*`.
