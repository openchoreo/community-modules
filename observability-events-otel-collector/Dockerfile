# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

# syntax=docker/dockerfile:1

# Stage 1: build the collector distribution with OCB.
# Built on $BUILDPLATFORM, cross-compiled to $TARGETARCH for native multi-arch builds.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src

ARG OCB_VERSION=v0.153.0
RUN go install go.opentelemetry.io/collector/cmd/builder@${OCB_VERSION}

COPY builder-config.yaml .
COPY k8seventenrichprocessor/ ./k8seventenrichprocessor/

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    builder --config=builder-config.yaml

# Stage 2: minimal, non-root runtime.
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /src/dist/otelcol-k8s-events /otelcol-k8s-events

EXPOSE 13133

ENTRYPOINT ["/otelcol-k8s-events"]
CMD ["--config", "/etc/otelcol/config.yaml"]
