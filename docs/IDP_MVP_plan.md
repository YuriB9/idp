# IDP — план разработки MVP (срез DevInfra)

**Объём MVP:** реалистичный срез целевой C4-архитектуры. Отдельный сервис проектов + DevInfra worker, реальные Keycloak + Oauth2-Proxy, остальные сервисы (IDM / команды / квоты) — по минимуму, но с правильными границами. Все четыре user stories. Внутренние вызовы — gRPC/protobuf сразу.

**Стек:** Vite + React + TS (SPA, статика, без SSR/BFF) · Go 1.26 · Temporal · PostgreSQL · DragonflyDB (кэш IDM) · gRPC/protobuf (внутри) · https/json (портал ↔ Oauth2-Proxy, внешние системы) · Kubernetes (прод) / docker-compose (локально).

**Управляемые ресурсы в MVP:** только DevInfra worker → GitLab (репозиторий), Vault (секреты/AppRole), Harbor (директория для образов). K8s / DBA worker'ы — вне MVP.

---

## Как MVP ложится на целевую архитектуру

| Целевой контейнер (C4) | В MVP |
|---|---|
| Портал разработчика (React) | **Да**, полноценно |
| Oauth2-Proxy (Go) | **Да**, реальный, OIDC через Keycloak |
| Keycloak (Identity provider) | **Да**, реальный |
| Основной API-шлюз (Go) | **Да**, тонкий: маршрутизация + вызов IDM |
| Сервис IDM (права/роли) | **Минимум**: gRPC-интерфейс + базовый RBAC, Postgres + DragonflyDB-кэш |
| Сервис управления проектами (Go: API + Temporal worker) | **Да** — ядро MVP |
| DevInfra worker (Go) | **Да** — единственный worker в MVP |
| Сервис команд | **Минимум**: модель команд внутри сервиса проектов или тонкий сервис; полноценное управление командами — позже |
| Сервис квот и ресурсов | **Заглушка/минимум**: квоты не провижентся, только модель |
| Интеграционный API-шлюз + внешние системы (Service Desk, Incident mgmt) | **Вне MVP** |
| K8s worker, DBA worker, Postgres/Kafka как управляемые ресурсы | **Вне MVP** |
| Temporal cluster | **Да** |

**Принцип резки:** то, что в MVP «по минимуму» или «заглушка», всё равно вызывается через целевые границы (gRPC-контракт к IDM, постановка задач в Temporal). Это значит, что наполнение урезанных сервисов позже **не потребует переписывать** сервис проектов и worker.

---

## Терминология (выравнивание user stories с моделью C4)

В целевой модели верхняя сущность — **проект**, внутри него **приложения/услуги**. Мои прежние user stories про «сервис» переносятся так: «сервис» ≈ создаваемая единица внутри проекта (приложение/услуга) либо сам проект — зафиксировать в начале. Команды и квоты — отдельные сущности (отдельные сервисы в C4). Ниже использую «сервис» в смысле создаваемой единицы, как в исходных user stories.

---

## Архитектурные решения (зафиксированы)

1. **Аутентификация — Oauth2-Proxy + Keycloak, не «голый OIDC из SPA».** Портал отдаёт запросы (https/json) на Oauth2-Proxy, тот аутентифицирует через Keycloak по OIDC и проксирует уже авторизованный трафик на основной API-шлюз. SPA не жонглирует токенами вручную — вопрос «токен в браузере без BFF» снимается, Oauth2-Proxy и есть слой авторизации.
2. **Авторизация прав (RBAC) — в сервисе IDM**, не «проверка в коде шлюза». API-шлюз проверяет роли/права через IDM (gRPC). IDM хранит данные в Postgres, кэширует в DragonflyDB. В MVP набор ролей минимален, но интерфейс вызова — целевой.
3. **Внутренние вызовы — gRPC/protobuf.** .proto-контракты как источник правды для меж-сервисного взаимодействия. https/json только на стыке с порталом (через Oauth2-Proxy) и с внешними системами (в MVP внешних нет).
4. **Контракт портал ↔ платформа.** OpenAPI на периметре API-шлюза → кодоген TS-клиента для портала (orval / openapi-typescript), zod-схемы форм из той же спеки.
5. **Сервис проектов = API + Temporal worker (раздельно масштабируемые процессы).** API принимает команды, валидирует, проверяет права через IDM, стартует Temporal workflow. DevInfra worker исполняет activities против GitLab/Vault/Harbor.
6. **Идемпотентность.** Детерминированный `WorkflowID` на операцию (напр. `create-service-{project}-{name}`). Каждая activity идемпотентна и имеет компенсацию.
7. **Политика отката при создании (ключевое).** Если GitLab и Harbor созданы, а Vault окончательно недоступен → **полный Saga-откат**, а не статус `failed`. Причина: без Vault сервис нежизнеспособен (нечем аутентифицироваться), висящие репозиторий/директория Harbor засоряют системы и ломают повторные попытки по конфликту имён. Компенсация идемпотентна; если откатить **не удалось** — тогда `failed` + алерт оператору.
8. **Состояние.** Каталог проектов/сервисов — Postgres (сервис проектов). IDM — свой Postgres + DragonflyDB. Temporal event history — отдельно. Статусы сервиса: `creating` · `active` · `decommissioned` · `failed`.

