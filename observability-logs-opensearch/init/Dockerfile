# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

FROM alpine:3.23

RUN apk upgrade --no-cache && \
    apk add --no-cache bash curl jq && \
    addgroup -g 10500 opensearch && \
    adduser -D -u 10500 -G opensearch opensearch

USER opensearch

COPY --chown=opensearch --chmod=0540 setup-opensearch.sh setup-opensearch.sh

CMD ["bash", "setup-opensearch.sh"]
