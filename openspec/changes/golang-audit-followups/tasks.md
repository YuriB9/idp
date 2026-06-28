## 1. Подготовка

- [x] 1.1 Создать git-ветку `change/golang-audit-followups` от master (прямые коммиты в master запрещены)
- [x] 1.2 Сверить план с отчётом `docs/audits/2026-06-golang-audit.md` (находки C1/P1/R1/D1/D2) и стандартами `docs/IDP_MVP_План.md` BLOCK 0
- [x] 1.3 Зафиксировать baseline: `golangci-lint` и `govulncheck` (`GOWORK=off`) по затронутым модулям зелёные до правок

## 2. C1/R1 — выдача токена Keycloak без удержания мьютекса на время I/O

- [x] 2.1 В `services/idm/internal/identity/keycloak.go` вынести HTTP-запрос токена за пределы критической секции (под `mu` — только чтение/запись `token`/`exp`)
- [x] 2.2 Дедуплицировать одновременные запросы токена через `singleflight` (или double-checked); учесть caveat контекста лидера (R1, при необходимости `context.WithoutCancel`)
- [x] 2.3 Секрет сервис-аккаунта не логировать; кэш токена в памяти сохранить
- [x] 2.4 Тест: отсутствие сериализации/гонок при конкурентной выдаче токена (`go test -race`)

## 3. P1 — ограниченно-параллельный Resolve

- [x] 3.1 Переписать `Resolve` на `errgroup.Group` + `SetLimit(k)`; результат писать в предвыделенный срез по индексу субъекта
- [x] 3.2 Сохранить порядок результата и семантику «осиротевшего» субъекта (`Found=false`) и `ErrUnavailable`
- [x] 3.3 Тест: параллельный резолв, порядок, частичное отсутствие субъектов
- [x] 3.4 `go mod tidy` (`GOWORK=off`) в `services/idm` (errgroup из `golang.org/x/sync`) — без diff

## 4. D1 — воспроизводимый govulncheck-гейт

- [x] 4.1 Заменить `go install govulncheck@latest` в `.github/workflows/ci.yml` на пин (версия `@vX.Y.Z` или запуск из `tools/bin`)
- [x] 4.2 При выборе `tools/bin` — добавить govulncheck в `make tools` и шаг сборки инструментов в джобе; гейт остаётся блокирующим `GOWORK=off`

## 5. D2 — Dependabot groups и согласованный tidy

- [x] 5.1 Добавить `groups:` для gomod-обновлений общих зависимостей в `.github/dependabot.yml`
- [x] 5.2 Добавить цель `make tidy-all` (`go mod tidy` `GOWORK=off` по модулям в порядке зависимостей)
- [x] 5.3 Прогнать `make tidy-all` — без diff во всех модулях, включая `devinfra-worker`

## 6. Верификация CI

- [x] 6.1 `go test -race -shuffle=on ./...` по затронутым модулям — зелёное
- [x] 6.2 `golangci-lint` v2.12.2 по затронутым модулям — зелёное
- [x] 6.3 `govulncheck ./...` (`GOWORK=off`) по затронутым модулям — зелёное
- [x] 6.4 `go mod tidy` + `git diff --exit-code` (`GOWORK=off`) по затронутым модулям — без diff
- [x] 6.5 integration-тесты (Postgres) — зелёные, если затронуты (для idm-правок обычно не требуются)
- [ ] 6.6 Открыть PR из `change/golang-audit-followups` с зелёным CI; после merge — отдельный sync+archive PR через `/opsx:archive`
