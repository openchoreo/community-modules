// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueryAllocations(t *testing.T) {
	const responseBody = `{
	  "code": 200,
	  "data": [
	    {
	      "pod-a": {
	        "name": "pod-a",
	        "properties": {
	          "namespace": "dp-default",
	          "labels": {
	            "openchoreo_dev_component_uid": "14e6fe2a-820a-481e-9a96-018e86a241fa",
	            "openchoreo_dev_component": "checkout"
	          }
	        },
	        "cpuCost": 1.5,
	        "ramCost": 0.5,
	        "cpuEfficiency": 0.4,
	        "ramEfficiency": 0.6,
	        "totalEfficiency": 0.45,
	        "cpuCoreHours": 2.0,
	        "ramByteHours": 100.0
	      }
	    }
	  ]
	}`

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/allocation/compute" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, responseBody)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sets, err := c.QueryAllocations(context.Background(), AllocationQuery{
		Namespace:      "default",
		EnvironmentUID: "3d9d7f27-f0ab-4310-ae0b-4980f4ccd302",
		Start:          time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:            time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("QueryAllocations error: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("expected 1 allocation set, got %d", len(sets))
	}
	alloc, ok := sets[0]["pod-a"]
	if !ok {
		t.Fatalf("pod-a not found in set")
	}
	if alloc.CPUCost != 1.5 || alloc.RAMCost != 0.5 {
		t.Errorf("unexpected costs: cpu=%v ram=%v", alloc.CPUCost, alloc.RAMCost)
	}
	if alloc.Label(LabelComponentUID) != "14e6fe2a-820a-481e-9a96-018e86a241fa" {
		t.Errorf("unexpected component uid label: %q", alloc.Label(LabelComponentUID))
	}
	if !contains(gotQuery, "accumulate=true") {
		t.Errorf("expected accumulate=true in query, got %q", gotQuery)
	}
}

func TestQueryAllocationsWithStep(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"code":200,"data":[]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := c.QueryAllocations(context.Background(), AllocationQuery{
		Namespace: "default",
		Start:     time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		Step:      "1d",
	})
	if err != nil {
		t.Fatalf("QueryAllocations error: %v", err)
	}
	if !contains(gotQuery, "step=1d") {
		t.Errorf("expected step=1d in query, got %q", gotQuery)
	}
	if !contains(gotQuery, "accumulate=false") {
		t.Errorf("expected accumulate=false in query, got %q", gotQuery)
	}
}

func TestQueryAllocationsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := c.QueryAllocations(context.Background(), AllocationQuery{}); err == nil {
		t.Fatalf("expected error for non-2xx status")
	}
}

func TestQueryAllocationsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{not json")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := c.QueryAllocations(context.Background(), AllocationQuery{}); err == nil {
		t.Fatalf("expected error for bad JSON")
	}
}

func TestQueryAllocationsRequestError(t *testing.T) {
	// Connection to a closed server surfaces a transport error.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := NewClient(url, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := c.QueryAllocations(context.Background(), AllocationQuery{}); err == nil {
		t.Fatalf("expected transport error against closed server")
	}
}

func TestNewClientTrimsTrailingSlash(t *testing.T) {
	c := NewClient("http://opencost:9003/", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c.baseURL != "http://opencost:9003" {
		t.Errorf("baseURL = %q, want trimmed", c.baseURL)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
