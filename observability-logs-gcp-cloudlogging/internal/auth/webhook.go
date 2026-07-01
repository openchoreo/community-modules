// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
)

const (
	WebhookAuthHeader = "X-OpenChoreo-Webhook-Token"
	WebhookAuthQuery  = "token"
	webhookPath       = "/api/v1alpha1/alerts/webhook"
)

// WebhookAuthMiddleware checks the shared-secret header on the webhook path.
// When `enabled` is false the middleware is a passthrough. When enabled and
// the secret is empty, all webhook calls are rejected.
func WebhookAuthMiddleware(secret string, enabled bool, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != webhookPath {
				next.ServeHTTP(w, r)
				return
			}
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}
			if secret == "" {
				logger.Warn("rejecting webhook: auth enabled but no shared secret configured",
					slog.String("path", r.URL.Path),
				)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// Accept the secret from any of three carriers, in order:
			//  1. HTTP Basic auth password — used by a GCP Cloud Monitoring
			//     webhook_basicauth notification channel, which sends
			//     Authorization: Basic base64(user:pass) and is the header-based
			//     path we prefer.
			//  2. The X-OpenChoreo-Webhook-Token header — used by a forwarder
			//     that can inject custom headers.
			//  3. The `token` URL query parameter — fallback for plain webhook
			//     receivers that cannot set custom headers (e.g. GCP
			//     webhook_tokenauth, Azure Action Group webhook receivers).
			supplied := ""
			if _, password, ok := r.BasicAuth(); ok && password != "" {
				supplied = password
			}
			if supplied == "" {
				supplied = r.Header.Get(WebhookAuthHeader)
			}
			if supplied == "" {
				supplied = r.URL.Query().Get(WebhookAuthQuery)
			}
			if !constantTimeStringEqual(supplied, secret) {
				logger.Warn("rejecting webhook: missing or invalid auth token",
					slog.String("path", r.URL.Path),
				)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// constantTimeStringEqual compares two strings without leaking length info.
func constantTimeStringEqual(a, b string) bool {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	aBuf := make([]byte, maxLen)
	bBuf := make([]byte, maxLen)
	copy(aBuf, a)
	copy(bBuf, b)
	return subtle.ConstantTimeCompare(aBuf, bBuf) == 1 && len(a) == len(b)
}