---

## Инженерные стандарты (переиспользованы из уроков предыдущего проекта на близком стеке)

Стек совпадает (Go-монорепо, React+Vite+TanStack Query), поэтому большинство уроков переносятся напрямую. **Главная адаптация:** в том проекте событийный конвейер был на голом AMQP — у нас оркестратор **Temporal**, поэтому уроки про консьюмеры/ack/реконнект перекладываются на Temporal SDK и activity-ретраи, а не копируются дословно. Ниже — только то, что подходит по стеку.

### Структура репозитория
Монорепо на `go.work`: `./pkg` (общие пакеты) + по модулю на сервис (`./services/gateway`, `./services/idm`, `./services/projects`, `./services/devinfra-worker`) + `./tests/e2e` + изолированный модуль `./tools` (вне `go.work`, пин инструментов, запуск с `GOWORK=off`). Frontend — `./web`.

### БЛОК 0 — Фундамент вперёд (делается ДО доменной логики, в Этапе 1)
CI с первого коммита (GitHub Actions, матрица по модулям go.work): `go test -race -shuffle=on`, `go mod tidy && git diff --exit-code`, `golangci-lint`, **`govulncheck` блокирующий** (особенно важно — не тащить уязвимые версии auth-зависимостей при работе с Keycloak/JWT), отдельный integration-джоб. `.golangci.yml`: errcheck, govet, staticcheck, revive, gosec, bodyclose, sqlclosecheck, nilerr, errname, **paralleltest**, package-comments. Dependabot/Renovate. Dockerfile на каждый сервис (для Trivy/SBOM). **Доменную логику начинаем только после зелёного CI.**

