// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/opensearch"
)

func rawRequest(body string) gen.QueryRawTracesRequestObject {
	b := gen.QueryRawTracesJSONRequestBody(body)
	return gen.QueryRawTracesRequestObject{JSONBody: &b}
}

func rawHandler(t *testing.T, status int, body string, limits RawQueryLimits) (*TracingHandler, *string, *string) {
	t.Helper()
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	client, err := opensearch.NewClient(srv.URL, "reader", "pass", true, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	return NewTracingHandler(nil, testLogger()).WithRawQuery(client, "otel-traces-*", limits), &gotPath, &gotBody
}

func TestQueryRawTraces_RunsTheBodyOnTheTraceIndices(t *testing.T) {
	const response = `{"took":4,"timed_out":false,"hits":{"total":{"value":2},"hits":[]},` +
		`"aggregations":{"n":{"value":12345678901234567890}}}`
	h, gotPath, gotBody := rawHandler(t, http.StatusOK, response, DefaultRawQueryLimits)

	resp, err := h.QueryRawTraces(context.Background(), rawRequest(`{"size":0,`+
		`"query":{"bool":{"filter":[{"range":{"startTime":{"gte":"now-1h"}}},{"term":{"status.code":"Error"}}]}},`+
		`"aggs":{"n":{"value_count":{"field":"traceId"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	// Write the response the way the generated server does, so the test sees
	// the bytes a client gets.
	rec := httptest.NewRecorder()
	if err := resp.VisitQueryRawTracesResponse(rec); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != response {
		t.Errorf("the response must be OpenSearch's, unchanged: %s", got)
	}
	if *gotPath != "/otel-traces-*/_search" {
		t.Errorf("path = %s", *gotPath)
	}
	want := `{"aggs":{"n":{"value_count":{"field":"traceId"}}},` +
		`"query":{"bool":{"filter":[{"range":{"startTime":{"gte":"now-1h"}}},{"term":{"status.code":"Error"}}]}},` +
		`"size":0,"timeout":"30000ms"}`
	if *gotBody != want {
		t.Errorf("the body must run as written, with the adapter's timeout:\n got %s\nwant %s", *gotBody, want)
	}
}

func TestQueryRawTraces_RejectsANonJSONRequest(t *testing.T) {
	h := NewTracingHandler(nil, testLogger())
	text := gen.QueryRawTracesTextRequestBody("SELECT 1")
	for _, req := range []gen.QueryRawTracesRequestObject{{TextBody: &text}, {}} {
		resp, err := h.QueryRawTraces(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		unsupported, ok := resp.(gen.QueryRawTraces415JSONResponse)
		if !ok {
			t.Fatalf("got %T, want 415", resp)
		}
		if unsupported.Detail == nil || *unsupported.Detail != "send an OpenSearch search body as application/json" {
			t.Errorf("detail = %v", unsupported.Detail)
		}
	}
}

func TestQueryRawTraces_NotSupportedWithoutReadOnlyCredentials(t *testing.T) {
	h := NewTracingHandler(nil, testLogger())
	resp, err := h.QueryRawTraces(context.Background(), rawRequest(`{"query":{"match_all":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QueryRawTraces501JSONResponse); !ok {
		t.Fatalf("got %T, want 501", resp)
	}
}

func TestQueryRawTraces_RejectsInvalidBodies(t *testing.T) {
	h, _, _ := rawHandler(t, http.StatusOK, `{}`, DefaultRawQueryLimits)
	tests := []struct {
		name   string
		body   string
		detail string
	}{
		{"a JSON string", `"SELECT 1"`,
			"invalid OpenSearch query DSL: the request must be an OpenSearch search body (a JSON object)"},
		{"global aggregation", `{"aggs":{"all":{"global":{}}}}`,
			"invalid OpenSearch query DSL: aggs.all.global: global aggregations are not allowed, as they ignore the query"},
		{"another index", `{"query":{"terms":{"traceId":{"index":"audit-logs","id":"1","path":"ids"}}}}`,
			"invalid OpenSearch query DSL: query.terms.traceId.index: raw queries can only read the trace indices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := h.QueryRawTraces(context.Background(), rawRequest(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			bad, ok := resp.(gen.QueryRawTraces400JSONResponse)
			if !ok {
				t.Fatalf("got %T, want 400", resp)
			}
			if bad.Detail == nil || *bad.Detail != tt.detail {
				t.Errorf("detail = %v, want %q", bad.Detail, tt.detail)
			}
		})
	}
}

func TestQueryRawTraces_MapsOpenSearchErrors(t *testing.T) {
	t.Run("rejected by OpenSearch", func(t *testing.T) {
		h, _, _ := rawHandler(t, http.StatusBadRequest,
			`{"error":{"root_cause":[{"type":"parsing_exception","reason":"unknown query [termz]"}]},"status":400}`,
			DefaultRawQueryLimits)
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(`{"query":{"termz":{}}}`))
		bad, ok := resp.(gen.QueryRawTraces400JSONResponse)
		if !ok || *bad.Detail != "invalid OpenSearch query DSL: parsing_exception: unknown query [termz]" {
			t.Errorf("got %#v", resp)
		}
		if bad.ErrorCode != nil {
			t.Errorf("a rejected query must not carry the result too large code, got %q", *bad.ErrorCode)
		}
	})
	t.Run("too large", func(t *testing.T) {
		h, _, _ := rawHandler(t, http.StatusOK, `{"took":1,"hits":{"hits":["`+strings.Repeat("x", 2048)+`"]}}`,
			RawQueryLimits{Timeout: time.Second, MaxResponseBytes: 1024})
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(`{"query":{"match_all":{}}}`))
		bad, ok := resp.(gen.QueryRawTraces400JSONResponse)
		if !ok || !strings.Contains(*bad.Detail, "larger than the maximum response size (1024 bytes)") {
			t.Errorf("got %#v", resp)
		}
		if bad.ErrorCode == nil || *bad.ErrorCode != "RAW_QUERY_RESULT_TOO_LARGE" {
			t.Errorf("a result too large must carry its errorCode, got %v", bad.ErrorCode)
		}
	})
	t.Run("timed out", func(t *testing.T) {
		h, _, _ := rawHandler(t, http.StatusOK, `{"took":30000,"timed_out":true,"hits":{"hits":[]}}`, DefaultRawQueryLimits)
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(`{"query":{"match_all":{}}}`))
		timeout, ok := resp.(gen.QueryRawTraces504JSONResponse)
		if !ok || timeout.Title == nil || *timeout.Title != gen.GatewayTimeout {
			t.Errorf("got %#v", resp)
		}
	})
}

