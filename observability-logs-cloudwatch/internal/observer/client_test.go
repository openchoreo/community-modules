// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package observer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientTrimsTrailingSlash(t *testing.T) {
	c := NewClient("http://observer.internal/")
	if c.baseURL != "http://observer.internal" {
		t.Fatalf("baseURL = %q, want %q", c.baseURL, "http://observer.internal")
	}
	if c.httpClient == nil || c.httpClient.Timeout == 0 {
		t.Fatal("expected httpClient to be configured with a non-zero timeout")
	}
}

func TestForwardAlertSendsJSONPayload(t *testing.T) {
	var captured struct {
		method      string
		path        string
		contentType string
		body        []byte
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		captured.body = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL)
	ts := time.Date(2026, 4, 23, 10, 0, 5, 0, time.UTC)
	if err := c.ForwardAlert(context.Background(), "rule", "ns", 9.5, ts); err != nil {
		t.Fatalf("ForwardAlert() error = %v", err)
	}

	if captured.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/v1alpha1/alerts/webhook" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.contentType != "application/json" {
		t.Fatalf("content-type = %q", captured.contentType)
	}

	var got alertWebhookRequest
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.RuleName != "rule" || got.RuleNamespace != "ns" || got.AlertValue != 9.5 {
		t.Fatalf("unexpected body fields: %#v", got)
	}
	if !got.AlertTimestamp.Equal(ts) {
		t.Fatalf("timestamp = %s, want %s", got.AlertTimestamp, ts)
	}
}

func TestForwardAlertReturnsErrorOnNon2xxResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL)
	err := c.ForwardAlert(context.Background(), "rule", "ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected non-2xx response to return error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want it to mention status code", err)
	}
}

func TestForwardAlertWrapsTransportFailure(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // unroutable
	c.httpClient.Timeout = 100 * time.Millisecond

	err := c.ForwardAlert(context.Background(), "rule", "ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected transport failure to return error")
	}
	if !strings.Contains(err.Error(), "observer webhook endpoint") {
		t.Fatalf("error = %v, want it wrapped", err)
	}
}

func TestForwardAlertRejectsInvalidBaseURL(t *testing.T) {
	c := NewClient("http://[::1")

	err := c.ForwardAlert(context.Background(), "rule", "ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected invalid URL to return error")
	}
}
