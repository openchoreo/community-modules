CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
SPEC := https://raw.githubusercontent.com/openchoreo/openchoreo.github.io/refs/heads/main/static/api-specs/observability-metrics-adapter.yaml
GOBIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

.PHONY: oapi-codegen-install openapi-codegen unit-test

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(GOBIN_DIR)/oapi-codegen --config cfg-models.yaml $(SPEC)
	cd $(CFG_DIR) && $(GOBIN_DIR)/oapi-codegen --config cfg-server.yaml $(SPEC)

MODULE_NAME := $(notdir $(CURDIR))

unit-test:
	go test -coverprofile=coverage.out ./...
	if [ -f coverage.out ]; then mv coverage.out ../$(MODULE_NAME)-coverage.out; fi
