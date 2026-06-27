## Context

DevInfra worker (`services/devinfra-worker`) исполняет Temporal-воркфлоу провизии и компенсаций
против GitLab/Vault/Harbor через узкие интерфейсы (`integration-clients`). После `devinfra-real-gitlab`
(ADR-0019) и `devinfra-real-vault` (ADR-0020) реальными сделаны GitLab- и Vault-плечи; Harbor остаётся
WireMock-моком на `:9083` (`HARBOR_BASE_URL=http://mock-harbor:8080`). Текущий `harborHTTP`
(`services/devinfra-worker/internal/integrations/http.go:465-525`) писался под мок:

- **Нет аутентификации.** `doer` Harbor — общий `sharedDoer` без заголовков. Реальный Harbor на любой
  `/api/v2.0/...` без авторизации отвечает `401`. Реальный Harbor v2.0 использует HTTP **Basic** admin
  (`Authorization: Basic base64(admin:Harbor12345)`), а не кастомный заголовок-токен.
- **`CreateProject`** (`:470`): `POST /api/v2.0/projects` с `{project_name}` → ок (реальный Harbor →
  `201`, повтор → `409`). Затем `POST /api/v2.0/robots` с `{name: "robot$<proj>", level: "project"}` —
  под реальный v2.0 тело НЕДОСТАТОЧНО: нужны `level` + `permissions[]` (kind/namespace/access) +
  `duration`; минимальное тело даёт `400/422`. Имя робота Harbor формирует САМ (`robot$<...>`) и
  возвращает `{id, name, secret}` — инъекция в GitLab-переменные ДОЛЖНА брать фактические `name`/
  `secret`, не сконструированные.
- **`DeleteProject`** (`:496`): `DELETE /api/v2.0/projects/<name>`. Реальный v2.0 принимает имя или id
  в path (id по умолчанию; имя — через `X-Is-Resource-Name: true` либо резолвинг
  `GET /api/v2.0/projects?name=`). Удаление требует ПУСТОГО проекта (репозиториев нет — у нас пусто).
  `404` идемпотентно.
- **`SetReadOnly`** (`:501`): `PUT /api/v2.0/projects/<name>` с `{metadata:{public:false}, read_only:
  true}` + `DELETE /api/v2.0/robots/robot$<proj>`. ДВА разрыва: (1) в Harbor НЕТ project-level
  `read_only` в метаданных проекта — поле игнорируется/отвергается; (2) `DELETE /api/v2.0/robots/...`
  ждёт ЧИСЛОВОЙ `id`, а код шлёт ИМЯ → `400/404`. Реальный «недоступен на запись» = ОТЗЫВ robot
  (удаление по id) — наблюдаемо.
- **`SetWritable`** (`:512`): `PUT .../projects/<name>` с `{read_only:false}` — поле несуществующее.
  Компенсация должна ВОССОЗДАТЬ robot.
- **`UpdateMetadata`** (`:519`): `PUT .../projects/<name>` с `{metadata:{owner_project: target}}` —
  `owner_project` НЕ входит в фиксированный набор полей `ProjectMetadata` Harbor; произвольные ключи
  могут отвергаться. Нужно наблюдаемое ДОПУСТИМОЕ поле метаданных.

Ограничения: комментарии в коде — только на русском; ветка `change/devinfra-real-harbor` от master;
SSRF-guard сохранить (на стенде `SSRF_DISABLED=true`); БЛОК 7 (integration-тег, in-memory/мок в
дефолте); `gen:check` зелёный; контракт периметра/proto не трогать.

> **Эмпирическая проверка — обязательна перед финализацией кода.** Решения ниже фиксируют ОЖИДАЕМУЮ
> семантику Harbor v2.0 по документации/опыту (как с Vault). Реальный Harbor может расходиться
> (точные коды robot/project, формат имени робота, набор допустимых полей metadata, путь health).
> Поэтому ПЕРВЫЙ шаг apply (tasks 1.x) — поднять Harbor в Docker и пробить КАЖДЫЙ метод (URL/тело/код)
> ЭМПИРИЧЕСКИ; расхождения зафиксировать комментариями в коде и в ADR. По памяти НЕ угадывать.

## Goals / Non-Goals

**Goals:**
- Реальный Harbor в отдельном compose-override; health-gate; не ломая
  локалку/e2e/gitlab-профиль/vault-профиль/conformance.
- `harborHTTP` под реальный Harbor v2.0 API: HTTP Basic только на Harbor; резолвинг robot id (и при
  необходимости project id); фактические name/secret робота; read-only через отзыв robot; коды,
  идемпотентность через `getFound`/коды; флаг `real`; in-memory/мок сохранены.
