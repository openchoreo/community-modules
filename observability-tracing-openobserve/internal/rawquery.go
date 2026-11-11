// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/openobserve"
)

// RawQueryLimits bound how a raw query runs.
type RawQueryLimits struct {
	// Timeout stops a raw query; OpenObserve is given the same search timeout.
	Timeout time.Duration
	// MaxResponseBytes is the largest OpenObserve response passed on.
	MaxResponseBytes int64
}

// DefaultRawQueryLimits are the limits used unless configured otherwise.
var DefaultRawQueryLimits = RawQueryLimits{Timeout: 30 * time.Second, MaxResponseBytes: 10 << 20}

// rawQueryGrace is how long past the timeout the adapter waits for OpenObserve
// to answer before giving up on the query.
const rawQueryGrace = 2 * time.Second

// QueryRawTraces implements POST /api/v1alpha1/traces/raw-query: it runs an
// OpenObserve search body, as written, through the search API for traces, as
// the raw query user and within the raw query limits. The response is
// OpenObserve's own.
func (h *TracingHandler) QueryRawTraces(
	ctx context.Context, request gen.QueryRawTracesRequestObject,
) (gen.QueryRawTracesResponseObject, error) {
	if request.JSONBody == nil {
		return gen.QueryRawTraces415JSONResponse{
			Title:  ptr(gen.UnsupportedMediaType),
			Detail: ptr("send an OpenObserve search body as application/json"),
		}, nil
	}
	if h.rawClient == nil {
		return gen.QueryRawTraces501JSONResponse{
			Title: ptr(gen.NotImplemented),
			Detail: ptr("raw trace queries need an OpenObserve user of their own, " +
				"which is not configured for this module"),
		}, nil
	}

	limits := h.rawQuery
	body, err := openobserve.PrepareRawQuery(*request.JSONBody, limits.Timeout)
	if err != nil {
		return h.rawQueryError(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, limits.Timeout+rawQueryGrace)
	defer cancel()
	result, err := h.rawClient.RawSearch(ctx, body, limits.MaxResponseBytes)
	if err != nil {
		return h.rawQueryError(err), nil
	}

	return rawQueryResponse(result), nil
}

// rawQueryResponse writes OpenObserve's response as is. The generated
// QueryRawTraces200JSONResponse is a defined type over json.RawMessage, which
// drops its MarshalJSON method, so the generated writer would encode the body
// as a base64 string.
type rawQueryResponse json.RawMessage

func (r rawQueryResponse) VisitQueryRawTracesResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r)
	return err
}

// rawQueryResultTooLargeCode is the errorCode of a 400 for a result larger
// than the maximum response size, which the Observer tells apart from a
// rejected query.
const rawQueryResultTooLargeCode = "RAW_QUERY_RESULT_TOO_LARGE"

func (h *TracingHandler) rawQueryError(err error) gen.QueryRawTracesResponseObject {
	var invalid *openobserve.InvalidRawQueryError
	switch {
	case errors.As(err, &invalid):
		return gen.QueryRawTraces400JSONResponse{Title: ptr(gen.BadRequest), Detail: ptr(invalid.Error())}
	case errors.Is(err, openobserve.ErrRawQueryTooLarge):
		return gen.QueryRawTraces400JSONResponse{
			Title:     ptr(gen.BadRequest),
			ErrorCode: ptr(rawQueryResultTooLargeCode),
			Detail: ptr(fmt.Sprintf("the result is larger than the maximum response size (%d bytes); "+
				"narrow the query or add a LIMIT", h.rawQuery.MaxResponseBytes)),
		}
	case errors.Is(err, openobserve.ErrRawQueryTimeout):
		return gen.QueryRawTraces504JSONResponse{
			Title:  ptr(gen.GatewayTimeout),
			Detail: ptr(fmt.Sprintf("the query ran past the %s query timeout", h.rawQuery.Timeout)),
		}
	}
	h.logger.Error("Raw trace query failed", "error", err)
	return gen.QueryRawTraces500JSONResponse{Title: ptr(gen.InternalServerError), Detail: ptr("internal server error")}
}
