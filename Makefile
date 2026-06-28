# Makefile платформы IDP. Инструменты кодогена/линтинга живут в ./tools
# (вне go.work) и запускаются с GOWORK=off, чтобы их зависимости не протекали
# в графы сервисов (docs/adr/0006).

MODULES := pkg services/gateway services/idm services/projects services/devinfra-worker tests/e2e
# TIDY_ORDER — порядок tidy ПО ЗАВИСИМОСТЯМ go.work: сначала pkg (общий), затем
# projects, затем зависящий от него devinfra-worker (replace ../projects), потом
# остальные. Bump общей зависимости в одном модуле ломает `go mod tidy` в
# зависимом, если тидить вразнобой (см. PR #75) — единый прогон в этом порядке
# держит модули согласованными.
TIDY_ORDER := pkg services/projects services/devinfra-worker services/gateway services/idm tests/e2e tools
TOOLS_BIN := $(CURDIR)/tools/bin

# DSN каталога проектов для миграций; переопределяется из окружения.
PROJECTS_DSN ?= postgres://projects:projects@localhost:5432/projects?sslmode=disable
PROJECTS_MIGRATIONS := $(CURDIR)/services/projects/migrations
# DSN сервиса IDM для миграций (роли/права RBAC); переопределяется из окружения.
IDM_DSN ?= postgres://idm:idm@localhost:5433/idm?sslmode=disable
IDM_MIGRATIONS := $(CURDIR)/services/idm/migrations
# GOOSE_CMD — команда goose (up|down|status|...).
GOOSE_CMD ?= up

# Конформанс-тестирование периметра Schemathesis (закреплённый Docker-образ).
# GATEWAY_BASE_URL — база живого gateway с префиксом /api (compose публикует :8081).
SCHEMATHESIS_IMAGE ?= schemathesis/schemathesis:4.21.10
GATEWAY_BASE_URL ?= http://localhost:8081/api
# CONF_EXAMPLES — число генерируемых примеров на операцию (баланс охвата/времени).
CONF_EXAMPLES ?= 25

.PHONY: tools proto openapi gen test lint lint-openapi tidy tidy-all tidy-check migrate-projects migrate-idm conformance

## tools: собрать пинованные инструменты кодогена в tools/bin
tools:
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/buf github.com/bufbuild/buf/cmd/buf
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/goose github.com/pressly/goose/v3/cmd/goose

## migrate-projects: применить миграции каталога проектов (goose, GOWORK=off).
## Использование: make migrate-projects [GOOSE_CMD=up|down|status] [PROJECTS_DSN=...]
migrate-projects: tools
	$(TOOLS_BIN)/goose -dir $(PROJECTS_MIGRATIONS) postgres "$(PROJECTS_DSN)" $(GOOSE_CMD)

## migrate-idm: применить миграции RBAC сервиса IDM (goose, GOWORK=off).
## Использование: make migrate-idm [GOOSE_CMD=up|down|status] [IDM_DSN=...]
migrate-idm: tools
	$(TOOLS_BIN)/goose -dir $(IDM_MIGRATIONS) postgres "$(IDM_DSN)" $(GOOSE_CMD)

## proto: сгенерировать Go-стабы из .proto
proto: tools
	cd proto && PATH="$(TOOLS_BIN):$$PATH" $(TOOLS_BIN)/buf lint
	cd proto && PATH="$(TOOLS_BIN):$$PATH" $(TOOLS_BIN)/buf generate

## openapi: сгенерировать TS-клиент и zod-схемы из OpenAPI
openapi:
	cd web && npm run gen

## lint-openapi: линтинг спецификации периметра Spectral (ADR-0009).
## Требует установленных зависимостей web (npm ci). Падает на error-правилах:
## недокументированный метод (description/operationId), битая структура OpenAPI.
lint-openapi:
	cd web && npm run lint:openapi

## gen: весь кодоген
gen: proto openapi

## test: тесты всех модулей с race+shuffle
test:
	@for m in $(MODULES); do echo ">> test $$m"; (cd $$m && go test -race -shuffle=on ./...) || exit 1; done

