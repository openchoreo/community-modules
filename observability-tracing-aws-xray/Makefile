CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
SPEC := https://raw.githubusercontent.com/openchoreo/openchoreo.github.io/ac623d6153db633fe78ce25fb649635e96b24361/static/api-specs/observability-tracing-adapter-api.yaml

.PHONY: oapi-codegen-install openapi-codegen unit-test

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-models.yaml $(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-server.yaml $(SPEC)

MODULE_NAME := $(notdir $(CURDIR))

unit-test:
	go test -coverprofile=coverage.out ./...
	if [ -f coverage.out ]; then mv coverage.out ../$(MODULE_NAME)-coverage.out; fi