- Детерминированный сид (admin-пароль фикстура) по образцу `vault-seed`/`gitlab-seed`.
- Интеграционный набор воркфлоу против Harbor API (тег `integration`, `requireHarbor`); Makefile
  `harbor-*`.

**Non-Goals:**
- Реальные GitLab/Vault меняются (остаются по своим профилям как есть).
- Изменение контракта периметра/proto/кодогена.
- Kubernetes; продовый Harbor/HA; Trivy-сканирование/Notary/подпись образов; реальные push/pull
  образов (нужен лишь API проектов/robot); ротация секретов на проде.
- Замена поллинга на SSE; редизайн портала.

## Decisions

### D1. Способ запуска Harbor в Docker: официальный installer-bundle с пиннутой версией

Harbor — НЕ один образ, а связка (core+db+redis+registry+jobservice+portal+nginx-proxy),
официально разворачиваемая из online/offline installer-bundle через `prepare` (генерит
`docker-compose.yml` из `harbor.yml`). Решение: **закоммитить пиннутый по версии набор сервисов
Harbor как `docker-compose.harbor.yml` с закоммиченным конфигом** (минимальный HTTP-доступ, без TLS —
стенд на приватном адресе), запуская официальные образы `goharbor/*` пиннутой версии. Образы пиннуты
тегом для воспроизводимости.

**Альтернатива (отклонена для MVP): урезанный API-only субсет** (только core+db+redis+registry без
portal/jobservice/nginx). Отвергнута: Harbor core ожидает зависимости (БД с миграциями, redis,
registry, secret-конфиг от `prepare`); ручная сборка субсета хрупка и расходится с тем, что Harbor
поддерживает, увеличивая риск «работает не так, как реальный». Официальный bundle детерминированнее
для интеграционной проверки API. Тяжесть приемлема: профиль локальный ручной (см. D6).

### D2. Аутентификация worker→Harbor: HTTP Basic admin (фикстура)

Harbor стартует с `HARBOR_ADMIN_PASSWORD` — известным фиксированным admin-паролем (фикстура стенда,
как `glpat-...` у GitLab и dev-root-token у Vault). Worker аутентифицируется admin-логином/паролем
через заголовок `Authorization: Basic base64(user:pass)` на КАЖДОМ Harbor-запросе. Креденшелы
подаются через `integrations.Config.HarborUsername`/`HarborPassword` (env `HARBOR_USERNAME`/
`HARBOR_PASSWORD` или `HARBOR_PASSWORD_FILE`, по образцу `gitLabToken()`/`vaultToken()` + общий
`tokenFromEnv`); в код сверх фикстуры не хардкодятся; пароль не логируется.

Реализация: в `NewHTTPClients` — отдельный `harborDoer` с
`headers: {"Authorization": "Basic " + base64(user+":"+pass)}` ТОЛЬКО при непустых креденшелах (иначе
общий `sharedDoer` без заголовка — мок-путь). Заголовок не протекает на GitLab/Vault (у каждого свой
`doer`). Это зеркалит `gitlabDoer`/`vaultDoer` (`http.go:77-87`). Basic-заголовок собираем один раз
при сборке клиента (не на каждый запрос).

**Альтернатива (отклонена): системный robot-аккаунт Harbor** вместо admin. Отвергнута для MVP: admin
проще и детерминированнее для одного сервис-актора на стенде (тот же аргумент, что для PAT/dev-token
в ADR-0019/0020); системный robot — продовая модель, вне scope.

### D3. Robot-аккаунт: тело v2.0, фактические name/secret, удаление по числовому id

Создание: `POST /api/v2.0/robots` с телом v2.0 —
`{"name":"<proj>-<name>","duration":-1,"level":"project","permissions":[{"kind":"project",
"namespace":"<projName>","access":[{"resource":"repository","action":"push"},{"resource":
"repository","action":"pull"}]}]}` (минимальный набор для push/pull; точные `resource/action` и
`duration:-1`=бессрочно проверить эмпирически). Ответ `201` несёт `{id, name, secret}`, где `name`
Harbor ПРЕФИКСУЕТ сам (`robot$<...>` или `robot$<proj>+<name>` — формат проверить эмпирически).
`HarborResult.RobotName`/`RobotToken` берут ИМЕННО возвращённые `name`/`secret` (не сконструированные),
далее инъектируются в GitLab-переменные `HARBOR_ROBOT_NAME`/`HARBOR_ROBOT_TOKEN` (`InjectVariables`).

Отзыв/удаление: реальный `DELETE /api/v2.0/robots/<id>` ждёт ЧИСЛОВОЙ id. Резолвинг id по имени —
`GET /api/v2.0/robots?q=name=<name>` (или список robots проекта с фильтром), как group_id/user_id в
GitLab (ADR-0019). Идемпотентность: робот не найден → no-op-успех. Под мок (`real=false`) сохраняется
текущий путь (обращение по имени, коды мока).

