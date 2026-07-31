OAPI_CODEGEN_VERSION ?= v2.7.0
OAS := api/observability-logs-adapter-api.yaml
MODULE_NAME := $(notdir $(CURDIR))

.PHONY: oapi-codegen-install generate build run tidy unit-test

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

## generate: regenerate server stubs — run after editing the OAS file
generate: oapi-codegen-install
	$(shell go env GOPATH)/bin/oapi-codegen -generate types,gin -package gen -o gen/server.gen.go $(OAS)

build:
	go build -o bin/adaptor-api ./cmd

run:
	go run ./cmd

tidy:
	go mod tidy

unit-test:
	go test -coverprofile=coverage.out ./...
	mv coverage.out ../$(MODULE_NAME)-coverage.out