## conformance: конформанс-тест периметра Schemathesis против ЖИВОГО стенда.
## Проверяет, что РАНТАЙМ gateway соответствует openapi/openapi.yaml (схемы
## ответов, коды, content-type) — закрывает разрыв «доки vs реальность», который
## не ловят codegen-check и Spectral. Локальный ручной прогон (не в CI пока).
##
## Предусловие: поднят стенд (docker compose ... up), gateway на :8081,
## AUTH_DISABLED=true + засеян demo-user. На старте охват — только GET-ручки
## (мутации create/owners/decommission/transfer/assign/revoke исключены, чтобы не
## трогать состояние и Temporal); /healthz исключён (живёт в корне, не под /api).
##
## CONF_CHECKS — НАБОР ПРОВЕРОК. По умолчанию — только «доки vs реальность»:
## not_a_server_error (нет недокументированных 5xx), status_code_conformance
## (код ответа описан), content_type_conformance и response_schema_conformance
## (тело по схеме). Намеренно ИСКЛЮЧЕНЫ проверки строгости ввода/методов, которые
## на нашем периметре дают лишь заведомо допустимый шум, не баги:
##   - unsupported_method: chi отдаёт 405 без Allow на НЕстандартный метод (QUERY)
##     — ограничение фреймворка для экзотических методов, реальные клиенты их не шлют;
##   - positive_data_acceptance: непрозрачный курсор page_token (любая строка
##     «валидна» по схеме, но чужой/битый курсор корректно отвергается 400);
##   - negative_data_rejection: приём неизвестного query-параметра (штатное REST).
## Полный набор: make conformance CONF_CHECKS=all
## Запуск: make conformance [GATEWAY_BASE_URL=...] [CONF_EXAMPLES=N] [CONF_CHECKS=...]
CONF_CHECKS ?= not_a_server_error,status_code_conformance,content_type_conformance,response_schema_conformance
conformance:
	docker run --rm --network host \
		-v "$(CURDIR)/openapi:/spec:ro" \
		$(SCHEMATHESIS_IMAGE) run /spec/openapi.yaml \
		--url "$(GATEWAY_BASE_URL)" \
		--include-method GET \
		--exclude-path /healthz \
		--checks "$(CONF_CHECKS)" \
		--max-examples=$(CONF_EXAMPLES)

## tidy: go mod tidy по всем модулям (псевдоним tidy-all)
tidy: tidy-all

## tidy-all: go mod tidy (GOWORK=off) по модулям в порядке зависимостей
## (TIDY_ORDER). Единый прогон после bump общей зависимости держит зависимые
## модули (devinfra-worker → projects) согласованными, не повторяя падение PR #75.
tidy-all:
	@for m in $(TIDY_ORDER); do echo ">> tidy $$m"; (cd $$m && GOWORK=off go mod tidy) || exit 1; done

## tidy-check: проверить, что go.mod/go.sum синхронизированы
tidy-check: tidy-all
	git diff --exit-code

## lint: golangci-lint по всем модулям (требует golangci-lint в PATH)
lint:
	@for m in $(MODULES); do echo ">> lint $$m"; (cd $$m && golangci-lint run ./...) || exit 1; done

# === Сквозные E2E через периметр (ADR-0018) ===
# Прогон ТОЛЬКО локальный, ручной: в CI стенд не поднимается. Цели поднимают
# docker-compose-стенд с реальным OIDC (Keycloak + Oauth2-Proxy) и гоняют набор
# tests/e2e (build-тег integration). Готовность стенда ждёт сам набор (TestMain),
# изоляция — уникальные имена сервисов, очистка — `docker compose down -v`.
COMPOSE_E2E := docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.e2e.yml
E2E_TLS_DIR := $(CURDIR)/deploy/tls
# База периметра для E2E — через oauth2-proxy (:4180), префикс /api добавляет набор.
E2E_PROXY_URL ?= http://localhost:4180
# База Keycloak для получения токена (password-grant idp-portal).
E2E_KEYCLOAK_URL ?= http://localhost:8088
# Бюджет ожидания переходов статусов воркфлоу (ретраи-поллинг, не sleep).
E2E_STATUS_TIMEOUT ?= 120s

