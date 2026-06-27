# ADR-0021: Модель аутентификации worker→Harbor и раскладка проектов/robot-аккаунтов

**Status:** Accepted
**Date:** 2026-06-27
**Change:** devinfra-real-harbor

## Context

После `devinfra-real-gitlab` (ADR-0019) и `devinfra-real-vault` (ADR-0020) реальными сделаны GitLab- и
Vault-плечи DevInfra; Harbor оставался ПОСЛЕДНИМ WireMock-моком. HTTP-клиент Harbor (`harborHTTP` в
`services/devinfra-worker/internal/integrations/http.go`) писался под мок и расходился с реальным Harbor
v2.0 API. Расхождения подтверждены ЭМПИРИЧЕСКИ против поднятого Harbor **v2.14.4** (official online
installer; см. `openspec/changes/.../empirical-harbor-api.md`), а не по памяти:

- **Нет аутентификации.** `doer` Harbor — общий `sharedDoer` без заголовков. Реальный Harbor на запись
  без авторизации отвечает `401` (чтения каталога проектов анонимны). Нужен HTTP **Basic** admin.
- **Robot по имени, минимальное тело.** Текущий код шлёт `POST /robots {name,level}` и удаляет
  `DELETE /robots/robot$<proj>` по имени. Реально: минимальное тело → `400` («duration must be …»);
  удаление по имени → `422` («robot_id must be of type int64»). Тело v2.0 требует `level`/`permissions[]`/
  `duration`; удаление — по ЧИСЛОВОМУ id.
- **project-level robots не перечислимы.** `GET /robots` отдаёт `X-Total-Count: 0` для project-робота;
  пути `/projects/<id>/robots` нет. Значит id project-робота НЕЛЬЗЯ резолвить по имени для отзыва.
- **Нет project-level read_only.** `SetReadOnly` слал несуществующее поле `read_only` в метаданных —
  Harbor его молча игнорирует.
- **Произвольные метаданные игнорируются.** `UpdateMetadata` слал `metadata.owner_project` — не
  сохраняется (Harbor принимает только фиксированный набор полей `ProjectMetadata`).

Это изменение (граница MVP) подключает РЕАЛЬНЫЙ Harbor вместо мока ТОЛЬКО для Harbor-плеча (GitLab/Vault
— реальные или моки по профилю), не меняя контракт периметра, proto и сгенерированный код. Опирается на
ADR-0001 (Temporal/activities), ADR-0005 (Saga-откат создания — компенсация `DeleteProject`), ADR-0008
(split воркфлоу), ADR-0012 (decommission → `SetReadOnly`, необратимо), ADR-0013 (transfer →
`UpdateMetadata`), ADR-0018 (E2E-харнесс), ADR-0019/0020 (образцы: статический креденшел, идемпотентность
по кодам, per-client заголовок, флаг `real`, отдельный профиль/сид/набор).

## Decision

- **Аутентификация — HTTP Basic admin (фикстура).** Harbor стартует с `HARBOR_ADMIN_PASSWORD` (известный
  фиксированный admin-пароль — фикстура стенда, как `glpat-…`/dev-root-token). Каждый запрос worker→Harbor
  несёт `Authorization: Basic base64(user:pass)` из конфигурации (`integrations.Config.HarborUsername`/
  `HarborPassword`, env `HARBOR_USERNAME`/`HARBOR_PASSWORD`/`HARBOR_PASSWORD_FILE`). Пароль не логируется;
  на GitLab/Vault не распространяется (отдельный `doer` с заголовком). Системный robot-аккаунт самого
  worker отклонён: для одного сервис-актора на стенде admin проще и детерминированнее (тот же аргумент,
  что для PAT/dev-token в ADR-0019/0020). `gosec G101` на имени константы заголовка глушится `//nolint`
  (это имя HTTP-заголовка, не секрет).

- **Способ запуска — официальный installer-bundle, пиннутая версия, отдельный compose-проект.** Harbor —
  НЕ один образ, а связка (core+db+redis+registry+registryctl+jobservice+portal+nginx). Разворачивается
  из официального online-installer (пиннут `v2.14.4`) через `prepare` из закоммиченного `harbor.yml`
  (`deploy/compose/harbor/`, скрипты `up.sh`/`down.sh`). Урезанный API-only субсет отклонён: Harbor core
  жёстко зависит от db/redis/registry/секретов от `prepare`, ручной субсет хрупок и расходится с
  поддерживаемой конфигурацией. Два стендовых патча генерируемого стека: (1) docker-логи компонентов →
  `json-file` (штатный syslog-контейнер `harbor-log` падает на новых rsyslog «no active actions»);
  (2) конфиги делаются world-readable (компоненты бегут под разными uid 10000/999 — world-read снимает
  конфликт владельцев без chown под каждый uid; каталог данных prepare выставляет сам, его не трогаем).
  Harbor поднимается ОТДЕЛЬНЫМ compose-проектом (`harbor-real`); IDP-override (`docker-compose.harbor.yml`)
  лишь переключает worker на него через `host.docker.internal:8085`.

