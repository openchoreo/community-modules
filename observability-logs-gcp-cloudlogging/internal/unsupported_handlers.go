// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/api/gen"
)

// Audit and platform logs are not served by this adapter. They return 501 so
// the observer reports the backend as not supporting them.

func (h *LogsHandler) QueryAuditLogs(
	_ context.Context, _ gen.QueryAuditLogsRequestObject,
) (gen.QueryAuditLogsResponseObject, error) {
	return gen.QueryAuditLogs501JSONResponse(makeError(gen.NotImplemented, errCodeNotImplemented,
		"audit logs are not supported by the GCP Cloud Logging adapter")), nil
}

func (h *LogsHandler) QueryAuditLogFilterValues(
	_ context.Context, _ gen.QueryAuditLogFilterValuesRequestObject,
) (gen.QueryAuditLogFilterValuesResponseObject, error) {
	return gen.QueryAuditLogFilterValues501JSONResponse(makeError(gen.NotImplemented, errCodeNotImplemented,
		"audit log filter values are not supported by the GCP Cloud Logging adapter")), nil
}

func (h *LogsHandler) QueryPlatformLogs(
	_ context.Context, _ gen.QueryPlatformLogsRequestObject,
) (gen.QueryPlatformLogsResponseObject, error) {
	return gen.QueryPlatformLogs501JSONResponse(makeError(gen.NotImplemented, errCodeNotImplemented,
		"platform logs are not supported by the GCP Cloud Logging adapter")), nil
}

func (h *LogsHandler) QueryPlatformLogFilterValues(
	_ context.Context, _ gen.QueryPlatformLogFilterValuesRequestObject,
) (gen.QueryPlatformLogFilterValuesResponseObject, error) {
	return gen.QueryPlatformLogFilterValues501JSONResponse(makeError(gen.NotImplemented, errCodeNotImplemented,
		"platform log filter values are not supported by the GCP Cloud Logging adapter")), nil
}
