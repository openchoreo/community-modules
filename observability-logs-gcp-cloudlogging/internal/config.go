// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds the runtime configuration for the adapter, populated from
// environment variables.
type Config struct {
	// ServerPort is the HTTP listener port. Default 8080.
	ServerPort string

	// LogLevel for slog. One of debug|info|warn|error. Default info.
	LogLevel slog.Level

	// ProjectID is the GCP project that owns the logs (and, for alerting,
	// the log-based metrics and alert policies). REQUIRED.
	ProjectID string

	// QueryTimeout caps a single Cloud Logging query. Default 30s.
	QueryTimeout time.Duration

	// ObserverURL is where fired alerts are forwarded after the adapter
	// receives them on its webhook. REQUIRED.
	ObserverURL string

	// NotificationChannelID is the resource name of a pre-existing Cloud
	// Monitoring notification channel whose webhook points back at this
	// adapter. Optional; when empty, alert rules are created without a
	// delivery target.
	NotificationChannelID string

	// WebhookAuthEnabled toggles the X-OpenChoreo-Webhook-Token check.
	// When true, WebhookSharedSecret must be set.
	WebhookAuthEnabled bool

	// WebhookSharedSecret is the bearer token compared against the
	// X-OpenChoreo-Webhook-Token header.
	WebhookSharedSecret string
}

// LoadConfig reads environment variables and returns a populated Config or an
// error if a required variable is missing or malformed.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ServerPort:            getEnvDefault("SERVER_PORT", "8080"),
		LogLevel:              parseLogLevel(getEnvDefault("LOG_LEVEL", "info")),
		ProjectID:             strings.TrimSpace(os.Getenv("GCP_PROJECT_ID")),
		QueryTimeout:          30 * time.Second,
		ObserverURL:           strings.TrimSpace(os.Getenv("OBSERVER_URL")),
		NotificationChannelID: strings.TrimSpace(os.Getenv("NOTIFICATION_CHANNEL_ID")),
	}

	missing := []string{}
	if cfg.ProjectID == "" {
		missing = append(missing, "GCP_PROJECT_ID")
	}
	if cfg.ObserverURL == "" {
		missing = append(missing, "OBSERVER_URL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	if v := strings.TrimSpace(os.Getenv("QUERY_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("QUERY_TIMEOUT: %w", err)
		}
		cfg.QueryTimeout = d
	}

	cfg.WebhookAuthEnabled = strings.EqualFold(getEnvDefault("WEBHOOK_AUTH_ENABLED", "true"), "true")
	cfg.WebhookSharedSecret = os.Getenv("WEBHOOK_SHARED_SECRET")
	if cfg.WebhookAuthEnabled && len(cfg.WebhookSharedSecret) < 16 {
		return nil, errors.New("WEBHOOK_SHARED_SECRET must be at least 16 bytes when WEBHOOK_AUTH_ENABLED=true")
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
