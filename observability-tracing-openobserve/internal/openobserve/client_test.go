// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestClient(serverURL string) *Client {
	return NewClient(serverURL, "default", "default", "admin", "token", testLogger())
}

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:5080/", "myorg", "mystream", "user", "pass", testLogger())

	if c.baseURL != "http://localhost:5080" {
		t.Errorf("expected trailing slash removed, got %q", c.baseURL)
	}
	if c.org != "myorg" {
		t.Errorf("unexpected org: %q", c.org)
	}
	if c.stream != "mystream" {
		t.Errorf("unexpected stream: %q", c.stream)
	}
	if c.user != "user" {
		t.Errorf("unexpected user: %q", c.user)
	}
	if c.token != "pass" {
		t.Errorf("unexpected token: %q", c.token)
	}
}

// isCountQuery checks if the request body contains a count query (size=0 and SELECT count).
// It reads the body and replaces it so subsequent reads still work.
func isCountQuery(r *http.Request) bool {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return false
	}
	query, ok := body["query"].(map[string]interface{})
	if !ok {
		return false
	}
	size, _ := query["size"].(float64)
	sql, _ := query["sql"].(string)
	return size == 0 && strings.Contains(strings.ToLower(sql), "count")
}

func TestGetTraces(t *testing.T) {
	startNs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC).UnixNano()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/default/_search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Query().Get("type") != "traces" {
			t.Errorf("expected type=traces query param, got %s", r.URL.Query().Get("type"))
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "token" {
			t.Error("missing or incorrect basic auth")
		}

		if isCountQuery(r) {
			// Return count response for the count query
			resp := OpenObserveResponse{
				Took:  1,
				Total: 1,
				Hits: []map[string]interface{}{
					{"total": json.Number("2")},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			data, _ := json.Marshal(resp)
			w.Write(data)
			return
		}

		resp := OpenObserveResponse{
			Took:  42,
			Total: 3,
			Hits: []map[string]interface{}{
				{
					"trace_id":                 "trace-1",
					"span_id":                  "span-root",
					"operation_name":           "GET /api/users",
					"span_kind":                "SERVER",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
					"reference_parent_span_id": "",
				},
				{
					"trace_id":                 "trace-1",
					"span_id":                  "span-child",
					"operation_name":           "db.query",
					"span_kind":                "CLIENT",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs+1000)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs-1000)),
					"reference_parent_span_id": "span-root",
				},
				{
					"trace_id":                 "trace-2",
					"span_id":                  "span-2-root",
					"operation_name":           "POST /api/orders",
					"span_kind":                "SERVER",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs+5000)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs+5000)),
					"reference_parent_span_id": "",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Scope: Scope{
			Namespace: "test-ns",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("expected 2 traces, got %d", result.Total)
	}
	if result.TookMs != 42 {
		t.Errorf("expected took 42, got %d", result.TookMs)
	}
	if len(result.Traces) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(result.Traces))
	}

	// First trace should have 2 spans grouped
	trace0 := result.Traces[0]
	if trace0.TraceID != "trace-1" {
		t.Errorf("expected traceID 'trace-1', got %q", trace0.TraceID)
	}
	if trace0.SpanCount != 2 {
		t.Errorf("expected spanCount 2, got %d", trace0.SpanCount)
	}
	if trace0.RootSpanID != "span-root" {
		t.Errorf("expected rootSpanID 'span-root', got %q", trace0.RootSpanID)
	}
	if trace0.RootSpanName != "GET /api/users" {
		t.Errorf("expected rootSpanName 'GET /api/users', got %q", trace0.RootSpanName)
	}
	if trace0.RootSpanKind != "SERVER" {
		t.Errorf("expected rootSpanKind 'SERVER', got %q", trace0.RootSpanKind)
	}
	if trace0.TraceName != "GET /api/users" {
		t.Errorf("expected traceName to equal rootSpanName, got %q", trace0.TraceName)
	}

	// Second trace should have 1 span
	trace1 := result.Traces[1]
	if trace1.TraceID != "trace-2" {
		t.Errorf("expected traceID 'trace-2', got %q", trace1.TraceID)
	}
	if trace1.SpanCount != 1 {
		t.Errorf("expected spanCount 1, got %d", trace1.SpanCount)
	}
}