### D4. read-only одного проекта: отзыв robot как наблюдаемый «недоступен на запись» (точка невозврата)

В Harbor НЕТ простого project-level `read_only` в теле `PUT /api/v2.0/projects/<id>` (поле
игнорируется/отвергается; глобальный read-only — это системная настройка всего реестра, не одного
проекта). Решение: **`SetReadOnly` = ОТЗЫВ robot-аккаунта проекта** (`DELETE /api/v2.0/robots/<id>` по
резолвленному id) — CI/CD больше не может push/pull, проект фактически недоступен на запись. Это
зеркалит Vault `RevokeSecretID` (ADR-0020). Наблюдаемость в тесте: после decommission robot проекта
ОТСУТСТВУЕТ (список robots проекта пуст / GET по id → `404`), сам проект СОХРАНЯЕТСЯ. Необратимо
(точка невозврата, ADR-0012): секрет отозванного робота не вернуть. Компенсация `SetWritable` —
ВОССОЗДАЁТ robot (новый secret). Идемпотентность: повторный отзыв (`404`) — успех.

**Альтернатива (отклонена): пометка проекта недопустимым полем metadata** — поле отвергается Harbor,
ненаблюдаемо как «read-only». Отзыв robot — единственный наблюдаемый и семантически верный механизм.

### D5. UpdateMetadata (transfer): наблюдаемое ДОПУСТИМОЕ поле метаданных

`owner_project` НЕ входит в фиксированный набор `ProjectMetadata` Harbor (`public`,
`enable_content_trust`, `prevent_vul`, `severity`, `auto_scan`, `reuse_sys_cve_allowlist`, ...);
произвольные ключи отвергаются. Решение: `UpdateMetadata` обновляет ДОПУСТИМОЕ наблюдаемое поле
метаданных проекта как маркер переноса (кандидат — переключить `public` либо иной допустимый
boolean-флаг; точный набор допустимых ключей и поведение на произвольный ключ ПРОВЕРИТЬ эмпирически).
Ассерт transfer-теста — наблюдаемое значение этого поля через `GET /api/v2.0/projects/<id>`. Harbor
не поддерживает rename/transfer проекта; в рамках MVP перенос на Harbor-плече = обновление
наблюдаемого метаданного маркера (а не физический перенос репозиториев — push/pull вне scope).

### D6. Профиль и CI: отдельный override, локальный ручной прогон (зеркалим gitlab)

Harbor тяжёлый (связка контейнеров, тяжёлый старт — как GitLab). По умолчанию **зеркалим
gitlab-паттерн**: отдельный override `docker-compose.harbor.yml`, локальный ручной прогон, CI НЕ
трогаем. Health-budget соразмерен GitLab (а не Vault) — старт минуты.

Обоснование: (1) единообразие операционной модели интеграционных профилей (один up/seed/test/down
контракт для всех «реальных» плеч); (2) интеграционные тесты против внешнего стенда — БЛОК 7,
отдельный тег, вне дефолтного `go test`; (3) добавление в CI — отдельное решение с бюджетом/флейки-
рисками (Harbor тяжелее Vault), не блокирует эту задачу.

### D7. Где тесты: отдельный harbor-набор против реального Harbor + моки GitLab/Vault

Создаём **отдельный** `tests/e2e/harbor_integration_test.go` с `requireHarbor` (по env `HARBOR_ADDR`/
`HARBOR_USERNAME`/`HARBOR_PASSWORD`), а стенд `docker-compose.harbor.yml` поднимает реальный Harbor,
оставляя GitLab/Vault моками. Альтернатива (комбинировать — сквозная инъекция Harbor-robot-token →
GitLab-variables на реальных бэкендах) отклонена для MVP: связывает тяжёлые профили, увеличивает время
и площадь флейков; изоляция плеча проще диагностируется и зеркалит ADR-0019/0020 (по умолчанию
зеркалим vault-решение). Harbor-набор активируется только при своём gate-env и не влияет на
`make e2e`/`make gitlab`/`make vault`.

### Sequence: воркфлоу ↔ activities ↔ Harbor (v2.0)

