// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/cloudwatch"
)

const (
	WebhookAuthHeader = "X-OpenChoreo-Webhook-Token"
	webhookPath       = "/api/v1alpha1/alerts/webhook"
	maxWebhookBody    = 256 << 10
)

// WebhookAuthMiddleware authenticates requests for the public webhook path.
// SNS envelopes are verified using the AWS-published signature; other webhook
// shapes require a static shared secret when enabled.
func WebhookAuthMiddleware(secret string, secretEnabled bool, logger *slog.Logger, verifySNS func(*cloudwatch.SNSEnvelopeResult) error) func(http.Handler) http.Handler {
	if verifySNS == nil {
		verifySNS = cloudwatch.VerifySNSMessageSignature
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != webhookPath {
				next.ServeHTTP(w, r)
				return
			}

			limitedBody := http.MaxBytesReader(w, r.Body, maxWebhookBody)
			body, err := io.ReadAll(limitedBody)
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			_ = limitedBody.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))

			if snsType, ok := peekSNSType(body); ok && snsType != "" {
				env, err := cloudwatch.ParseSNSEnvelope(body)
				if err != nil || verifySNS(env) != nil {
					logger.Warn("Rejecting webhook request: invalid SNS signature",
						slog.String("path", r.URL.Path),
						slog.String("snsType", snsType),
					)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if secretEnabled && !constantTimeStringEqual(r.Header.Get(WebhookAuthHeader), secret) {
				logger.Warn("Rejecting webhook request: missing or invalid auth token",
					slog.String("path", r.URL.Path),
				)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func peekSNSType(body []byte) (string, bool) {
	var probe struct {
		Type string `json:"Type"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", false
	}
	return probe.Type, true
}

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