func TestGetTraces_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetTraces(context.Background(), TracesQueryParams{
		Scope: Scope{
			Namespace: "test-ns",
		},
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestGetTraces_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isCountQuery(r) {
			resp := OpenObserveResponse{
				Took:  1,
				Total: 1,
				Hits: []map[string]interface{}{
					{"total": json.Number("0")},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			data, _ := json.Marshal(resp)
			w.Write(data)
			return
		}
		resp := OpenObserveResponse{
			Took:  1,
			Total: 0,
			Hits:  []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		Scope: Scope{
			Namespace: "test-ns",
		},
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 traces, got %d", result.Total)
	}
	if len(result.Traces) != 0 {
		t.Errorf("expected empty traces, got %d", len(result.Traces))
	}
}

func TestGetSpans(t *testing.T) {
	startNs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC).UnixNano()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isCountQuery(r) {
			resp := OpenObserveResponse{
				Took:  1,
				Total: 1,
				Hits: []map[string]interface{}{
					{"total": json.Number("2")},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			data, _ := json.Marshal(resp)
			w.Write(data)
			return
		}

		resp := OpenObserveResponse{
			Took:  10,
			Total: 2,
			Hits: []map[string]interface{}{
				{
					"span_id":                  "span-1",
					"operation_name":           "GET /api/users",
					"span_kind":                "SERVER",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
					"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs)),
					"reference_parent_span_id": "",
				},
				{
					"span_id":                  "span-2",
					"operation_name":           "db.query",
					"span_kind":                "CLIENT",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs+100)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs-100)),
					"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs-200)),
					"reference_parent_span_id": "span-1",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSpans(context.Background(), TracesQueryParams{
		TraceID:   "trace-1",
		StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}
	if result.TookMs != 10 {
		t.Errorf("expected took 10, got %d", result.TookMs)
	}
	if len(result.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(result.Spans))
	}

	span0 := result.Spans[0]
	if span0.SpanID != "span-1" {
		t.Errorf("unexpected spanID: %q", span0.SpanID)
	}
	if span0.SpanName != "GET /api/users" {
		t.Errorf("unexpected spanName: %q", span0.SpanName)
	}
	if span0.SpanKind != "SERVER" {
		t.Errorf("unexpected spanKind: %q", span0.SpanKind)
	}
	if span0.ParentSpanID != "" {
		t.Errorf("expected empty parentSpanID for root span, got %q", span0.ParentSpanID)
	}

	span1 := result.Spans[1]
	if span1.ParentSpanID != "span-1" {
		t.Errorf("expected parentSpanID 'span-1', got %q", span1.ParentSpanID)
	}
}

func TestGetSpans_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetSpans(context.Background(), TracesQueryParams{
		TraceID:   "trace-1",
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestGetSpanDetail(t *testing.T) {
	startNs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC).UnixNano()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := OpenObserveResponse{
			Took:  2,
			Total: 1,
			Hits: []map[string]interface{}{
				{
					"span_id":                  "span-1",
					"operation_name":           "db.query",
					"span_kind":                "CLIENT",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
					"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs)),
					"reference_parent_span_id": "span-root",
					"http.method":              "GET",
					"http.status_code":         "200",
					"service.name":             "my-service",
					"resource.version":         "v1",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSpanDetail(context.Background(), TracesQueryParams{
		TraceID: "trace-1",
		SpanID:  "span-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	span := result.Span
	if span.SpanID != "span-1" {
		t.Errorf("expected spanID 'span-1', got %q", span.SpanID)
	}
	if span.SpanName != "db.query" {
		t.Errorf("expected spanName 'db.query', got %q", span.SpanName)
	}
	if span.SpanKind != "CLIENT" {
		t.Errorf("expected spanKind 'CLIENT', got %q", span.SpanKind)
	}
	if span.ParentSpanID != "span-root" {
		t.Errorf("expected parentSpanID 'span-root', got %q", span.ParentSpanID)
	}
	if span.StartTime != time.Unix(0, startNs) {
		t.Errorf("unexpected startTime: %v", span.StartTime)
	}
	if span.EndTime != time.Unix(0, endNs) {
		t.Errorf("unexpected endTime: %v", span.EndTime)
	}

	// Check that attributes are populated (http.method, http.status_code)
	foundHTTPMethod := false
	for _, attr := range span.Attributes {
		if attr.Key == "http.method" && attr.Value == "GET" {
			foundHTTPMethod = true
		}
	}
	if !foundHTTPMethod {
		t.Error("expected http.method attribute in span attributes")
	}

	// Check that resource attributes are populated (service.name, resource.version)
	foundServiceName := false
	for _, attr := range span.ResourceAttributes {
		if attr.Key == "service.name" && attr.Value == "my-service" {
			foundServiceName = true
		}
	}
	if !foundServiceName {
		t.Error("expected service.name in resource attributes")
	}
}

func TestGetSpanDetail_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := OpenObserveResponse{
			Took:  1,
			Total: 0,
			Hits:  []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetSpanDetail(context.Background(), TracesQueryParams{
		TraceID: "trace-1",
		SpanID:  "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for not found span")
	}
}

func TestParseSpanEntry(t *testing.T) {
	startNs := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 6, 15, 10, 30, 1, 0, time.UTC).UnixNano()

	hit := map[string]interface{}{
		"span_id":                  "span-1",
		"operation_name":           "db.query",
		"span_kind":                "CLIENT",
		"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
		"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
		"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs)),
		"reference_parent_span_id": "span-root",
	}

	entry := parseSpanEntry(hit)

	if entry.SpanID != "span-1" {
		t.Errorf("expected spanID 'span-1', got %q", entry.SpanID)
	}
	if entry.SpanName != "db.query" {
		t.Errorf("expected spanName 'db.query', got %q", entry.SpanName)
	}
	if entry.SpanKind != "CLIENT" {
		t.Errorf("expected spanKind 'CLIENT', got %q", entry.SpanKind)
	}
	if entry.ParentSpanID != "span-root" {
		t.Errorf("expected parentSpanID 'span-root', got %q", entry.ParentSpanID)
	}
	if entry.StartTime != time.Unix(0, startNs) {
		t.Errorf("unexpected startTime: %v", entry.StartTime)
	}
	if entry.EndTime != time.Unix(0, endNs) {
		t.Errorf("unexpected endTime: %v", entry.EndTime)
	}
	if entry.DurationNs != endNs-startNs {
		t.Errorf("expected durationNs %d, got %d", endNs-startNs, entry.DurationNs)
	}
}

func TestParseSpanEntry_MissingFields(t *testing.T) {
	hit := map[string]interface{}{}

	entry := parseSpanEntry(hit)

	if entry.SpanID != "" {
		t.Errorf("expected empty spanID, got %q", entry.SpanID)
	}
	if entry.SpanName != "" {
		t.Errorf("expected empty spanName, got %q", entry.SpanName)
	}
	if entry.DurationNs != 0 {
		t.Errorf("expected zero durationNs, got %d", entry.DurationNs)
	}
}

func TestParseSpanDetail(t *testing.T) {
	startNs := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 6, 15, 10, 30, 1, 0, time.UTC).UnixNano()

	hit := map[string]interface{}{
		"span_id":                  "span-1",
		"operation_name":           "db.query",
		"span_kind":                "CLIENT",
		"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
		"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
		"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs)),
		"reference_parent_span_id": "span-root",
		"trace_id":                 "trace-1",
		"_timestamp":               json.Number("1234567890"),
		"http.method":              "GET",
		"http.status_code":         "200",
		"service.name":             "my-service",
		"resource.version":         "v1",
	}

	detail := parseSpanDetail(hit)

	if detail.SpanID != "span-1" {
		t.Errorf("expected spanID 'span-1', got %q", detail.SpanID)
	}
	if detail.SpanName != "db.query" {
		t.Errorf("expected spanName 'db.query', got %q", detail.SpanName)
	}
	if detail.ParentSpanID != "span-root" {
		t.Errorf("expected parentSpanID 'span-root', got %q", detail.ParentSpanID)
	}

	// Internal fields should be excluded from attributes
	for _, attr := range detail.Attributes {
		for _, internal := range internalFields {
			if attr.Key == internal {
				t.Errorf("internal field %q should not appear in attributes", internal)
			}
		}
	}

	// http.method and http.status_code should be in attributes (not resource)
	attrMap := make(map[string]string)
	for _, attr := range detail.Attributes {
		attrMap[attr.Key] = attr.Value
	}
	if attrMap["http.method"] != "GET" {
		t.Errorf("expected http.method=GET in attributes, got %q", attrMap["http.method"])
	}
	if attrMap["http.status_code"] != "200" {
		t.Errorf("expected http.status_code=200 in attributes, got %q", attrMap["http.status_code"])
	}

	// service.name and resource.version should be in resource attributes
	resAttrMap := make(map[string]string)
	for _, attr := range detail.ResourceAttributes {
		resAttrMap[attr.Key] = attr.Value
	}
	if resAttrMap["service.name"] != "my-service" {
		t.Errorf("expected service.name=my-service in resource attributes, got %q", resAttrMap["service.name"])
	}
	if resAttrMap["resource.version"] != "v1" {
		t.Errorf("expected resource.version=v1 in resource attributes, got %q", resAttrMap["resource.version"])
	}
}

func TestExecuteSearchQuery_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not valid json"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetTraces(context.Background(), TracesQueryParams{
		Scope: Scope{
			Namespace: "test-ns",
		},
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}

func TestGetSpanDetail_InvalidStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with invalid stream name containing semicolon
	client := NewClient(server.URL, "default", "bad;stream", "admin", "token", testLogger())
	_, err := client.GetSpanDetail(context.Background(), TracesQueryParams{
		TraceID: "trace-1",
		SpanID:  "span-1",
	})
	if err == nil {
		t.Fatal("expected error for invalid stream identifier")
	}
	if !strings.Contains(err.Error(), "invalid stream identifier") {
		t.Errorf("expected 'invalid stream identifier' error, got: %v", err)
	}
}

func TestParseSpanDetail_NoExtraAttributes(t *testing.T) {
	hit := map[string]interface{}{
		"span_id":                  "span-1",
		"operation_name":           "test",
		"span_kind":                "SERVER",
		"start_time":               json.Number("1000"),
		"end_time":                 json.Number("2000"),
		"duration":                 json.Number("1000"),
		"reference_parent_span_id": "",
		"trace_id":                 "trace-1",
		"_timestamp":               json.Number("1234"),
	}

	detail := parseSpanDetail(hit)

	if len(detail.Attributes) != 0 {
		t.Errorf("expected 0 attributes, got %d: %v", len(detail.Attributes), detail.Attributes)
	}
	if len(detail.ResourceAttributes) != 0 {
		t.Errorf("expected 0 resource attributes, got %d: %v", len(detail.ResourceAttributes), detail.ResourceAttributes)
	}
}
