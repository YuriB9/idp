# Makefile платформы IDP. Инструменты кодогена/линтинга живут в ./tools
# (вне go.work) и запускаются с GOWORK=off, чтобы их зависимости не протекали
# в графы сервисов (docs/adr/0006).

MODULES := pkg services/gateway services/idm services/projects services/devinfra-worker tests/e2e
TOOLS_BIN := $(CURDIR)/tools/bin

.PHONY: tools proto openapi gen test lint tidy tidy-check

## tools: собрать пинованные инструменты кодогена в tools/bin
tools:
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/buf github.com/bufbuild/buf/cmd/buf
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go
	cd tools && GOWORK=off GOBIN=$(TOOLS_BIN) go build -o bin/protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc

## proto: сгенерировать Go-стабы из .proto
proto: tools
	cd proto && PATH="$(TOOLS_BIN):$$PATH" $(TOOLS_BIN)/buf lint
	cd proto && PATH="$(TOOLS_BIN):$$PATH" $(TOOLS_BIN)/buf generate

## openapi: сгенерировать TS-клиент и zod-схемы из OpenAPI
openapi:
	cd web && npm run gen

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
