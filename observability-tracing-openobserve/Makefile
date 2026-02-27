CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
SPEC := openapi/openapi.yaml

.PHONY: oapi-codegen-install openapi-codegen

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-models.yaml ../../$(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-server.yaml ../../$(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-client.yaml ../../$(SPEC)