.PHONY: e2e-certs e2e-up e2e-test e2e-down e2e e2e-logs

## e2e-certs: сгенерировать тест-сертификаты (CA + server cert SAN=keycloak) для
## https-листенера Keycloak и доверия gateway. Идемпотентно: повторно не
## перегенерирует. Файлы не коммитятся (см. .gitignore).
e2e-certs:
	@mkdir -p $(E2E_TLS_DIR)
	@if [ ! -f $(E2E_TLS_DIR)/tls.crt ]; then \
		echo ">> генерация тест-сертификатов E2E в $(E2E_TLS_DIR)"; \
		openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
			-keyout $(E2E_TLS_DIR)/ca.key -out $(E2E_TLS_DIR)/ca.crt \
			-subj "/CN=idp-e2e-ca" 2>/dev/null; \
		openssl req -newkey rsa:2048 -nodes \
			-keyout $(E2E_TLS_DIR)/tls.key -out $(E2E_TLS_DIR)/tls.csr \
			-subj "/CN=keycloak" 2>/dev/null; \
		printf "subjectAltName=DNS:keycloak\n" > $(E2E_TLS_DIR)/ext.cnf; \
		openssl x509 -req -in $(E2E_TLS_DIR)/tls.csr \
			-CA $(E2E_TLS_DIR)/ca.crt -CAkey $(E2E_TLS_DIR)/ca.key -CAcreateserial \
			-out $(E2E_TLS_DIR)/tls.crt -days 3650 \
			-extfile $(E2E_TLS_DIR)/ext.cnf 2>/dev/null; \
		chmod 0644 $(E2E_TLS_DIR)/ca.crt $(E2E_TLS_DIR)/tls.crt $(E2E_TLS_DIR)/tls.key; \
	fi

## e2e-up: поднять E2E-стенд (реальный OIDC) в фоне. Готовность ждёт e2e-test.
e2e-up: e2e-certs
	$(COMPOSE_E2E) up --build -d

## e2e-test: прогнать сквозной набор против поднятого E2E-стенда.
e2e-test:
	cd tests/e2e && \
		E2E_PROXY_URL="$(E2E_PROXY_URL)" \
		E2E_KEYCLOAK_URL="$(E2E_KEYCLOAK_URL)" \
		E2E_STATUS_TIMEOUT="$(E2E_STATUS_TIMEOUT)" \
		go test -tags=integration -count=1 -v ./...

## e2e-down: остановить E2E-стенд и очистить тома.
e2e-down:
	$(COMPOSE_E2E) down -v

## e2e: полный локальный цикл — поднять стенд, прогнать набор, гарантированно
## погасить стенд (очистка выполняется даже при падении тестов).
e2e: e2e-up
	@$(MAKE) e2e-test; rc=$$?; $(MAKE) e2e-down; exit $$rc

## e2e-logs: собрать логи E2E-стенда (диагностика при падении набора).
e2e-logs:
	$(COMPOSE_E2E) logs --no-color

# === Интеграционные тесты воркфлоу против РЕАЛЬНОГО GitLab (ADR-0019) ===
# Прогон ТОЛЬКО локальный, ручной: GitLab CE тяжёл (~2.5GB, старт 3-5 мин), в CI не
# поднимается. Стенд = базовая локалка + реальный OIDC (e2e-override, чтобы
# переиспользовать харнесс tests/e2e через oauth2-proxy) + реальный GitLab
# (gitlab-override). Vault/Harbor остаются моками. Готовность GitLab ждёт healthcheck
# compose и сам набор (requireGitLab), очистка — `down -v`.
COMPOSE_GITLAB := docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.e2e.yml -f deploy/compose/docker-compose.gitlab.yml
# Публикуемый адрес реального GitLab для сидирования и ассертов набора через GitLab API.
GITLAB_API_URL ?= http://localhost:8929
# Фиксированный root-PAT (фикстура стенда): должен совпадать с SEED_TOKEN/GITLAB_TOKEN
# в docker-compose.gitlab.yml. password-grant в GitLab отключён, поэтому токен минтит
# seed.rb через gitlab-rails runner ИМЕННО этим значением.
GITLAB_TOKEN ?= glpat-idpdevinfraseed01234
# Пароль тест-пользователей (alice/bob), создаваемых сидом по REST.
SEED_USER_PASSWORD ?= Idp-9fK2-xQ7w-Vb3T
# Бюджет ожидания готовности GitLab/воркфлоу (GitLab стартует медленно).
GITLAB_STATUS_TIMEOUT ?= 600s