### Общие `pkg/*` (не копипастить по сервисам)
`pkg/httpserver` (единые таймауты, graceful shutdown, middleware-стек: Recoverer, RequestID, rate-limit, auth-toggle, content-aware `/readyz`), `pkg/httpclient` (S2S: маппинг `404→ErrNotFound`/`409→ErrConflict`, тюнингованный Transport), `pkg/config` (env-хелперы, принимать легитимный `0`), `pkg/errs` (канонические sentinel'ы — без локальных дублей), `pkg/db` (`NewPool` с конфигом пула), `pkg/auth`, `pkg/ssrf`, `pkg/logger`. Для gRPC — аналогичный общий interceptor-стек (recovery, request-id, otel, auth).

### БЛОК 2 — Безопасность (fail-closed) — для нас критично из-за Keycloak + внешних API
- **Auth fail-closed:** пустой `JWKS_URL` → `os.Exit(1)`, не passthrough. Отключение auth только явным `AUTH_DISABLED=true`. *(Даже при том, что аутентификацию несёт Oauth2-Proxy — сервисы за ним валидируют JWT сами, fail-closed.)*
- JWT строго: `WithAudience/WithIssuer/WithValidMethods/WithExpirationRequired`. JWKS_URL форсить https.
- admin/god-key — `subtle.ConstantTimeCompare`, не `==`; заложить путь к ротации/scope.
- **SSRF-guard — особенно важен здесь:** DevInfra activities ходят в URL GitLab/Vault/Harbor, которые в целевой модели могут быть tenant-задаваемыми. `pkg/ssrf.ValidateURL` (https + блок private/loopback/link-local/ULA) на записи **и** `GuardedDialContext` на отправке (против TOCTOU/DNS-rebinding).
- Rate-limit на webhooks/auth-эндпоинтах. Никогда не отдавать `err.Error()` клиенту — внутренние тексты только в лог.

### БЛОК 3 — Жизненный цикл, адаптировано под Temporal
- **Реконнект/supervisor консьюмеров → забота Temporal worker SDK.** Не пишем свою петлю реконнекта к брокеру; настраиваем worker'ы и activity-ретраи (RetryPolicy, таймауты, heartbeat для долгих activity).
- **Ack/nack/requeue → семантика activity:** успех = завершение activity; retriable-ошибка → `temporal` ретраит по политике; нерекаверабл → `ApplicationError`-non-retryable (аналог Drop без requeue) → ветка компенсации в workflow.
- **Graceful-drain HTTP/gRPC-частей** остаётся как был: фоновые горутины в `errgroup`, `g.Wait()` после `Shutdown`; in-flight — drain-контекст `WithTimeout(WithoutCancel(ctx), 30s)`. **`recover` в каждой фоновой горутине.**
- `singleflight` против stampede на кэше IDM (DragonflyDB) + вытеснение протухших ключей.

### БЛОК 4 — База данных (атомарность статусов — прямо наш случай)
- **Guarded-CAS на переходах статусов сервиса:** `UPDATE ... WHERE id AND status=$expected`, проверка `RowsAffected==0 → ErrConflict (409)`. Не check-then-act — иначе гонки на `creating→active→decommissioned`. Это критично: workflow и API могут пытаться менять статус конкурентно.
- Многошаговые записи — в транзакции (`withTx` + узкий `dbConn` для `*pgxpool.Pool` и `pgx.Tx`). Публикация событий/статусов — **после commit**.
- Keyset-пагинация по `(created_at, id)`, курсор — непрозрачный base64. Конфиг пула обязателен.

### БЛОК 5–6 — Error handling и Observability
- Ошибки `%w`, проверка `errors.Is/As`, не глотать (`_ =`) — особенно аудит/history. Единый ключ slog **`"err"`** везде.
- `Recoverer`/recovery-interceptor во всех сервисах (HTTP и gRPC).
- `/readyz` content-aware: реально пингует Postgres/DragonflyDB + **сигнал живости Temporal worker** (k8s не должен слать трафик в мёртвый под).
- **Никогда `r.URL.Path` как Prometheus-метку** — `chi.RouteContext().RoutePattern()`. OpenTelemetry end-to-end, trace-context через метаданные (в т.ч. в Temporal). `RequestID` во всех сервисах.
- Метрики конвейера: счётчики на этапах workflow, успехи/компенсации activity, ошибки походов в GitLab/Vault/Harbor, лаг.

### БЛОК 7 — Тестирование
- `goleak` (`TestMain`) в пакетах с горутинами. Table-driven + `t.Parallel()` (paralleltest).
- Стаб/in-memory тесты — **в дефолтном прогоне**; тег `integration` только для реально внешних. Temporal — `temporal testsuite` (env для workflow, мок activity).
- Обязательно покрыть: guarded-CAS-конфликт стора, переходы статусов, rate-limit, кэш IDM, компенсации workflow.

### БЛОК 8–9 — Стиль и Frontend
- Package-doc в каждом пакете; избегать стуттера (`store.Store`); конструкторы >4 параметров → options-struct; enum'ы с zero-value `Unknown`, не маппить незнакомое в дефолт молча.
- Frontend: единый стиль валидации **react-hook-form + zod** (схема = источник правды), общие валидаторы в `lib/validators.ts`, межполевые инварианты до отправки, **рантайм-валидация ответов API через zod `.parse`** (рассинхрон контракта всплывает явно). RTL + `user-event` + jsdom сразу.

### Процесс
ADR в `docs/adr/` на каждое значимое решение; PR с зелёным CI; wire-формат .proto и событий — контракт, изменения помечать BREAKING. **Стартовые ADR заведены отдельно** (см. файлы ADR-0001…0004): транспорт Temporal, gRPC внутри, auth-модель Oauth2-Proxy+Keycloak+per-service-JWT, guarded-CAS статусов.

---

## Этап 1. Инфраструктура, контракты, скелет (Недели 1–2)

> Включает **БЛОК 0 целиком** (CI, линтеры, govulncheck, `./tools`) и каркас `pkg/*` — до любой доменной логики.


**Контракты first:**
- .proto для меж-сервисных вызовов (API-шлюз ↔ IDM, API-шлюз ↔ сервис проектов), кодоген Go-стабов.
- OpenAPI периметра (портал ↔ API-шлюз), кодоген TS-клиента и zod-схем.

**docker-compose (локалка):** Keycloak, Oauth2-Proxy, PostgreSQL (проекты), PostgreSQL (IDM), DragonflyDB, Temporal Server + UI, API-шлюз, IDM, сервис проектов (API), DevInfra worker, mock-серверы GitLab / Vault / Harbor.

**Backend-скелеты (Go 1.26):**
- HTTP-роутер (chi/echo) на периметре API-шлюза; gRPC-серверы у IDM и сервиса проектов.
- Temporal Client в сервисе проектов; worker-процесс DevInfra отдельно.
- Graceful shutdown через `context` для всех процессов.
- Слои сервиса проектов: `transport (HTTP/gRPC)` → `usecase` → `temporal client` → `workflows/activities` → `integration clients (GitLab/Vault/Harbor)` за интерфейсами (мокаются).

**Keycloak + Oauth2-Proxy:** realm, клиент для портала, базовые роли; Oauth2-Proxy перед API-шлюзом, проверка OIDC-потока end-to-end.

**IDM (минимум):** gRPC `CheckAccess(user, resource, action)`, модель ролей, Postgres + кэш DragonflyDB.

**Frontend-скелет:** Vite + React + TS, статика, TanStack Query, react-hook-form + zod, UI-библиотека (MUI / Ant / Radix). Запросы идут через Oauth2-Proxy.

---

## Этап 2. Интеграционный слой — DevInfra activities (Недели 2–3)

Клиенты за интерфейсами, идемпотентны, с компенсациями.

- **GitLab:** создание репозитория в нужной группе, members/права, CI/CD-переменные, archive/transfer.
- **Vault:** политики, KV, **AppRole** (RoleID/SecretID для сервиса), миграция путей, отзыв SecretID.
- **Harbor:** директория/проект для образов, Robot Account, read-only, маппинг прав.

---

## Этап 3. Бизнес-логика — Temporal Workflows (Недели 3–5)

Все workflow живут в сервисе проектов, исполняются DevInfra worker'ом. Перед стартом любого workflow API-шлюз/сервис проектов проверяет права через IDM.

### «Создание сервиса»
1. Статус `creating` в каталоге; проверка уникальности имени.
2. **Activity:** репозиторий в GitLab (группа проекта).
3. **Activity:** директория в Harbor + Robot Account.
4. **Activity:** политики и AppRole в Vault.
5. **Activity:** инъекция секретов (Vault RoleID/SecretID, Harbor Robot-токены) в CI/CD-переменные GitLab — без этого сервис нечем собирать/деплоить.
6. Статус `active`.
- Компенсации в обратном порядке; политика отката — решение №7.

**Фронт:** форма (проект/стрим, команда, название), zod-валидация, экран прогресса по шагам.

### «Изменение владельцев»
1. Резолв логинов → пользователи.
2. **Activity:** синхронизация members/ролей в GitLab.
3. **Activity:** обновление политик доступа в Vault.
4. Обновление owners в каталоге; при необходимости — синхронизация ролей в IDM.
- Декларативный diff (add/remove), не императивно.

**Фронт:** модалка «Изменить состав команды», ввод логинов с автодополнением, превью diff.

### «Перенос сервиса» (самый рискованный — последним)
1. Проверка целевых стрима/системы/команды и прав.
2. **Activity:** transfer проекта в GitLab в новую группу.
3. **Activity:** миграция путей Vault (копирование секретов старый→новый, обновление политик).
4. **Activity:** апдейт метаданных/прав в Harbor.
5. **Activity:** очистка старых доступов/путей.
6. Обновление связей в каталоге.
- **Компенсации:** transfer GitLab и миграция Vault частично необратимы → проектируем как «точка невозврата» + явный алерт оператору при частичном сбое, не молчаливый откат. Предупреждение о последствиях — в UI.

### «Удаление сервиса» (soft delete / decommission)
0. **Предусловие:** нагрузка снята из K8s — проверяем программно до старта workflow. *(В MVP K8s worker отсутствует; проверку делаем через прямой запрос к кластеру либо как явный чек-флаг — зафиксировать.)*
1. **Activity:** archive проекта в GitLab, отзыв доступов.
2. **Activity:** Harbor → read-only, отзыв Robot.
3. **Activity:** отзыв активных SecretID/токенов в Vault — немедленное прекращение доступа.
4. **Activity:** статус `decommissioned` в каталоге. **Данные сохраняются**, физического удаления нет, возможен restore.

**Фронт:** кнопка «Удалить сервис» → подтверждение (ввод имени), явно: decommission, не purge.

---

## Этап 4. Frontend и UX (Недели 5–6)

- Сложные формы (zod + react-hook-form): проект, команда, название, целевой стрим/система.
- Трекинг прогресса: поллинг статус-API или SSE/WebSocket — прогресс-бар «GitLab (✓) → Vault (в процессе)…».
- Панель управления: список сервисов со статусами `Active` / `Creating` / `Decommissioned` / `Failed`.

---

## Этап 5. Развёртывание в Kubernetes (Неделя 7)

- **Сборка:** Dockerfile'ы для API-шлюза, IDM, сервиса проектов (API), DevInfra worker; многоэтапный билд портала (Vite → раздача через Nginx).
- **Манифесты:** Deployment'ы Nginx (портал), Oauth2-Proxy, API-шлюз, IDM, сервис проектов (API), DevInfra worker (масштабируется отдельно). Keycloak — как managed/отдельный деплой.
- **Конфигурация:** Ingress, ConfigMaps, Secret (через Vault) для Temporal, Vault, GitLab, Harbor, Keycloak, подключений к Postgres/DragonflyDB.
- **CI:** lint, тесты, проверка кодогена (.proto + OpenAPI), сборка образов в Harbor.

---

## Тестирование (сквозное, со 2-го этапа)

- **Unit:** activities с мок-клиентами GitLab/Vault/Harbor.
- **Integration:** workflows через Temporal test framework — компенсации, ретраи, точки невозврата.
- **Контрактные:** .proto (gRPC) и OpenAPI (периметр) ↔ сгенерированные клиенты.
- **E2E:** docker-compose со стабами + реальным Keycloak/Oauth2-Proxy, прогон всех четырёх user stories через портал.

---

## Порядок реализации (по нарастанию риска)

1. Контракты + инфра + скелеты + Keycloak/Oauth2-Proxy/IDM-минимум (Этап 1).
2. **Создание сервиса** — закрывает все три интеграции DevInfra, задаёт Saga-паттерн.
3. **Изменение владельцев** — переиспользует GitLab/Vault.
4. **Удаление / decommission** — отрабатывает обратные операции.
5. **Перенос сервиса** — самый сложный, последним.

---

## Ориентировочный таймлайн

| Недели | Этап |
|--------|------|
| 1–2 | Контракты, инфраструктура, скелеты, Keycloak/Oauth2-Proxy/IDM |
| 2–3 | DevInfra activities (GitLab/Vault/Harbor) |
| 3–5 | Workflows (4 user stories) |
| 5–6 | Frontend и UX |
| 7   | Kubernetes |

*Тестирование — параллельно, начиная со 2-го этапа.*

---

## Что осознанно отложено за пределы MVP (но границы заложены)

K8s worker и DBA worker (namespaces/RBAC, Postgres/Kafka-кластеры); интеграционный API-шлюз и внешние системы (Service Desk, Incident management); полноценные сервисы команд и квот с реальной провизией квот; расширенный RBAC в IDM. Все они в MVP присутствуют как контрактные границы (gRPC-интерфейсы, постановка задач в Temporal), поэтому их наполнение не потребует переписывать ядро.