func TestLoadRawQueryLimits(t *testing.T) {
	t.Setenv("RAW_QUERY_TIMEOUT", "")
	t.Setenv("RAW_QUERY_MAX_RESPONSE_BYTES", "")
	limits, err := loadRawQueryLimits()
	if err != nil || limits != DefaultRawQueryLimits {
		t.Fatalf("defaults: %+v, %v", limits, err)
	}

	t.Setenv("RAW_QUERY_TIMEOUT", "45s")
	t.Setenv("RAW_QUERY_MAX_RESPONSE_BYTES", "2048")
	limits, err = loadRawQueryLimits()
	if err != nil || limits.Timeout != 45*time.Second || limits.MaxResponseBytes != 2048 {
		t.Fatalf("overrides: %+v, %v", limits, err)
	}

	for _, bad := range []map[string]string{
		{"RAW_QUERY_TIMEOUT": "soon"}, {"RAW_QUERY_TIMEOUT": "-1s"}, {"RAW_QUERY_MAX_RESPONSE_BYTES": "0"},
	} {
		t.Setenv("RAW_QUERY_TIMEOUT", "")
		t.Setenv("RAW_QUERY_MAX_RESPONSE_BYTES", "")
		for k, v := range bad {
			t.Setenv(k, v)
		}
		if _, err := loadRawQueryLimits(); err == nil {
			t.Errorf("want an error for %v", bad)
		}
	}
}