.PHONY: gitlab-up gitlab-seed gitlab-test gitlab-down gitlab gitlab-logs

## gitlab-up: поднять стенд с реальным GitLab (реальный OIDC + реальный GitLab) в
## фоне, дождаться готовности GitLab и засеять его (gitlab-seed).
gitlab-up: e2e-certs
	$(COMPOSE_GITLAB) up --build -d
	@$(MAKE) gitlab-seed

## gitlab-seed: дождаться готовности GitLab и засеять детерминированно — выпустить
## фиксированный root-PAT через `gitlab-rails runner /seed.rb` ВНУТРИ контейнера
## gitlab (ROPC отключён), затем создать группы demo/demo2 и пользователей alice/bob
## по REST. Идемпотентно (повторный прогон безопасен).
gitlab-seed:
	@echo ">> ждём готовности GitLab ($(GITLAB_API_URL)); холодный старт 3-5 мин ..."
	@for i in $$(seq 1 90); do \
		if curl -fsS $(GITLAB_API_URL)/-/health >/dev/null 2>&1; then echo ">> GitLab готов"; break; fi; \
		sleep 5; \
	done
	@echo ">> выпускаем фиксированный root-PAT (gitlab-rails runner) ..."
	@$(COMPOSE_GITLAB) exec -T gitlab gitlab-rails runner /seed.rb
	@echo ">> сидируем группы/пользователей по REST ..."
	@GITLAB_URL="$(GITLAB_API_URL)" GITLAB_TOKEN="$(GITLAB_TOKEN)" SEED_USER_PASSWORD="$(SEED_USER_PASSWORD)" \
		sh deploy/compose/gitlab-seed/seed.sh

## gitlab-test: прогнать интеграционный набор против поднятого и засеянного стенда.
## Токен — фиксированная фикстура GITLAB_TOKEN (тот же root-PAT, что выпустил seed.rb).
gitlab-test:
	cd tests/e2e && \
		E2E_PROXY_URL="$(E2E_PROXY_URL)" \
		E2E_KEYCLOAK_URL="$(E2E_KEYCLOAK_URL)" \
		E2E_STATUS_TIMEOUT="$(GITLAB_STATUS_TIMEOUT)" \
		GITLAB_API_URL="$(GITLAB_API_URL)" \
		GITLAB_TOKEN="$(GITLAB_TOKEN)" \
		GITLAB_STATUS_TIMEOUT="$(GITLAB_STATUS_TIMEOUT)" \
		go test -tags=integration -count=1 -v ./...

## gitlab-down: остановить стенд с реальным GitLab и очистить тома.
gitlab-down:
	$(COMPOSE_GITLAB) down -v

## gitlab: полный локальный цикл — поднять стенд, прогнать набор, гарантированно
## погасить стенд (очистка выполняется даже при падении тестов).
gitlab: gitlab-up
	@$(MAKE) gitlab-test; rc=$$?; $(MAKE) gitlab-down; exit $$rc

## gitlab-logs: собрать логи стенда с реальным GitLab (диагностика).
gitlab-logs:
	$(COMPOSE_GITLAB) logs --no-color

