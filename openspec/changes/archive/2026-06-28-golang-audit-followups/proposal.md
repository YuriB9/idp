## Why

Аудит Go-кода (`docs/audits/2026-06-golang-audit.md`, change `golang-code-audit`,
PR #78) выявил 6 находок без блокеров/high. Тривиальная правка (ST1) уже
применена в аудит-PR; оставшиеся 5 находок (2 medium + 3 low) требуют отдельных
правок кода/конфигов и собраны в один follow-up, чтобы закрыть бэклог одним
проходом без раздувания числа PR.

## What Changes

- **D1 (medium, dependencies-ci):** в CI заменить `go install
  govulncheck@latest` на воспроизводимый прогон — пинованная версия (`@vX.Y.Z`)
  или запуск из пинованного `tools/bin` (паттерн `make tools`). Гейт
  безопасности станет детерминированным.
- **D2 (low, dependencies-ci):** в `.github/dependabot.yml` добавить `groups:`
  для gomod-обновлений общих зависимостей; добавить `make tidy-all` (по модулю
  `GOWORK=off` в порядке зависимостей), чтобы bump общей dep тидился консистентно
  и не повторял падение PR #75.
- **C1 (medium, concurrency):** в `services/idm/internal/identity/keycloak.go`
  убрать удержание мьютекса на время сетевого запроса токена сервис-аккаунта
  (double-checked под локом / `singleflight`), чтобы конкурентные обращения к
  справочнику не сериализовались за одним in-flight запросом.
- **P1 (low, performance):** в том же файле `Resolve` сделать
  ограниченно-параллельным (`errgroup.Group` + `SetLimit`) вместо N
  последовательных round-trips; порядок результата восстанавливать по индексу.
- **R1 (low, reliability):** учесть caveat `singleflight` (контекст лидера) при
  правке C1 — фоновую загрузку при необходимости вести в `context.WithoutCancel`.
- Поведение наружу (контракты gRPC/OpenAPI, коды ответов, fail-closed-семантика)
  **не меняется** — это внутренние улучшения конкурентности/производительности и
  CI-конфиги.

## Capabilities

### New Capabilities
- `golang-audit-remediations`: критерии приёмки ремедиаций C1/D1/P1/R1/D2 —
  воспроизводимый govulncheck-гейт, группировка Dependabot + согласованный
  кросс-модульный tidy, отсутствие удержания мьютекса на время I/O при выдаче
  токена, ограниченно-параллельный резолв, сохранение fail-closed и зелёного CI
  без изменения внешнего поведения.

### Modified Capabilities
<!-- Нет: внешнее поведение и требования существующих capability не меняются;
     правки внутренние (конкурентность/производительность) и инфраструктурные (CI). -->

## Impact

- **Код:** `services/idm/internal/identity/keycloak.go` (C1/P1/R1) — возможна
  новая зависимость `golang.org/x/sync/errgroup` в модуле `services/idm`
  (`golang.org/x/sync` уже используется там для `singleflight`).
- **Конфиги:** `.github/workflows/ci.yml` (D1), `.github/dependabot.yml` (D2),
  `Makefile` (`tidy-all`, при необходимости пин govulncheck в `tools`).
- **Затрагиваемые модули:** `services/idm`; CI-конфиги — общерепозиторные.
  gRPC/Temporal-границы и контракты не изменяются.
- **CI:** все Go-джобы остаются зелёными; при добавлении зависимости в
  `services/idm` — `go mod tidy` (`GOWORK=off`) согласован по затронутым модулям.
- **Соответствие docs/ADR:** правки соответствуют инженерным стандартам
  (`docs/IDP_MVP_План.md` BLOCK 0); пересмотра ADR не требуется.
