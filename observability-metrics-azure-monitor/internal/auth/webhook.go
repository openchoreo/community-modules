// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

const (
	WebhookAuthHeader = "X-OpenChoreo-Webhook-Token"
	WebhookAuthQuery  = "token"
	webhookPath       = "/api/v1alpha1/alerts/webhook"

	// maxWebhookBody caps the Common Alert Schema payload the webhook accepts.
	// The endpoint is publicly reachable (the Action Group posts to it), so an
	// unbounded body is a memory-exhaustion vector. 256 KiB matches the AWS
	// CloudWatch sibling and is well above any real Common Alert Schema payload.
	maxWebhookBody = 256 << 10
)

func WebhookAuthMiddleware(secret string, enabled bool, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != webhookPath {
				next.ServeHTTP(w, r)
				return
			}

			// Cap the body before anything reads it, regardless of auth mode.
			limited := http.MaxBytesReader(w, r.Body, maxWebhookBody)
			body, err := io.ReadAll(limited)
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					logger.Warn("rejecting webhook: body exceeds limit",
						slog.String("path", r.URL.Path),
						slog.Int64("limit", maxWebhookBody),
					)
					http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			_ = limited.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))

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

			supplied := r.Header.Get(WebhookAuthHeader)
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
