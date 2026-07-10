// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds the runtime configuration for the adapter, populated from
// environment variables.
type Config struct {
	// ServerPort is the HTTP listener port. Default 9099.
	ServerPort string

	// LogLevel for slog. One of debug|info|warn|error. Default info.
	LogLevel slog.Level

	// ProjectID is the GCP project whose Cloud Monitoring time series are
	// queried. REQUIRED.
	ProjectID string

	// QueryTimeout caps a single metrics query (all six sub-queries share
	// it). Default 30s.
	QueryTimeout time.Duration

	// Alert rule management. Alerting is enabled only when both ObserverURL and
	// NotificationChannelID are set. When either is empty the adapter leaves the
	// alert client nil and the alert-rule endpoints keep answering "not
	// implemented" (nil-means-disabled, matching the Azure/AWS siblings).

	// ObserverURL is the base URL of the OpenChoreo Observer that fired alerts
	// are forwarded to (its /api/v1alpha1/alerts/webhook endpoint). Optional.
	ObserverURL string

	// NotificationChannelID is a *pre-configured* Cloud Monitoring notification
	// channel (full resource name
	// projects/<id>/notificationChannels/<n>) attached to every managed alert
	// policy. It must already exist; the adapter verifies it at boot rather
	// than creating it. Optional.
	NotificationChannelID string

	// AlertEvaluationInterval is the default condition duration applied when a
	// rule omits condition.interval. Default 60s.
	AlertEvaluationInterval time.Duration

	// AlertWindow is the default alignment period applied when a rule omits
	// condition.window. Default 300s.
	AlertWindow time.Duration

	// WebhookAuthEnabled guards the publicly exposed alert webhook path with a
	// shared-secret check. Default true; when true WebhookSharedSecret must be
	// set (>=16 bytes).
	WebhookAuthEnabled bool

	// WebhookSharedSecret is the token the notification channel presents on the
	// inbound webhook (Basic-auth password, X-OpenChoreo-Webhook-Token header,
	// or ?token= query param).
	WebhookSharedSecret string
}

// AlertingEnabled reports whether alert rule management should be wired. Both
// the Observer URL and a pre-configured notification channel are required.
func (c *Config) AlertingEnabled() bool {
	return c.ObserverURL != "" && c.NotificationChannelID != ""
}

// LoadConfig reads environment variables and returns a populated Config or an
// error if a required variable is missing or malformed.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ServerPort:              getEnvDefault("SERVER_PORT", "9099"),
		LogLevel:                parseLogLevel(getEnvDefault("LOG_LEVEL", "info")),
		ProjectID:               strings.TrimSpace(os.Getenv("GCP_PROJECT_ID")),
		QueryTimeout:            30 * time.Second,
		ObserverURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("OBSERVER_URL")), "/"),
		NotificationChannelID:   strings.TrimSpace(os.Getenv("NOTIFICATION_CHANNEL_ID")),
		AlertEvaluationInterval: 60 * time.Second,
		AlertWindow:             300 * time.Second,
	}

	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("missing required env vars: GCP_PROJECT_ID")
	}

	if v := strings.TrimSpace(os.Getenv("QUERY_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("QUERY_TIMEOUT: %w", err)
		}
		cfg.QueryTimeout = d
	}

	if v := strings.TrimSpace(os.Getenv("ALERT_EVALUATION_INTERVAL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("ALERT_EVALUATION_INTERVAL: %w", err)
		}
		cfg.AlertEvaluationInterval = d
	}

	if v := strings.TrimSpace(os.Getenv("ALERT_WINDOW")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("ALERT_WINDOW: %w", err)
		}
		cfg.AlertWindow = d
	}

	cfg.WebhookAuthEnabled = strings.EqualFold(getEnvDefault("WEBHOOK_AUTH_ENABLED", "true"), "true")
	cfg.WebhookSharedSecret = os.Getenv("WEBHOOK_SHARED_SECRET")
	// The shared secret is only required when alerting is actually enabled;
	// a metrics-only deploy has no webhook path to guard.
	if cfg.AlertingEnabled() && cfg.WebhookAuthEnabled && len(cfg.WebhookSharedSecret) < 16 {
		return nil, fmt.Errorf("WEBHOOK_SHARED_SECRET must be at least 16 bytes when WEBHOOK_AUTH_ENABLED=true and alerting is enabled")
	}

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