- **Robot-аккаунт — на уровне `system`, scope на проект, фактические name/secret.** Создание:
  `POST /api/v2.0/robots` с телом v2.0 (`level:"system"`, `permissions:[{kind:project,namespace:<proj>,
  access:[push,pull]}]`, `duration:-1`). **Уровень system, а не project**: project-level robots Harbor НЕ
  перечисляет, их id нельзя резолвить по имени для отзыва; system-robots перечислимы и резолвятся через
  `GET /robots?q=name=<запрошенное-имя>`. Имя Harbor префиксует сам (`robot$<reqName>`); в CI/CD-переменные
  GitLab инъектируются ФАКТИЧЕСКИ возвращённые `name`/`secret`, не сконструированные. Повтор имени → `409`
  (идемпотентно).

- **read-only одного проекта — отзыв robot (нет project.read_only).** `SetReadOnly` (decommission,
  ADR-0012) резолвит id робота по имени и `DELETE /api/v2.0/robots/<id>` — CI/CD больше не может push/pull,
  проект фактически недоступен на запись. Проект СОХРАНЯЕТСЯ. Необратимо (точка невозврата): новый secret
  при воссоздании. Наблюдаемость: после отзыва робот не резолвится (`GET /robots?q=name=` → `[]`).
  Компенсация `SetWritable` воссоздаёт system-робота (новый secret). `DeleteProject` (откат создания,
  ADR-0005) ДОПОЛНИТЕЛЬНО отзывает робота: удаление проекта оставляет system-робота сиротой.

- **transfer — наблюдаемый допустимый маркер метаданных.** Harbor не умеет rename/transfer проекта и
  молча игнорирует произвольные ключи метаданных. `UpdateMetadata` (transfer, ADR-0013) проставляет
  допустимое наблюдаемое поле `ProjectMetadata` (`auto_scan="true"`) на проекте-источнике (`<source>-<name>`,
  имя не меняется). Имя целевого проекта Harbor не хранит — маркер фиксирует факт переноса на Harbor-плече
  (физический перенос репозиториев — push/pull — вне scope MVP). Ассерт переноса — значение этого поля.

- **Идемпотентность — по кодам/`getFound`, не по тексту.** Создание проекта/робота → `201`, повтор → `409`
  (успех); удаление по id/имени → `200`, отсутствующего → `404` (успех/no-op); PUT метаданных → `200`
  (upsert). `okExtra`/`getFound` пересмотрены под реальные коды Harbor.

- **Выбор реализации по наличию `HARBOR_USERNAME`+`HARBOR_PASSWORD`.** Заданы оба → клиент против
  реального Harbor; иначе — клиент против мока (поведение по умолчанию). In-memory стаб
  (`HasHarbor`/`IsHarborReadOnly`/`HarborProject`) остаётся для дефолтного прогона. SSRF-guard для
  стендового Harbor на приватном http-адресе выключается `SSRF_DISABLED=true`; в проде guard включён.

## Consequences

- Реальный Harbor поднимается отдельным compose-профилем (`docker-compose.harbor.yml` + bundle
  `deploy/compose/harbor/`) — только локально/ручно, не в CI и не в дефолтной локалке (Harbor тяжёлый,
  как GitLab); health-gate на `GET /api/v2.0/health`; цели `make harbor-up/seed/test/down/harbor/logs`.
- Компенсации наблюдаемы против реального Harbor: `DeleteProject` (откат создания + отзыв robot, ADR-0005),
  `SetReadOnly`/`SetWritable` (decommission/компенсация, ADR-0012), `UpdateMetadata` (transfer, ADR-0013).
- Интеграционный набор `tests/e2e/harbor_integration_test.go` (тег `integration`, `requireHarbor`)
  ассертит фактическое состояние через Harbor API (проект, robot, маркер переноса); GitLab/Vault — моки.
- Контракт периметра/proto/кодоген не затрагиваются (`gen:check` зелёный).
- НЕ для прода: продовая модель (системный robot worker-а, HA/TLS, Trivy/Notary/подпись, реальные
  push/pull, ротация секретов) вне scope и отмечена как Non-Goal.