# === Интеграционные тесты воркфлоу против РЕАЛЬНОГО Vault (ADR-0020) ===
# Прогон ТОЛЬКО локальный, ручной: реальный Vault — отдельный профиль, в CI не
# поднимается (зеркалим gitlab-паттерн ради единой операционной модели, хотя сам
# Vault лёгок и стартует за секунды). Стенд = базовая локалка + реальный OIDC
# (e2e-override, чтобы переиспользовать харнесс tests/e2e через oauth2-proxy) +
# реальный Vault (vault-override). GitLab/Harbor остаются моками. Готовность Vault
# ждёт healthcheck compose и сам набор (requireVault), очистка — `down -v`.
COMPOSE_VAULT := docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.e2e.yml -f deploy/compose/docker-compose.vault.yml
# Публикуемый адрес реального Vault для сидирования и ассертов набора через Vault API.
VAULT_ADDR ?= http://localhost:8200
# Dev root-токен (фикстура стенда): должен совпадать с VAULT_DEV_ROOT_TOKEN_ID/
# VAULT_TOKEN в docker-compose.vault.yml.
VAULT_TOKEN ?= idp-dev-root-token
# Бюджет ожидания готовности Vault/воркфлоу. Vault стартует за секунды, поэтому
# дефолт существенно меньше gitlab-бюджета (там 600s).
VAULT_STATUS_TIMEOUT ?= 120s

.PHONY: vault-up vault-seed vault-test vault-down vault vault-logs

## vault-up: поднять стенд с реальным Vault (реальный OIDC + реальный Vault) в фоне,
## дождаться готовности Vault и засеять его (vault-seed).
vault-up: e2e-certs
	$(COMPOSE_VAULT) up --build -d
	@$(MAKE) vault-seed

## vault-seed: дождаться готовности Vault и засеять детерминированно — включить
## AppRole auth-движок и предзасеять секрет для transfer-теста по REST уже известным
## dev root-токеном. Идемпотентно (повторный прогон безопасен).
vault-seed:
	@echo ">> ждём готовности Vault ($(VAULT_ADDR)); dev-старт — секунды ..."
	@for i in $$(seq 1 30); do \
		if curl -fsS $(VAULT_ADDR)/v1/sys/health >/dev/null 2>&1; then echo ">> Vault готов"; break; fi; \
		sleep 2; \
	done
	@echo ">> сидируем approle/KV по REST ..."
	@VAULT_ADDR="$(VAULT_ADDR)" VAULT_TOKEN="$(VAULT_TOKEN)" \
		sh deploy/compose/vault-seed/seed.sh

## vault-test: прогнать интеграционный набор против поднятого и засеянного стенда.
## Токен — фиксированная фикстура VAULT_TOKEN (тот же dev root-токен).
vault-test:
	cd tests/e2e && \
		E2E_PROXY_URL="$(E2E_PROXY_URL)" \
		E2E_KEYCLOAK_URL="$(E2E_KEYCLOAK_URL)" \
		E2E_STATUS_TIMEOUT="$(VAULT_STATUS_TIMEOUT)" \
		VAULT_ADDR="$(VAULT_ADDR)" \
		VAULT_TOKEN="$(VAULT_TOKEN)" \
		VAULT_STATUS_TIMEOUT="$(VAULT_STATUS_TIMEOUT)" \
		go test -tags=integration -count=1 -v ./...

## vault-down: остановить стенд с реальным Vault и очистить тома.
vault-down:
	$(COMPOSE_VAULT) down -v

## vault: полный локальный цикл — поднять стенд, прогнать набор, гарантированно
## погасить стенд (очистка выполняется даже при падении тестов).
vault: vault-up
	@$(MAKE) vault-test; rc=$$?; $(MAKE) vault-down; exit $$rc

## vault-logs: собрать логи стенда с реальным Vault (диагностика).
vault-logs:
	$(COMPOSE_VAULT) logs --no-color

