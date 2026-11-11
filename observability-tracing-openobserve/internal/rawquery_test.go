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

	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/openobserve"
)

func rawRequest(body string) gen.QueryRawTracesRequestObject {
	b := gen.QueryRawTracesJSONRequestBody(body)
	return gen.QueryRawTracesRequestObject{JSONBody: &b}
}

const rawBody = `{"query":{"sql":"SELECT trace_id FROM \"default\" LIMIT 1",` +
	`"start_time":1792483200000000,"end_time":1792486800000000}}`

// rawHandler returns a handler whose raw query client connects, as
// rawQueryUser, to a server answering status and body. The adapter's own
// client points nowhere, so a raw query that used it would fail.
func rawHandler(t *testing.T, status int, body string, limits RawQueryLimits) (*TracingHandler, *string) {
	t.Helper()
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, _ := r.BasicAuth(); user != rawQueryUser {
			http.Error(w, "Unauthorized Access", http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	adapterClient := openobserve.NewClient("http://adapter-client.invalid", "default", "default", "admin", "pass", testLogger())
	rawClient := openobserve.NewClient(srv.URL, "default", "default", rawQueryUser, "pass", testLogger())
	return NewTracingHandler(adapterClient, testLogger()).WithRawQuery(rawClient, limits), &gotBody
}

const rawQueryUser = "traces-reader@openchoreo.localhost"

func TestQueryRawTraces_NotSupportedWithoutTheRawQueryUser(t *testing.T) {
	h := NewTracingHandler(nil, testLogger())
	resp, err := h.QueryRawTraces(context.Background(), rawRequest(rawBody))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QueryRawTraces501JSONResponse); !ok {
		t.Errorf("got %#v, want 501", resp)
	}
}

func TestQueryRawTraces_ReturnsOpenObservesResponse(t *testing.T) {
	const response = `{"took":9,"hits":[{"trace_id":"t1"}],"total":1}`
	h, gotBody := rawHandler(t, http.StatusOK, response, DefaultRawQueryLimits)

	resp, err := h.QueryRawTraces(context.Background(), rawRequest(rawBody))
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
		t.Errorf("the response must be OpenObserve's, unchanged: %s", got)
	}
	want := `{"query":{"sql":"SELECT trace_id FROM \"default\" LIMIT 1",` +
		`"start_time":1792483200000000,"end_time":1792486800000000},"timeout":30}`
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
		if !ok || unsupported.Detail == nil || *unsupported.Detail != "send an OpenObserve search body as application/json" {
			t.Errorf("got %#v, want 415", resp)
		}
	}
}

func TestQueryRawTraces_RejectsInvalidBodies(t *testing.T) {
	h, _ := rawHandler(t, http.StatusOK, `{}`, DefaultRawQueryLimits)
	tests := []struct {
		name   string
		body   string
		detail string
	}{
		{"a JSON string", `"SELECT 1"`,
			"invalid OpenObserve search request: the request must be an OpenObserve search body (a JSON object)"},
		{"no SQL", `{"query":{"start_time":1792483200000000}}`,
			"invalid OpenObserve search request: query.sql must be a non-empty SQL string"},
		{"no query", `{"timeout":5}`,
			"invalid OpenObserve search request: query must be an object with the SQL in query.sql"},
		{"another cluster", `{"query":{"sql":"SELECT 1"},"clusters":["eu"]}`,
			`invalid OpenObserve search request: "clusters" is not allowed in a raw query`},
		{"a VRL function", `{"query":{"sql":"SELECT 1","query_fn":"."}}`,
			`invalid OpenObserve search request: "query.query_fn" is not allowed in a raw query`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := h.QueryRawTraces(context.Background(), rawRequest(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			bad, ok := resp.(gen.QueryRawTraces400JSONResponse)
			if !ok || bad.Detail == nil || *bad.Detail != tt.detail {
				t.Errorf("got %#v, want 400 %q", resp, tt.detail)
			}
		})
	}
}

func TestQueryRawTraces_MapsOpenObserveErrors(t *testing.T) {
	t.Run("SQL error", func(t *testing.T) {
		h, _ := rawHandler(t, http.StatusBadRequest, `{"code":400,"message":"sql parser error"}`, DefaultRawQueryLimits)
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(rawBody))
		bad, ok := resp.(gen.QueryRawTraces400JSONResponse)
		if !ok || *bad.Detail != "invalid OpenObserve search request: sql parser error" {
			t.Errorf("got %#v", resp)
		}
		if bad.ErrorCode != nil {
			t.Errorf("a rejected query must not carry the result too large code, got %q", *bad.ErrorCode)
		}
	})
	t.Run("too large", func(t *testing.T) {
		h, _ := rawHandler(t, http.StatusOK, `{"took":1,"hits":["`+strings.Repeat("x", 2048)+`"]}`,
			RawQueryLimits{Timeout: time.Second, MaxResponseBytes: 1024})
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(rawBody))
		bad, ok := resp.(gen.QueryRawTraces400JSONResponse)
		if !ok || !strings.Contains(*bad.Detail, "add a LIMIT") {
			t.Errorf("got %#v", resp)
		}
		if bad.ErrorCode == nil || *bad.ErrorCode != "RAW_QUERY_RESULT_TOO_LARGE" {
			t.Errorf("a result too large must carry its errorCode, got %v", bad.ErrorCode)
		}
	})
	t.Run("the raw query user is rejected", func(t *testing.T) {
		h, _ := rawHandler(t, http.StatusUnauthorized, `Unauthorized Access`, DefaultRawQueryLimits)
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(rawBody))
		failed, ok := resp.(gen.QueryRawTraces500JSONResponse)
		if !ok || *failed.Detail != "internal server error" {
			t.Errorf("a credentials problem is the module's, not the query's: got %#v", resp)
		}
	})
	t.Run("partial result", func(t *testing.T) {
		h, _ := rawHandler(t, http.StatusOK, `{"took":1,"hits":[],"is_partial":true}`, DefaultRawQueryLimits)
		resp, _ := h.QueryRawTraces(context.Background(), rawRequest(rawBody))
		if _, ok := resp.(gen.QueryRawTraces504JSONResponse); !ok {
			t.Errorf("got %#v", resp)
		}
	})
}

func TestLoadRawQueryLimits(t *testing.T) {
	t.Setenv("RAW_QUERY_TIMEOUT", "")
	t.Setenv("RAW_QUERY_MAX_RESPONSE_BYTES", "")
	if limits, err := loadRawQueryLimits(); err != nil || limits != DefaultRawQueryLimits {
		t.Fatalf("defaults: %+v, %v", limits, err)
	}
	t.Setenv("RAW_QUERY_TIMEOUT", "1m")
	t.Setenv("RAW_QUERY_MAX_RESPONSE_BYTES", "4096")
	if limits, err := loadRawQueryLimits(); err != nil || limits.Timeout != time.Minute || limits.MaxResponseBytes != 4096 {
		t.Fatalf("overrides: %+v, %v", limits, err)
	}
	t.Setenv("RAW_QUERY_MAX_RESPONSE_BYTES", "lots")
	if _, err := loadRawQueryLimits(); err == nil {
		t.Error("want an error for an invalid size")
	}
}
