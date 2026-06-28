# ci-pipeline Specification

## Purpose
TBD - created by archiving change foundation-and-pkg. Update Purpose after archive.
## Requirements
### Requirement: Блокирующий CI с матрицей по модулям

CI (GitHub Actions) ДОЛЖЕН (MUST) запускаться с первого коммита и прогонять по матрице модулей `go.work`: `go test -race -shuffle=on`, проверку `go mod tidy` через `git diff --exit-code`, `golangci-lint`. Падение любого шага ДОЛЖНО блокировать merge.

#### Scenario: Тесты с race и shuffle на каждом модуле

- **WHEN** запускается CI на pull request
- **THEN** для каждого модуля матрицы выполняется `go test -race -shuffle=on ./...`, и красный результат блокирует merge

#### Scenario: Несинхронизированный go.mod валит сборку

- **WHEN** в PR `go.mod`/`go.sum` не приведены к `go mod tidy`
- **THEN** шаг `go mod tidy && git diff --exit-code` завершается ненулевым кодом и CI краснеет

### Requirement: Блокирующий govulncheck

CI ДОЛЖЕН (MUST) прогонять `govulncheck` как **блокирующий** шаг (особенно для auth-зависимостей при работе с Keycloak/JWT); найденная эксплуатируемая уязвимость ДОЛЖНА останавливать pipeline.

#### Scenario: Уязвимая зависимость блокирует merge

- **WHEN** `govulncheck` находит вызываемую уязвимость в графе зависимостей
- **THEN** шаг завершается с ошибкой и merge заблокирован

### Requirement: Конфигурация линтеров

В корне ДОЛЖЕН (MUST) лежать `.golangci.yml`, включающий как минимум: errcheck, govet, staticcheck, revive, gosec, bodyclose, sqlclosecheck, nilerr, errname, paralleltest, package-comments.

#### Scenario: Включён обязательный набор линтеров

- **WHEN** читается `.golangci.yml`
- **THEN** в списке включённых линтеров присутствуют все перечисленные

### Requirement: Integration-джоб

CI ДОЛЖЕН (MUST) содержать отдельный job для тестов с тегом `integration`, изолированный от дефолтного быстрого прогона.

#### Scenario: Integration-тесты в отдельном job

- **WHEN** запускается CI
- **THEN** тесты с build-тегом `integration` выполняются в отдельном job, а дефолтный прогон их не запускает

### Requirement: Автообновление зависимостей и Dockerfile на сервис

Репозиторий ДОЛЖЕН (MUST) содержать конфигурацию Dependabot для Go-модулей и GitHub Actions, а также по `Dockerfile` на каждый сервис (для Trivy/SBOM).

#### Scenario: Dependabot настроен на модули

- **WHEN** проверяется `.github/dependabot.yml`
- **THEN** в нём заданы экосистемы `gomod` для каждого модуля и `github-actions`

#### Scenario: У каждого сервиса есть Dockerfile

- **WHEN** перечисляются `services/*`
- **THEN** для каждого сервиса существует `Dockerfile`

### Requirement: Блокирующая валидация Helm-чартов

CI SHALL содержать блокирующую джобу валидации чартов деплоя: `helm lint`,
`helm template` для каждого окружения (`values-local`/`values-prod`) с прогоном
рендера через `kubeconform` (включая схемы Istio CRD) и `istioctl analyze`.
Версии `helm`, `kubeconform`, `istioctl` и набора Istio CRD-схем SHALL быть
пинованы (не `latest`), как у других блокирующих гейтов.

#### Scenario: невалидный чарт роняет CI

- **WHEN** изменение вносит ошибку в шаблон чарта или Istio-ресурс
- **THEN** джоба валидации Helm падает и блокирует merge

#### Scenario: оба окружения проверяются пинованными инструментами

- **WHEN** запускается джоба валидации Helm
- **THEN** local- и prod-overlay рендерятся и проходят `kubeconform` (со схемами
  Istio CRD) и `istioctl analyze` на закреплённых версиях инструментов
