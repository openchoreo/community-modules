// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package observer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestForwardAlertPostsPayload(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL + "/")
	ts := time.Date(2026, 7, 8, 5, 0, 0, 0, time.UTC)
	if err := c.ForwardAlert(context.Background(), "high-cpu", "default", 0.9, ts); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if gotPath != "/api/v1alpha1/alerts/webhook" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["ruleName"] != "high-cpu" || gotBody["ruleNamespace"] != "default" {
		t.Errorf("body identity = %+v", gotBody)
	}
	if gotBody["alertValue"] != 0.9 {
		t.Errorf("alertValue = %v", gotBody["alertValue"])
	}
}

func TestForwardAlertNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.ForwardAlert(context.Background(), "r", "ns", 1, time.Now()); err == nil {
		t.Errorf("expected error on 500 response")
	}
}
