CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
OAPI_CODEGEN_BIN := $(if $(shell go env GOBIN),$(shell go env GOBIN),$(shell go env GOPATH)/bin)/oapi-codegen
SPEC := openapi/openapi.yaml

.PHONY: oapi-codegen-install openapi-codegen unit-test

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(OAPI_CODEGEN_BIN) --config cfg-models.yaml ../../$(SPEC)
	cd $(CFG_DIR) && $(OAPI_CODEGEN_BIN) --config cfg-server.yaml ../../$(SPEC)

unit-test:
	go test -coverprofile=coverage.out ./...