# === Интеграционные тесты воркфлоу против РЕАЛЬНОГО Harbor (ADR-0021) ===
# Прогон ТОЛЬКО локальный, ручной: реальный Harbor — отдельный профиль, в CI не
# поднимается (Harbor тяжёлый — связка контейнеров, зеркалим gitlab-паттерн). В отличие
# от GitLab/Vault, Harbor поднимается ОТДЕЛЬНЫМ compose-проектом из официального
# installer-bundle (deploy/compose/harbor/up.sh|down.sh), а IDP-стек лишь переключает
# worker на него (docker-compose.harbor.yml). Стенд = базовая локалка + реальный OIDC
# (e2e-override, харнесс tests/e2e) + реальный Harbor. GitLab/Vault остаются моками.
# Готовность Harbor ждёт harbor-seed (/api/v2.0/health) и сам набор (requireHarbor).
COMPOSE_HARBOR := docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.e2e.yml -f deploy/compose/docker-compose.harbor.yml
# Публикуемый адрес реального Harbor для сидирования и ассертов набора через Harbor API.
HARBOR_ADDR ?= http://localhost:8085
# Креденшелы admin (фикстура стенда): должны совпадать с harbor_admin_password в
# deploy/compose/harbor/harbor.yml и HARBOR_USERNAME/HARBOR_PASSWORD в docker-compose.harbor.yml.
HARBOR_USERNAME ?= admin
HARBOR_PASSWORD ?= Harbor12345
# Пиннутая версия Harbor (installer-bundle); должна совпадать с дефолтом up.sh.
HARBOR_VERSION ?= v2.14.4
# Бюджет ожидания готовности Harbor/воркфлоу. Harbor тяжёлый — соразмерно gitlab (600s),
# не vault. Duration-строка для go test; harbor-seed получает число секунд отдельно.
HARBOR_STATUS_TIMEOUT ?= 600s

.PHONY: harbor-up harbor-seed harbor-test harbor-down harbor harbor-logs

## harbor-up: поднять стенд с реальным Harbor — связку контейнеров Harbor отдельным
## compose-проектом (up.sh) и IDP-стек с переключением worker'а на Harbor; затем
## дождаться готовности (harbor-seed).
harbor-up: e2e-certs
	HARBOR_VERSION="$(HARBOR_VERSION)" sh deploy/compose/harbor/up.sh
	$(COMPOSE_HARBOR) up --build -d
	@$(MAKE) harbor-seed

## harbor-seed: дождаться готовности Harbor (/api/v2.0/health) и подтвердить admin-доступ
## по HTTP Basic (фикстура стенда). Per-service проекты/robot создаёт сам воркфлоу.
harbor-seed:
	@echo ">> ждём готовности Harbor ($(HARBOR_ADDR)); связка контейнеров стартует не быстро ..."
	@HARBOR_ADDR="$(HARBOR_ADDR)" HARBOR_USERNAME="$(HARBOR_USERNAME)" HARBOR_PASSWORD="$(HARBOR_PASSWORD)" \
		HARBOR_SEED_TIMEOUT="600" \
		sh deploy/compose/harbor-seed/seed.sh

## harbor-test: прогнать интеграционный набор против поднятого и засеянного стенда.
## Креденшелы — фиксированные фикстуры (admin/Harbor12345).
harbor-test:
	cd tests/e2e && \
		E2E_PROXY_URL="$(E2E_PROXY_URL)" \
		E2E_KEYCLOAK_URL="$(E2E_KEYCLOAK_URL)" \
		E2E_STATUS_TIMEOUT="$(HARBOR_STATUS_TIMEOUT)" \
		HARBOR_ADDR="$(HARBOR_ADDR)" \
		HARBOR_USERNAME="$(HARBOR_USERNAME)" \
		HARBOR_PASSWORD="$(HARBOR_PASSWORD)" \
		HARBOR_STATUS_TIMEOUT="$(HARBOR_STATUS_TIMEOUT)" \
		go test -tags=integration -count=1 -v ./...

## harbor-down: остановить IDP-стек и связку Harbor, очистить тома/данные.
harbor-down:
	$(COMPOSE_HARBOR) down -v
	sh deploy/compose/harbor/down.sh

## harbor: полный локальный цикл — поднять стенд, прогнать набор, гарантированно
## погасить стенд (очистка выполняется даже при падении тестов).
harbor: harbor-up
	@$(MAKE) harbor-test; rc=$$?; $(MAKE) harbor-down; exit $$rc

## harbor-logs: собрать логи IDP-стека профиля Harbor (диагностика).
harbor-logs:
	$(COMPOSE_HARBOR) logs --no-color
