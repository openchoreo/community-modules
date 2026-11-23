// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"github.com/openchoreo/community-modules/observability-tracing-gcp-cloudtrace/internal/api/gen"
)

// rawTracesNotSupported is the detail of the 501 answer to a raw trace query.
const rawTracesNotSupported = "raw trace queries are not supported by the GCP Cloud Trace tracing module"

// QueryRawTraces implements POST /api/v1alpha1/traces/raw-query. Raw queries
// are not supported by this module yet, so it answers 501, which the Observer
// passes on as "not supported".
//
// TODO: run Cloud Trace filters as raw queries (openchoreo/openchoreo#5043).
func (h *TracingHandler) QueryRawTraces(
	_ context.Context, _ gen.QueryRawTracesRequestObject,
) (gen.QueryRawTracesResponseObject, error) {
	return gen.QueryRawTraces501JSONResponse{
		Title:  ptr(gen.NotImplemented),
		Detail: ptr(rawTracesNotSupported),
	}, nil
}
