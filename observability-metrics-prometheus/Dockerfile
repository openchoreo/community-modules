# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.26.2-alpine3.23 AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

FROM alpine:3.23

WORKDIR /app

RUN apk add --no-cache ca-certificates && \
    addgroup -g 10500 appgroup && \
    adduser -D -u 10500 -G appgroup appuser

COPY --from=builder /app/main .

EXPOSE 9099

USER 10500:10500

CMD ["./main"]
