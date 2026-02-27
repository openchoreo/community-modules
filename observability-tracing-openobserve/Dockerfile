# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .


FROM alpine:3.23

RUN apk --no-cache add ca-certificates && \
    addgroup -g 10500 appuser && \
    adduser -D -u 10500 -G appuser appuser

WORKDIR /home/appuser
COPY --from=builder --chown=appuser:appuser --chmod=0550 /app/main .

USER appuser
EXPOSE 9098

CMD ["./main"]
