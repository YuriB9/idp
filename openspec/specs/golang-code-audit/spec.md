# golang-code-audit Specification

## Purpose
TBD - created by archiving change golang-code-audit. Update Purpose after archive.
## Requirements
### Requirement: Отчёт аудита покрывает все 10 доменов

Отчёт аудита Go-кода ДОЛЖЕН (MUST) быть единым документом
`docs/audits/2026-06-golang-audit.md` и SHALL содержать ровно 10 разделов,
по одному на каждый домен аудита: structure-layers, concurrency,
error-handling, reliability-panics, testing, performance, database, security,
style-idiomatic, dependencies-ci. Каждый раздел SHALL явно называть применённый
доменный скилл (`samber/cc-skills-golang@*`).

#### Scenario: Все домены присутствуют

- **GIVEN** сформированный отчёт `docs/audits/2026-06-golang-audit.md`
- **WHEN** проверяется его структура
- **THEN** в нём есть раздел для каждого из 10 доменов
- **AND** каждый раздел ссылается на имя применённого скилла

#### Scenario: Домен без находок помечен явно

- **WHEN** в домене не обнаружено находок
- **THEN** раздел домена ДОЛЖЕН (MUST) содержать явную пометку «находок нет»
  вместо пустого раздела

### Requirement: Каждая находка прослеживаема и приоритизирована

Каждая находка в отчёте ДОЛЖНА (MUST) содержать: `severity` из множества
{blocker, high, medium, low}; локацию `file:line`; краткое описание;
ссылку на правило/скилл, обосновывающее находку; рекомендацию по ремедиации.
Отчёт SHALL начинаться со сводной таблицы количества находок по severity.

#### Scenario: Поля находки заполнены

- **GIVEN** любая находка в отчёте
- **WHEN** она проверяется на полноту
- **THEN** у неё указаны severity, file:line, описание, ссылка на правило/скилл
  и рекомендация

#### Scenario: Сводная таблица severity вверху

- **WHEN** открывается отчёт
- **THEN** до доменных разделов присутствует сводная таблица с количеством
  находок по каждому уровню severity

### Requirement: Бэклог ремедиаций разделён на «сейчас» и «follow-up»

Отчёт ДОЛЖЕН (MUST) включать приоритизированный бэклог ремедиаций, явно
разделённый на две категории: (а) низкорисковые механические правки, применяемые
в этом change без изменения поведения; (б) крупные рефакторы, выносимые в
отдельные follow-up changes по доменам. Категория (б) SHALL называть
предлагаемое имя follow-up change и его домен.

#### Scenario: Каждый пункт бэклога отнесён к категории

- **GIVEN** бэклог ремедиаций
- **WHEN** проверяется любой его пункт
- **THEN** пункт отнесён либо к «чинить в этом change», либо к конкретному
  follow-up change с указанием домена

#### Scenario: Правки этого change низкорисковые

- **WHEN** пункт отнесён к «чинить в этом change»
- **THEN** он является механической правкой (modernize/style/очевидный safety)
  и не меняет поведение сервисов и контракты

### Requirement: Границы аудита соблюдены

Аудит SHALL охватывать только Go-модули в границах: `pkg`,
`services/{gateway,idm,projects,devinfra-worker}`, `tests/e2e`, `tools`.
Аудит ДОЛЖЕН (MUST) НЕ предлагать изменения фронтенда `./web`, контрактов
(`.proto`/OpenAPI), ручную правку сгенерированного кода и пересмотр принятых
ADR 0001..0023.

#### Scenario: Находка вне границ не попадает в бэклог правок

- **WHEN** потенциальная проблема относится к `./web`, контрактам или
  сгенерированному коду
- **THEN** она не включается в бэклог правок этого change

#### Scenario: Архитектурное расхождение выносится в ADR

- **WHEN** аудит обнаруживает значимое расхождение уровня архитектуры с ADR
- **THEN** оно фиксируется как рекомендация завести отдельный ADR, а не правка в
  этом change

### Requirement: Аудит сверяет код с инженерными стандартами проекта

Аудит ДОЛЖЕН (MUST) проверять соответствие кода зафиксированным fail-closed и
надёжностным стандартам: пустой `JWKS_URL` → `os.Exit(1)`; строгая валидация JWT
(audience/issuer/validMethods/expirationRequired); admin-key через
`subtle.ConstantTimeCompare`; SSRF-guard (`ValidateURL` + `GuardedDialContext`)
на всех исходящих к GitLab/Vault/Harbor; неотдача `err.Error()` клиенту;
guarded-CAS переходов статуса (`UPDATE ... WHERE status=$expected`,
`RowsAffected==0` → 409); публикация событий после commit; единый ключ slog
`err`. Расхождения с этими стандартами SHALL фиксироваться как находки.

#### Scenario: Нарушение fail-closed зафиксировано

- **WHEN** обнаружен исходящий клиент к GitLab/Vault/Harbor без SSRF-guard
- **THEN** в домене security создаётся находка с severity не ниже high и ссылкой
  на стандарт

#### Scenario: Нарушение guarded-CAS зафиксировано

- **WHEN** обнаружен переход статуса сервиса через check-then-act вместо
  guarded-CAS
- **THEN** в домене database создаётся находка с рекомендацией перейти на
  `UPDATE ... WHERE status=$expected`

### Requirement: CI остаётся зелёным после применённых правок

Если в рамках change применяются правки кода/конфигов, они ДОЛЖНЫ (MUST)
оставлять все Go-джобы CI зелёными: `go test -race -shuffle=on`,
`golangci-lint` v2.12.2, `govulncheck` (`GOWORK=off`), `go mod tidy --check`
(`GOWORK=off` по модулю), integration. Кросс-модульный `tidy` SHALL оставаться
согласованным, чтобы не повторить падение PR #75.

#### Scenario: Применённая правка проходит CI

- **GIVEN** правка из категории «чинить в этом change»
- **WHEN** запускаются Go-джобы CI
- **THEN** все они зелёные

#### Scenario: Кросс-модульный tidy согласован

- **WHEN** правка затрагивает зависимость, общую для модулей go.work
- **THEN** `go mod tidy` (`GOWORK=off`) прогоняется в каждом затронутом модуле,
  включая зависимый `devinfra-worker`, без diff

