CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
# Pinned to an immutable commit SHA so `make openapi-codegen` is reproducible.
# Bump the SHA when the upstream spec changes.
#
# NOTE: the events API (`POST /api/v1/events/query`, EventsQueryRequest/Response,
# EventEntry) must be present in this upstream spec before regenerating. Until that
# lands upstream, the generated `internal/api/gen/*` was produced from the local
# TASKS/logs-event-publishing/openapi.yaml — regenerating against a spec that lacks
# the events path will strip the events code and break the build. Override with e.g.
# `make openapi-codegen SPEC=../TASKS/logs-event-publishing/openapi.yaml` in the meantime.
SPEC := https://raw.githubusercontent.com/openchoreo/openchoreo/main/openapi/observability-logs-adapter-api.yaml

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
