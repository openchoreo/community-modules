// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func passThrough(_ http.ResponseWriter, _ *http.Request) {}

func newMiddleware(secret string, enabled bool) func(http.Handler) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return WebhookAuthMiddleware(secret, enabled, logger)
}

func TestWebhookAuth_PassthroughForNonWebhookPaths(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/query", bytes.NewBufferString("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestWebhookAuth_GETIsPassthrough(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodGet, "/api/v1alpha1/alerts/webhook", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestWebhookAuth_RejectsWhenSecretEmpty(t *testing.T) {
	mw := newMiddleware("", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewBufferString("{}"))
	req.Header.Set(WebhookAuthHeader, "anything")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestWebhookAuth_RejectsMissingHeader(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewBufferString("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestWebhookAuth_RejectsWrongHeader(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewBufferString("{}"))
	req.Header.Set(WebhookAuthHeader, "wrongvalue")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestWebhookAuth_AcceptsCorrectHeader(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewBufferString("{}"))
	req.Header.Set(WebhookAuthHeader, "verysecrettokenxx")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestWebhookAuth_AcceptsQueryToken(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook?token=verysecrettokenxx", bytes.NewBufferString("{}"))
	// no header — should still pass via query
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestWebhookAuth_RejectsWrongQueryToken(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook?token=wrong", bytes.NewBufferString("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestWebhookAuth_DisabledMeansPassthrough(t *testing.T) {
	mw := newMiddleware("ignored", false)
	h := mw(http.HandlerFunc(passThrough))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewBufferString("{}"))
	// no header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestWebhookAuth_RejectsOversizedBody(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	called := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))

	big := bytes.Repeat([]byte("a"), (256<<10)+1) // one byte over the cap
	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewReader(big))
	req.Header.Set(WebhookAuthHeader, "verysecrettokenxx")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("want 413, got %d", rec.Code)
	}
	if called {
		t.Error("handler must not be invoked for an oversized body")
	}
}

func TestWebhookAuth_BodyReadableDownstream(t *testing.T) {
	mw := newMiddleware("verysecrettokenxx", true)
	var got string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", bytes.NewBufferString(`{"schemaId":"x"}`))
	req.Header.Set(WebhookAuthHeader, "verysecrettokenxx")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
	if got != `{"schemaId":"x"}` {
		t.Errorf("downstream handler must still read the full body, got %q", got)
	}
}
