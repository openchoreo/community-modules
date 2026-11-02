// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"github.com/openchoreo/community-modules/observability-logs-aws-cloudwatch/internal/api/gen"
)

// Signals this adapter does not implement. The observer surfaces 501 as an unsupported backend capability.

func notImplemented(msg string) gen.ErrorResponse {
	return gen.ErrorResponse{Title: ptr(gen.NotImplemented), Message: ptr(msg)}
}

func (h *LogsHandler) QueryPlatformLogs(
	_ context.Context, _ gen.QueryPlatformLogsRequestObject,
) (gen.QueryPlatformLogsResponseObject, error) {
	return gen.QueryPlatformLogs501JSONResponse(notImplemented("platform logs are not supported by this adapter")), nil
}

func (h *LogsHandler) QueryPlatformLogFilterValues(
	_ context.Context, _ gen.QueryPlatformLogFilterValuesRequestObject,
) (gen.QueryPlatformLogFilterValuesResponseObject, error) {
	return gen.QueryPlatformLogFilterValues501JSONResponse(
		notImplemented("platform log filter values are not supported by this adapter")), nil
}

func (h *LogsHandler) QueryAuditLogs(
	_ context.Context, _ gen.QueryAuditLogsRequestObject,
) (gen.QueryAuditLogsResponseObject, error) {
	return gen.QueryAuditLogs501JSONResponse(notImplemented("audit logs are not supported by this adapter")), nil
}

func (h *LogsHandler) QueryAuditLogFilterValues(
	_ context.Context, _ gen.QueryAuditLogFilterValuesRequestObject,
) (gen.QueryAuditLogFilterValuesResponseObject, error) {
	return gen.QueryAuditLogFilterValues501JSONResponse(
		notImplemented("audit log filter values are not supported by this adapter")), nil
}
