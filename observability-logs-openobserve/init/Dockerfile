# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

FROM alpine:3.23

RUN apk upgrade --no-cache && \
    apk add --no-cache bash curl jq && \
    addgroup -g 10500 openobserve && \
    adduser -D -u 10500 -G openobserve openobserve

USER openobserve

COPY --chown=openobserve --chmod=0540 setup-openobserve.sh setup-openobserve.sh

CMD ["bash", "setup-openobserve.sh"]
