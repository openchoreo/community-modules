// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
)

// Signals this adapter is yet to implement.
// The observer will surface these as 501, so the UI can say the backend does not support them.
//
// The adapter contract covers more than any one backend implements, and the
// observer negotiates capability at runtime rather than through config: an
// adapter that cannot answer a signal returns 501 and the observer surfaces
// that verbatim, so the UI can say the backend does not support it. Leaving
// these unimplemented would instead 404, which reads as a misrouted request.
//
// Each is independently declinable, so adopting one later does not disturb
// the others.

func (h *LogsHandler) QueryAuditLogs(
	_ context.Context, _ gen.QueryAuditLogsRequestObject,
) (gen.QueryAuditLogsResponseObject, error) {
	return gen.QueryAuditLogs501JSONResponse(
		makeError(gen.NotImplemented, errCodeNotImplemented, "audit logs are not supported by this adapter")), nil
}

func (h *LogsHandler) QueryAuditLogFilterValues(
	_ context.Context, _ gen.QueryAuditLogFilterValuesRequestObject,
) (gen.QueryAuditLogFilterValuesResponseObject, error) {
	return gen.QueryAuditLogFilterValues501JSONResponse(
		makeError(gen.NotImplemented, errCodeNotImplemented, "audit log filter values are not supported by this adapter")), nil
}
