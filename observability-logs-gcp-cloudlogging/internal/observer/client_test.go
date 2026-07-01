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

func TestForwardAlert_PostsExpectedShape(t *testing.T) {
	var got alertWebhookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1alpha1/alerts/webhook" {
			t.Errorf("path: want /api/v1alpha1/alerts/webhook, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: want POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ts := time.Date(2026, 5, 28, 5, 30, 0, 0, time.UTC)
	if err := c.ForwardAlert(context.Background(), "rule-x", "ns-y", 42, ts); err != nil {
		t.Fatalf("ForwardAlert: %v", err)
	}

	if got.RuleName != "rule-x" {
		t.Errorf("ruleName: got %q", got.RuleName)
	}
	if got.RuleNamespace != "ns-y" {
		t.Errorf("ruleNamespace: got %q", got.RuleNamespace)
	}
	if got.AlertValue != 42 {
		t.Errorf("alertValue: got %v", got.AlertValue)
	}
	if !got.AlertTimestamp.Equal(ts) {
		t.Errorf("alertTimestamp: got %v want %v", got.AlertTimestamp, ts)
	}
}

func TestForwardAlert_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream broken"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.ForwardAlert(context.Background(), "r", "n", 1, time.Now())
	if err == nil {
		t.Fatal("expected error for 502")
	}
}
