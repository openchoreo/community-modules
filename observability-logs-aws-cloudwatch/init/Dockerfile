# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

FROM alpine:3.23

RUN apk upgrade --no-cache && \
    apk add --no-cache bash aws-cli jq && \
    addgroup -g 10500 cloudwatch && \
    adduser -D -u 10500 -G cloudwatch cloudwatch

USER cloudwatch

COPY --chown=cloudwatch --chmod=0540 setup-cloudwatch.sh setup-cloudwatch.sh

CMD ["bash", "setup-cloudwatch.sh"]
