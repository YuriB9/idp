# ADR (изменение: devinfra-real-harbor)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0021 — Harbor auth-модель и раскладка проектов/robot-аккаунтов**
  (`docs/adr/0021-harbor-auth-and-project-robot-layout.md`): аутентификация `Authorization: Basic`
  admin (фикстура `HARBOR_ADMIN_PASSWORD`, не протекает на GitLab/Vault); способ запуска Harbor в
  Docker — официальный installer-bundle с пиннутой версией и закоммиченным конфигом (не урезанный
  субсет); тело robot v2.0 (`level`/`permissions[]`/`duration`) с забором ФАКТИЧЕСКИХ `name`/`secret`
  из ответа; удаление/отзыв robot по ЧИСЛОВОМУ id (резолвинг по имени через список); read-only одного
  проекта реализован ОТЗЫВОМ robot (в Harbor нет project-level `read_only`), наблюдаемо и необратимо;
  UpdateMetadata через допустимое наблюдаемое поле метаданных; идемпотентность по кодам Harbor; выбор
  реального клиента по наличию `HARBOR_USERNAME`/`HARBOR_PASSWORD`.

## Реализуемые существующие ADR

- **ADR-0001 — Temporal как оркестратор**: Harbor-activity исполняются в воркфлоу провизии/
  decommission/transfer на task-queue worker-а.
- **ADR-0005 — Saga-откат создания**: компенсация `DeleteProject` (откат создания проекта+robot)
  наблюдаема против реального Harbor.
- **ADR-0008 — Split воркфлоу определения/исполнения**: Harbor-activity встроены в существующий split
  без изменений контракта.
- **ADR-0012 — Decommission, точка невозврата**: `SetReadOnly` необратим — реализован как ОТЗЫВ
  robot-аккаунта проекта (в Harbor нет project-level read-only) с наблюдаемым результатом; компенсация
  `SetWritable` воссоздаёт robot.
- **ADR-0013 — Transfer, частичная необратимость**: `UpdateMetadata` обновляет наблюдаемое допустимое
  поле метаданных проекта под целевой проект.
- **ADR-0018 — E2E-харнесс и стенд-override**: интеграционный набор переиспользует харнесс `tests/e2e`.
- **ADR-0019 — Реальный GitLab (образец)**: паттерны per-client заголовка, резолвинга id по имени,
  идемпотентности по кодам, выбора клиента по креденшелам, отдельного профиля и сида перенесены на
  Harbor-плечо.
- **ADR-0020 — Реальный Vault (образец)**: паттерны отдельного `doer`-заголовка, флага `real`,
  немедленного отзыва (зеркало для отзыва robot), отдельного интеграционного набора и Makefile-целей
  перенесены на Harbor-плечо.
