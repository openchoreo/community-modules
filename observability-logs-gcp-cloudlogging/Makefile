# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

CFG_DIR := internal/api
OAPI_CODEGEN_VERSION ?= v2.5.1
SPEC := https://raw.githubusercontent.com/openchoreo/openchoreo/main/openapi/observability-logs-adapter-api.yaml

.PHONY: oapi-codegen-install openapi-codegen unit-test build run

oapi-codegen-install:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

openapi-codegen: oapi-codegen-install
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-models.yaml $(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-server.yaml $(SPEC)
	cd $(CFG_DIR) && $(shell go env GOPATH)/bin/oapi-codegen --config cfg-client.yaml $(SPEC)

MODULE_NAME := $(notdir $(CURDIR))

unit-test:
	go test -coverprofile=coverage.out ./...
	mv coverage.out ../$(MODULE_NAME)-coverage.out

build:
	CGO_ENABLED=0 go build -o bin/adapter .

run: build
	./bin/adapter
