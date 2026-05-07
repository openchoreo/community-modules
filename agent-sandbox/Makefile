# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

MODULE_NAME := $(notdir $(CURDIR))

.PHONY: build unit-test lint docker-build

## Build the manager binary locally.
build:
	mkdir -p bin
	go build -o bin/manager ./cmd

## Run unit tests and emit a coverage file named <module>-coverage.out
## in the parent directory (consumed by the CI codecov step).
unit-test:
	go test -coverprofile=coverage.out ./...
	mv coverage.out ../$(MODULE_NAME)-coverage.out

## Run go vet.
lint:
	go vet ./...

## Build the Docker image locally (does not push).
docker-build:
	docker build -t agent-sandbox-controller:dev .
