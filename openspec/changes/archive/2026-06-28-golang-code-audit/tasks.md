## 1. Подготовка и сверка

- [x] 1.1 Создать git-ветку `change/golang-code-audit` от master (прямые коммиты в master запрещены)
- [x] 1.2 Сверить объём с `docs/IDP_MVP_План.md` (BLOCK 0, инженерные стандарты) и ADR 0001..0023; зафиксировать критерии аудита
- [x] 1.3 Учесть memory: `vault-real-api-divergences`, `e2e-real-oidc-wiring`, `e2e-change-owners-single-shot`, `gowork-cross-module-tidy`
- [x] 1.4 Создать каркас отчёта `docs/audits/2026-06-golang-audit.md` (сводная таблица severity + критерии severity + 10 пустых доменных разделов)

## 2. Baseline существующего тулинга (не дублировать руками)

- [x] 2.1 Прочитать `.golangci.yml` и зафиксировать включённые линтеры (что уже ловится)
- [x] 2.2 Прочитать `.github/workflows/ci.yml` — состав джоб (test -race -shuffle, lint, govulncheck, tidy, integration)
- [x] 2.3 Прогнать локально `golangci-lint` и `govulncheck` (`GOWORK=off`) по модулям, зафиксировать текущий baseline-вывод
- [x] 2.4 Сверить go.work/go.mod каждого модуля и `replace`-директивы (особенно `devinfra-worker` → `../projects`)

## 3. Аудит по доменам (скилл → чек-лист → находки в отчёт)

- [x] 3.1 structure-layers (`golang-project-layout`, `golang-structs-interfaces`): границы пакетов/модулей, cmd/internal, accept interfaces/return structs, утечки слоёв, циклы
- [x] 3.2 concurrency (`golang-concurrency`, `golang-context`): goroutine-leaks, ownership каналов, select, errgroup/singleflight, отмена/таймауты, проброс context
- [x] 3.3 error-handling (`golang-error-handling`): `%w`/`errors.Is/As/Join`, sentinel, single-handling-rule, slog ключ `err`, неутечка ошибок наружу периметра
- [x] 3.4 reliability-panics (`golang-safety`, `golang-troubleshooting`): nil-паники, append-aliasing, конкурентные map, overflow конверсий, defer в циклах, zero-value, recover в воркерах
- [x] 3.5 testing (`golang-testing`, `golang-stretchr-testify`, `golang-benchmark`): покрытие критичных путей, table-driven+`t.Parallel()`, goleak, Temporal testsuite, флейки, integration vs дефолтный прогон
- [x] 3.6 performance (`golang-performance`, `golang-benchmark`): аллокации на горячих путях, prealloc, лишние копии, пулинг; предложить бенчмарки при быстром выигрыше
- [x] 3.7 database (`golang-database`): pgx-параметризация, скан NULLable, транзакции/isolation, `SELECT FOR UPDATE` vs guarded-CAS, пул соединений, проброс context, миграции goose
- [x] 3.8 security (`golang-security`, `golang-lint`, `golang-safety`): fail-closed, SSRF-guard на всех клиентах GitLab/Vault/Harbor, крипто/секреты, безопасность логов
- [x] 3.9 style-idiomatic (`golang-code-style`, `golang-naming`, `golang-modernize`, `golang-design-patterns`, `golang-documentation`): идиоматичность, нейминг, паттерны под Go 1.26, godoc, отсутствие англоязычных комментариев
- [x] 3.10 dependencies-ci (`golang-dependency-management`, `golang-continuous-integration`): MVS-конфликты, устаревшие/уязвимые зависимости, согласованность версий go.work, хрупкость кросс-модульного tidy, полнота CI-гейтов, Dependabot-группировка

## 4. Оформление отчёта

- [x] 4.1 Заполнить каждую находку: severity, `file:line`, описание, ссылка на правило/скилл, рекомендация
- [x] 4.2 Заполнить сводную таблицу количества находок по severity (blocker/high/medium/low)
- [x] 4.3 Пометить домены без находок явной пометкой «находок нет»
- [x] 4.4 Сформировать бэклог: категория (а) «чинить в этом change» (механические правки) и категория (б) follow-up changes с именем+доменом

## 5. Применение низкорисковых правок (категория «сейчас», опционально)

- [x] 5.1 Применить только механические правки (modernize/style/очевидный safety), не меняющие поведение и контракты
- [x] 5.2 Все комментарии в правках — только на русском языке
- [x] 5.3 При касании общих зависимостей — `go mod tidy` (`GOWORK=off`) во всех затронутых модулях, включая `devinfra-worker` (не повторить PR #75)

## 6. Верификация CI

- [x] 6.1 `go test -race -shuffle=on ./...` по затронутым модулям — зелёное
- [x] 6.2 `golangci-lint` v2.12.2 по затронутым модулям — зелёное
- [x] 6.3 `govulncheck ./...` (`GOWORK=off`) по затронутым модулям — зелёное
- [x] 6.4 `go mod tidy` + `git diff --exit-code` (`GOWORK=off`) по затронутым модулям — без diff
- [x] 6.5 integration-тесты (Postgres) — зелёные, если затронуты пути repository/perimeter
- [ ] 6.6 Открыть PR из `change/golang-code-audit` с зелёным CI; после merge — отдельный sync+archive PR через `/opsx:archive`
