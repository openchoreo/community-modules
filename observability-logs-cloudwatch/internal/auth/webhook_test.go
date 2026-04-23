// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/cloudwatch"
)

func TestWebhookAuthMiddlewarePassesThroughOtherPaths(t *testing.T) {
	nextCalled := false
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status %d", rec.Code)
	}
	if !nextCalled {
		t.Fatal("expected middleware to pass through non-webhook path")
	}
}

func TestWebhookAuthMiddlewareAcceptsHeaderAuthenticatedWebhook(t *testing.T) {
	nextCalled := false
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", strings.NewReader(`{"alarmName":"oc-1","ruleName":"r","state":"ALARM"}`))
	req.Header.Set(WebhookAuthHeader, "0123456789abcdef")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rec.Code)
	}
	if !nextCalled {
		t.Fatal("expected request to reach next handler")
	}
}

func TestWebhookAuthMiddlewareRejectsMissingHeader(t *testing.T) {
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", strings.NewReader(`{"alarmName":"oc-1","ruleName":"r","state":"ALARM"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status %d", rec.Code)
	}
}

func TestWebhookAuthMiddlewareRejectsWrongHeader(t *testing.T) {
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", strings.NewReader(`{"alarmName":"oc-1","ruleName":"r","state":"ALARM"}`))
	req.Header.Set(WebhookAuthHeader, "wrong-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status %d", rec.Code)
	}
}

func TestWebhookAuthMiddlewareAcceptsValidSNSWithoutHeader(t *testing.T) {
	nextCalled := false
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), func(*cloudwatch.SNSEnvelopeResult) error {
		return nil
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", strings.NewReader(validSNSBody()))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rec.Code)
	}
	if !nextCalled {
		t.Fatal("expected valid SNS request to reach next handler")
	}
}

func TestWebhookAuthMiddlewareRejectsInvalidSNSDespiteHeader(t *testing.T) {
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), func(*cloudwatch.SNSEnvelopeResult) error {
		return errors.New("bad signature")
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", strings.NewReader(validSNSBody()))
	req.Header.Set(WebhookAuthHeader, "0123456789abcdef")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status %d", rec.Code)
	}
}

func TestWebhookAuthMiddlewareRejectsOversizedBody(t *testing.T) {
	mw := WebhookAuthMiddleware("0123456789abcdef", true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/alerts/webhook", strings.NewReader(strings.Repeat("a", maxWebhookBody+1)))
	req.Header.Set(WebhookAuthHeader, "0123456789abcdef")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("unexpected status %d", rec.Code)
	}
}

func validSNSBody() string {
	return `{
		"Type": "Notification",
		"MessageId": "msg-1",
		"TopicArn": "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp": "2026-04-23T10:00:00Z",
		"SignatureVersion": "1",
		"Signature": "placeholder",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message": "{\"AlarmName\":\"oc-logs-alert-ns.cGF5bWVudHM.rn.aGlnaC1lcnJvci1yYXRl.hash\",\"NewStateValue\":\"ALARM\",\"NewStateReason\":\"Threshold Crossed: 1 datapoint [5.0 (23/04/26 10:00:00)] was greater than the threshold (3.0).\",\"StateChangeTime\":\"2026-04-23T10:00:05Z\"}"
	}`
}
