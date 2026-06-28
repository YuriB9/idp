# Аудит Go-кода монорепо IDP — 2026-06

Системный аудит Go-кода против инженерных стандартов проекта
(`openspec/config.yaml`, `docs/IDP_MVP_План.md` BLOCK 0, ADR 0001..0023) с
применением доменных скиллов `samber/cc-skills-golang@*` под оркестратором
`golang-how-to`. Change: `golang-code-audit`.

## Границы и метод

- **В границах:** Go-модули `pkg`, `services/{gateway,idm,projects,devinfra-worker}`,
  `tests/e2e`, `tools` (≈22.7K LOC, 92 `.go`-файла).
- **Вне границ:** фронтенд `./web`, контракты `.proto`/OpenAPI, сгенерированный
  код (`*.pb.go`), пересмотр принятых ADR.
- **Baseline (не дублируется вручную):** прогон `golangci-lint` v2.12.2 и
  `govulncheck` (`GOWORK=off`) по всем модулям на момент аудита — **0 issues,
  0 уязвимостей**. Аудит ищет то, что эти гейты НЕ покрывают (дизайн
  конкурентности, горячие пути, идиоматичность, прослеживаемость).
- **Критерии severity:** `blocker` — ломает безопасность/корректность;
  `high` — высокий риск инцидента в проде; `medium` — техдолг/анти-паттерн с
  ограниченным влиянием; `low` — косметика/мелкое улучшение.

## Сводка по severity

| Severity | Кол-во | Находки |
| --- | --- | --- |
| blocker | 0 | — |
| high | 0 | — |
| medium | 2 | C1, D1 |
| low | 4 | P1, R1, ST1, D2 |
| **Итого** | **6** | |

**Общий вывод:** кодовая база в отличном состоянии. Инженерные стандарты
(fail-closed, SSRF-guard, guarded-CAS, события-после-commit, единый ключ slog
`err`, content-aware `/readyz`, `RoutePattern()` в метках, goleak,
table-driven + `t.Parallel()`, Temporal testsuite) соблюдаются последовательно.
Блокеров и high-находок нет; найденное — точечные улучшения.

---

## 1. structure-layers

Скиллы: `golang-project-layout`, `golang-structs-interfaces`.

**Находок нет.** Раскладка go.work чистая: `pkg` (общее) + модуль на сервис +
`tests/e2e` + изолированный `tools`. Слои разделены идиоматично:
`grpcapi`/handlers (транспорт) → `usecase` (домен) → `repository` (хранение),
зависимости направлены внутрь. Принцип «accept interfaces, return structs»
выдержан: `usecase.Store`, `usecase.WorkflowStarter`, `grpcapi.Catalog`,
`grpcapi.AccessChecker`, `repository.Querier` — узкие интерфейсы на стороне
потребителя; конструкторы возвращают конкретные `*Repo`/`*Catalog`/`*Server`.
Контракт workflow (`provisioning`/`changeowners`/`transfer`/`decommission`)
публичен и не тянет реализации интеграций в граф API-процесса (activities — по
строковым именам). Циклов импортов нет.

## 2. concurrency

Скиллы: `golang-concurrency`, `golang-context`.

### C1 (medium) — мьютекс удерживается на время сетевого I/O при получении токена