```
Создание (CreateServiceWorkflow, порядок GitLab→Harbor→Vault→Inject→ACTIVE):
  workflow ──HarborCreateProject──▶ harborHTTP
                                     POST /api/v2.0/projects {project_name}          (201; 409 повтор)
                                     POST /api/v2.0/robots {level,permissions[],dur}  (201 {id,name,secret})
            (Authorization: Basic admin на каждом запросе)
            HarborResult.RobotName/RobotToken = фактические name/secret из ответа
            → InjectVariables: HARBOR_ROBOT_NAME/HARBOR_ROBOT_TOKEN в GitLab-переменные
  RetryPolicy: Temporal-дефолт; non-retryable «ProvisioningFatal» → компенсация
  Компенсация (ADR-0005): HarborDeleteProject ─▶ DELETE robots/<id> + DELETE projects/<id> (404=успех)

Decommission (ADR-0012, точка невозврата):
  HarborSetReadOnly ─▶ резолв robot id → DELETE /api/v2.0/robots/<id> (необратимо; 404=успех)
  Компенсация SetWritable ─▶ воссоздать robot (новый secret)

Transfer (ADR-0013):
  HarborUpdateMetadata ─▶ PUT /api/v2.0/projects/<id> {metadata:{<допустимое поле>:...}} (наблюдаемо)
```

Идемпотентность воркфлоу — детерминированный WorkflowID (как в существующих воркфлоу; см.
`e2e-change-owners-single-shot`). Переходы статуса каталога — guarded-CAS (ADR-0004/0008), без
изменений в этой задаче.

## Risks / Trade-offs

- **[Эмпирические расхождения Harbor API от памяти]** (как у GitLab transfer PUT не POST; у Vault
  destroy-all). Ожидаемые точки: тело/коды robot v2.0, формат возвращённого имени робота, путь
  health (`/api/v2.0/health` vs `/ping`), резолвинг project по имени (`X-Is-Resource-Name` vs
  `?name=`), набор допустимых полей metadata. → Проверять против ПОДНЯТОГО Harbor (tasks 1.x), а не по
  памяти; уроки фиксировать комментариями и в ADR.
- **[Тяжесть стенда / флейки старта]** Harbor — связка контейнеров, старт минуты. → Health-gate
  соразмерен GitLab (настраиваемый бюджет); локальный ручной прогон, CI не трогаем.
- **[read-only без project-level флага]** Семантика «недоступен на запись» реализуется отзывом robot;
  убедиться, что это наблюдаемо и согласуется с контрактом ADR-0012 (отозвать ДОСТУП, не снести
  проект). → Проект сохраняется, robot отозван; ассерт через список robots проекта.
- **[Регрессия мок-пути]** Изменения `harborHTTP` могут сломать дефолтный `make e2e`. → Ветвление по
  `real`; мок-путь и in-memory не трогаем; покрыто дефолтным прогоном.
- **[Утечка пароля]** Basic-пароль не логировать; отдельный `doer` (не протекает на GitLab/Vault);
  пароль через `HARBOR_PASSWORD_FILE` при необходимости. gosec G101 на const с «Token/Password» в
  имени — `//nolint:gosec` с обоснованием (имя заголовка/ключа, не секрет), как у `vaultTokenHeader`.
- **[gen:check]** Контракт периметра/proto/кодоген не трогаем — `gen:check` зелёный на каждом шаге.

## Migration Plan

Инкрементально, каждый шаг зелёный: (0) ЭМПИРИЧЕСКАЯ проверка реального Harbor API против поднятого
стенда (зафиксировать методы/тела/коды); (1) compose-override + сид + Makefile (стенд поднимается,
сид идемпотентен); (2) `Config.HarborUsername`/`HarborPassword` + `harborDoer` + `main.go`
`harborCreds()` (мок-путь не затронут); (3) `harborHTTP` реальная семантика за флагом `real`
(robot v2.0 body, резолвинг id, read-only через отзыв robot, UpdateMetadata, okExtra); (4)
интеграционный набор + `harbor-*` цели. Откат: удалить override/сид/набор и ветку `real`-семантики —
дефолтный мок-путь и контракт периметра не затронуты, `gen:check` зелёный на каждом шаге.

## Open Questions

- Точный путь health-gate Harbor (`GET /api/v2.0/health` со `{status:"healthy"}` vs `/api/v2.0/ping`
  → "Pong") — закрыть эмпирически.
- Точное тело `POST /api/v2.0/robots` v2.0 (минимальный набор `permissions[]`, формат `duration`,
  формат возвращённого `name`) — закрыть эмпирически.
- Резолвинг project/robot по имени → id: `X-Is-Resource-Name` vs `GET ?name=`/`?q=name=`; форма
  фильтра robots — закрыть эмпирически.
- Набор допустимых полей `ProjectMetadata` для наблюдаемого маркера transfer и поведение Harbor на
  произвольный ключ — закрыть эмпирически (выбрать допустимое наблюдаемое поле).
- Нужен ли предсозданный проект/сущности сидом, или воркфлоу создаёт всё сам — уточнить при написании
  ассертов.
