SPEC := openapi/openapi.yaml
CFG_DIR := internal/api

.PHONY: oapi-codegen-install openapi-codegen

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

openapi-codegen:
	cd $(CFG_DIR) && oapi-codegen --config cfg-models.yaml ../../$(SPEC)
	cd $(CFG_DIR) && oapi-codegen --config cfg-server.yaml ../../$(SPEC)
	cd $(CFG_DIR) && oapi-codegen --config cfg-client.yaml ../../$(SPEC)