- **Файл:** [services/idm/internal/identity/keycloak.go:207-249](../../services/idm/internal/identity/keycloak.go#L207-L249)
- **Суть:** `accessToken` берёт `c.mu.Lock()` и `defer Unlock()` на всю функцию,
  включая POST-запрос токена сервис-аккаунта к Keycloak. Все конкурентные
  обращения к справочнику (`Search`/`Resolve`) сериализуются за одним in-flight
  запросом токена; на момент истечения токена читатели блокируются на длительность
  сетевого вызова (анти-паттерн «lock across I/O»).
- **Правило/скилл:** `golang-concurrency` — не удерживать мьютекс на время
  блокирующего I/O; для дедупликации одинаковой дорогой операции — `singleflight`.
- **Рекомендация:** вынести сетевой запрос за пределы критической секции
  (double-checked: под локом только чтение/запись `token`/`exp`) либо обернуть
  получение токена в `singleflight.Group` (как уже сделано в `idm/internal/usecase`).
  Влияние ограничено эндпоинтами справочника (на горячем пути `CheckAccess`
  Keycloak не вызывается), поэтому medium, не high.

Прочее: единственная «ручная» горутина — `httpserver.Server.Run`
([pkg/httpserver/server.go:93](../../pkg/httpserver/server.go#L93)) — с буфер.
каналом и graceful-shutdown через `context.WithoutCancel`, покрыта goleak.
Распространение `context` сквозное; `context.Background()` только в `main`.

## 3. error-handling

Скиллы: `golang-error-handling`.

**Находок нет.** Канонические sentinel-ошибки в `pkg/errs`, обёртывание через
`%w`, проверки `errors.Is`/`errors.As` (в т.ч. `pgconn.PgError` по коду `23505`).
Single-handling-rule соблюдён: периметр (`grpcapi.mapError`,
`httpserver`/handlers) маппит доменные ошибки в коды и **не отдаёт `err.Error()`
наружу**, логируя детали под единым ключом slog `err`. Намеренное использование
`%v` (а не `%w`) в `identity` для внутренних деталей при `%w`-обёртке sentinel
`ErrUnavailable` — корректно (цепочка `errors.Is` сохраняется, сырьё не утекает).

## 4. reliability-panics

Скиллы: `golang-safety`, `golang-troubleshooting`.

### R1 (low) — singleflight: отмена контекста лидера влияет на ведомых

- **Файл:** [services/idm/internal/usecase/usecase.go:51](../../services/idm/internal/usecase/usecase.go#L51)
- **Суть:** `group.Do` исполняет чтение БД в контексте первого вызывающего
  («лидера»); при отмене его `ctx` все ведомые на том же ключе получат ту же
  ошибку. Это известный caveat `singleflight`, не баг, но под нагрузкой с частыми
  отменами может давать всплески ложных отказов (fail-closed → лишние deny).
- **Правило/скилл:** `golang-safety`/`golang-concurrency` — учитывать общий
  контекст лидера в `singleflight`.
- **Рекомендация (опционально):** при необходимости — исполнять загрузку в
  `context.WithoutCancel` лидера или применять паттерн с per-call отменой поверх
  shared-результата. Для MVP-масштаба влияние незначимо → low.

Прочее (проверено, чисто): числовые конверсии `int32(...)` ограничены до
конверсии (`> math.MaxInt32` в `projects`/`devinfra-worker` main; `ParseInt(...,
10, 32)` + `n<0` в `gateway.parsePageSize`) — overflow невозможен. `panic` в
рантайме нет; recovery-перехватчики есть и в gRPC, и в HTTP. `defer tx.Rollback`
идемпотентен. Конкурентный доступ к map (`gitLabHTTP.groupIDs`) под `sync.Mutex`.

## 5. testing

Скиллы: `golang-testing`, `golang-stretchr-testify`, `golang-benchmark`.

**Находок нет.** 221 тест-функция; `t.Parallel()` повсеместно (линтер
`paralleltest` включён). Temporal testsuite — для всех четырёх workflow
(`provisioning`/`changeowners`/`transfer`/`decommission`) и activities. goleak — в
пакетах с горутинами/HTTP (`httpserver`, `cache`, `identity`, `integrations`,
`usecase`). Интеграционные тесты на Postgres под тегом `integration` (отделены от
дефолтного прогона), периметр-тесты в `tests/e2e`. In-memory стабы держат домен
тестируемым без БД.

## 6. performance

Скиллы: `golang-performance`, `golang-benchmark`.

### P1 (low) — последовательные HTTP-вызовы при резолве набора субъектов

- **Файл:** [services/idm/internal/identity/keycloak.go:142-161](../../services/idm/internal/identity/keycloak.go#L142-L161)
- **Суть:** `Resolve` делает по одному `GET /users/{id}` на каждый субъект
  последовательно → латентность O(N) round-trips. Для страницы владельцев это
  заметно при росте N.
- **Правило/скилл:** `golang-performance` — параллелизовать независимый I/O с
  ограничением степени параллелизма.
- **Рекомендация:** `errgroup.Group` c `SetLimit(k)` для ограниченно-параллельного
  резолва (порядок результата восстанавливать по индексу). Бенчмарк не требуется —
  выигрыш по латентности очевиден; для MVP-масштаба (малые наборы владельцев) —
  low-приоритет. Кэш идентичностей (`identity/cache.go`) уже снимает повторные
  обращения в пределах TTL.

Прочее: горячие пути каталога аккуратны — батч-загрузка владельцев без N+1
(`loadOwners` через `ANY($1)`), prealloc срезов (`make([]T, 0, n)`), keyset-
пагинация. Преждевременной оптимизации не требуется.

## 7. database

Скиллы: `golang-database`.

**Находок нет.** Все запросы pgx параметризованы (`$1..$n`) — SQL-инъекций нет.
Переходы статуса — строго **guarded-CAS** (`UPDATE ... WHERE id=$id AND
status=$expected`, `RowsAffected()==0` → `ErrConflict`/`ErrPrecondition` через
перечитывание лишь для классификации), не check-then-act
([repository.go:292-437](../../services/projects/internal/repository/repository.go#L292-L437)).
Многошаговые записи — в транзакции (`InTx`, `Querier` работает и на пуле, и на
`pgx.Tx`); публикация событий/статусов — после commit (workflow стартует после
успешной вставки). `rows` закрываются (линтер `sqlclosecheck`), `rows.Err()`
проверяется; на одном соединении не держатся два активных запроса
([repository.go:584](../../services/projects/internal/repository/repository.go#L584)).
Пул конфигурируется явно (`pkg/db`), `context` пробрасывается везде. NULLable
`decommissioned_at` сканируется в `*time.Time`.

## 8. security

Скиллы: `golang-security`, `golang-lint`, `golang-safety`.

**Находок нет (соответствие fail-closed-стандартам подтверждено).**
- JWT строго: `WithValidMethods`/`WithExpirationRequired` + при заданных —
  `WithIssuer`/`WithAudience`; пустой `JWKS_URL` при включённой проверке → ошибка
  конструктора, `MustVerifierFromEnv` → `os.Exit(1)`; bypass только явным
  `AUTH_DISABLED=true` ([pkg/auth/auth.go:64-137](../../pkg/auth/auth.go#L64-L137)).
- admin-key — `subtle.ConstantTimeCompare` ([auth.go:167](../../pkg/auth/auth.go#L167)).
- **SSRF-guard на всех исходящих к GitLab/Vault/Harbor/Keycloak:**
  `ssrf.ValidateURL` на конфигурации + `ssrf.GuardedDialContext`
  (повторная проверка резолвнутого IP против TOCTOU/DNS-rebinding) на dial
  ([pkg/ssrf/ssrf.go](../../pkg/ssrf/ssrf.go),
  [integrations/http.go:72-85](../../services/devinfra-worker/internal/integrations/http.go#L72-L85),
  [identity/keycloak.go:84-93](../../services/idm/internal/identity/keycloak.go#L84-L93)).
- Секреты/токены не логируются; заголовки аутентификации изолированы по `doer` на
  интеграцию (не протекают между GitLab/Vault/Harbor); ошибки наружу — без сырья.
- RBAC fail-closed: недоступность/ошибка IDM → `PermissionDenied`
  ([grpcapi/server.go:219-237](../../services/projects/internal/grpcapi/server.go#L219-L237));
  кэш-промах/ошибка кэша → чтение БД, не молчаливый allow.
- `gosec` (вкл. G101) проходит; ложные срабатывания на именах заголовков/activity
  подавлены точечными `//nolint:gosec` с обоснованием (см. memory
  `vault-real-api-divergences`).

## 9. style-idiomatic

Скиллы: `golang-code-style`, `golang-naming`, `golang-modernize`,
`golang-design-patterns`, `golang-documentation`.

### ST1 (low) — опечатка со смешением кириллицы и латиницы в комментарии

- **Файл:** [services/devinfra-worker/internal/activities/activities.go:43](../../services/devinfra-worker/internal/activities/activities.go#L43)
- **Суть:** `эксплуatации` — латинская `a` внутри кириллического слова
  («эксплуатации»). Единственное такое место в репозитории.
- **Правило/скилл:** `golang-documentation` — корректность комментариев
  (комментарии в проекте — только на русском).
- **Рекомендация:** исправить на «эксплуатации». **Чинится в этом change**
  (механическая правка).

Прочее (проверено, чисто): нейминг идиоматичен (MixedCaps, `ErrXxx`,
конструкторы `New*`, функциональные опции `With*`); package-doc на русском во всех
пакетах (линтер `revive/package-comments`); англоязычных комментариев нет (только
технические термины-имена внутри русских фраз); код современен под Go 1.26
(`slices`/`maps`, `errors.Join`-готовность, дженерик-`Querier`).

## 10. dependencies-ci

Скиллы: `golang-dependency-management`, `golang-continuous-integration`.

### D1 (medium) — `govulncheck@latest` в CI: невоспроизводимый гейт безопасности

- **Файл:** [.github/workflows/ci.yml:105](../../.github/workflows/ci.yml#L105)
  (`go install golang.org/x/vuln/cmd/govulncheck@latest`)
- **Суть:** блокирующий security-гейт устанавливается из `@latest` → версия
  инструмента не зафиксирована, прогон невоспроизводим, апстрим-изменение
  поведения может неожиданно «покраснить»/«позеленить» CI. При этом проект уже
  держит пинованные бинари в `tools/bin` (паттерн `make tools`).
- **Правило/скилл:** `golang-continuous-integration` — пинить версии инструментов
  качества (как уже сделано для `golangci-lint` v2.12.2).
- **Рекомендация:** пинить govulncheck явной версией (`@vX.Y.Z`) или добавить его в
  пинованный набор `tools` и запускать из `tools/bin`. **Вынести в follow-up**
  (изменение CI-джобы, требует проверки прогона) — см. бэклог.

### D2 (low) — Dependabot без группировки gomod-обновлений

- **Файл:** [.github/dependabot.yml](../../.github/dependabot.yml)
- **Суть:** 7 отдельных gomod-экосистем (по модулю) без `groups:` — общие
  зависимости обновляются разрозненными PR (ср. #75/#76), что усиливает
  кросс-модульную хрупкость `go mod tidy` (bump общей dep в одном модуле ломает
  `tidy`/`govulncheck` в зависимом `devinfra-worker` → `replace ../projects`; см.
  memory `gowork-cross-module-tidy`).
- **Правило/скилл:** `golang-dependency-management` — группировать обновления
  общих зависимостей.
- **Рекомендация:** добавить `groups:` (например, единая группа для общих
  runtime-зависимостей) и/или ввести `make tidy-all` (по модулю `GOWORK=off` в
  порядке зависимостей: `pkg` → `projects` → `devinfra-worker` → остальные), чтобы
  bump общей dep тидился консистентно в одном PR. **Вынести в follow-up.**

Прочее (проверено, чисто): версии общих зависимостей **согласованы** между
модулями (pgx v5.10.0, grpc v1.81.1, temporal sdk v1.45.0, go-redis v9.21.0,
jwt/v5 v5.3.1); все 8 модулей на `go 1.26.4` без toolchain-дрейфа; Dependabot
покрывает gomod (все модули), github-actions и npm; `tidy --check` и codegen-check
в CI блокирующие.

---

## Бэклог ремедиаций

### (а) Чинить в этом change — низкорисковые механические правки

| ID | Правка | Файл | Риск |
| --- | --- | --- | --- |
| ST1 | Опечатка `эксплуatации` → `эксплуатации` | activities.go:43 | нулевой (комментарий) |

### (б) Follow-up changes по доменам (вне этого PR)

| ID | Домен | Предлагаемый change | Суть |
| --- | --- | --- | --- |
| D1 | dependencies-ci | `ci-pin-govulncheck` | Пинить govulncheck (версия или `tools/bin`) |
| D2 | dependencies-ci | `dependabot-group-gomod` + `make tidy-all` | Группировка gomod-обновлений и согласованный кросс-модульный tidy |
| C1 | concurrency | `idm-keycloak-token-singleflight` | Убрать удержание мьютекса на время сетевого запроса токена |
| P1 | performance | `idm-keycloak-parallel-resolve` | Ограниченно-параллельный резолв субъектов (`errgroup.SetLimit`) |
| R1 | reliability | (опционально, вместе с C1) | Учесть leader-ctx caveat `singleflight` |

Крупных рефакторов и пересмотра ADR не требуется. Значимых архитектурных
расхождений с ADR 0001..0023 не выявлено — отдельный ADR не нужен.
