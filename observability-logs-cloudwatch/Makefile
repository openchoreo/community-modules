CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
# Pinned to an immutable commit SHA so `make openapi-codegen` is reproducible.
# Bump the SHA when the upstream spec changes.
SPEC := https://raw.githubusercontent.com/openchoreo/openchoreo.github.io/1f1eb6ca3dadb4198ef561eb6fd910cf60f38fa4/static/api-specs/observability-logs-adapter-api.yaml

.PHONY: oapi-codegen-install openapi-codegen unit-test

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-models.yaml $(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-server.yaml $(SPEC)

MODULE_NAME := $(notdir $(CURDIR))

unit-test:
	go test -coverprofile=coverage.out ./...
	mv coverage.out ../$(MODULE_NAME)-coverage.out
