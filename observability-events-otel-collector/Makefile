# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

MODULE_NAME := $(notdir $(CURDIR))
PROCESSOR_DIR := k8seventenrichprocessor
OCB_VERSION ?= v0.153.0
OCB_BIN ?= $(shell if [ -n "$$(go env GOBIN)" ]; then echo "$$(go env GOBIN)/builder"; else echo "$$(go env GOPATH)/bin/builder"; fi)

DOCKER ?= docker
IMAGE ?= observability-events-otel-collector:latest-dev

.PHONY: unit-test lint build docker-build clean

unit-test:
	cd $(PROCESSOR_DIR) && go test -coverprofile=coverage.out -covermode=atomic ./...
	mv $(PROCESSOR_DIR)/coverage.out ../$(MODULE_NAME)-coverage.out

lint:
	cd $(PROCESSOR_DIR) && golangci-lint run ./...

build:
	go install go.opentelemetry.io/collector/cmd/builder@$(OCB_VERSION)
	$(OCB_BIN) --config=builder-config.yaml

docker-build:
	$(DOCKER) build -t $(IMAGE) -f Dockerfile .

clean:
	rm -rf dist
