# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

# Pin the target platform so a build from an arm64 host (e.g. Apple Silicon)
# still produces a linux/amd64 image for the amd64 GKE nodes.
FROM --platform=linux/amd64 golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

FROM --platform=linux/amd64 alpine:3.23

RUN apk --no-cache add ca-certificates && \
    addgroup -g 10500 appuser && \
    adduser -D -u 10500 -G appuser appuser

WORKDIR /home/appuser
COPY --from=builder --chown=appuser:appuser --chmod=0550 /app/main .

USER appuser
EXPOSE 9099

CMD ["./main"]
