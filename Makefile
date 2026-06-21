# Makefile платформы IDP. Инструменты кодогена/линтинга живут в ./tools
# (вне go.work) и запускаются с GOWORK=off, чтобы их зависимости не протекали
# в графы сервисов (docs/adr/0006).

MODULES := pkg services/gateway services/idm services/projects services/devinfra-worker tests/e2e
TOOLS_BIN := $(CURDIR)/tools/bin

# DSN каталога проектов для миграций; переопределяется из окружения.
PROJECTS_DSN ?= postgres://projects:projects@localhost:5432/projects?sslmode=disable
PROJECTS_MIGRATIONS := $(CURDIR)/services/projects/migrations
# DSN сервиса IDM для миграций (роли/права RBAC); переопределяется из окружения.
IDM_DSN ?= postgres://idm:idm@localhost:5433/idm?sslmode=disable
IDM_MIGRATIONS := $(CURDIR)/services/idm/migrations
# GOOSE_CMD — команда goose (up|down|status|...).
GOOSE_CMD ?= up

.PHONY: tools proto openapi gen test lint lint-openapi tidy tidy-check migrate-projects migrate-idm

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

## tidy: go mod tidy по всем модулям
tidy:
	@for m in $(MODULES) tools; do echo ">> tidy $$m"; (cd $$m && GOWORK=off go mod tidy) || exit 1; done

## tidy-check: проверить, что go.mod/go.sum синхронизированы
tidy-check: tidy
	git diff --exit-code

## lint: golangci-lint по всем модулям (требует golangci-lint в PATH)
lint:
	@for m in $(MODULES); do echo ">> lint $$m"; (cd $$m && golangci-lint run ./...) || exit 1; done
