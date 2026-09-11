CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
# The adapter contract is owned by openchoreo/openchoreo. Override SPEC to generate
# against a local checkout or a branch while a contract change is still in review, e.g.
#   make openapi-codegen SPEC=../../openchoreo/openapi/observability-logs-adapter-api.yaml
SPEC ?= https://raw.githubusercontent.com/openchoreo/openchoreo/main/openapi/observability-logs-adapter-api.yaml

.PHONY: oapi-codegen-install openapi-codegen unit-test

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-models.yaml $(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-server.yaml $(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-client.yaml $(SPEC)

MODULE_NAME := $(notdir $(CURDIR))

unit-test:
	go test -coverprofile=coverage.out ./...
	if [ -f coverage.out ]; then mv coverage.out ../$(MODULE_NAME)-coverage.out; fi
