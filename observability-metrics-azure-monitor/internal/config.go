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

type Config struct {
	// ServerPort is the HTTP listener port. Default 9099
	ServerPort string

	// LogLevel for slog. One of debug|info|warn|error. Default info.
	LogLevel slog.Level

	// WorkspaceID is the Log Analytics workspace customerId GUID. REQUIRED.
	WorkspaceID string

	// QueryTimeout caps a single Log Analytics query. Default 30s.
	QueryTimeout time.Duration

	SubscriptionID      string
	ResourceGroup       string
	Region              string
	WorkspaceResourceID string
	ActionGroupID       string
	ObserverURL         string

	WebhookAuthEnabled  bool
	WebhookSharedSecret string

	DefaultEvaluationFrequency string
	DefaultWindowSize          string
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		ServerPort:                 getEnvDefault("SERVER_PORT", "9099"),
		LogLevel:                   parseLogLevel(getEnvDefault("LOG_LEVEL", "info")),
		WorkspaceID:                strings.TrimSpace(os.Getenv("LOG_ANALYTICS_WORKSPACE_ID")),
		QueryTimeout:               30 * time.Second,
		DefaultEvaluationFrequency: getEnvDefault("DEFAULT_EVALUATION_FREQUENCY", "PT5M"),
		DefaultWindowSize:          getEnvDefault("DEFAULT_WINDOW_SIZE", "PT5M"),
	}

	if cfg.WorkspaceID == "" {
		return nil, errors.New("LOG_ANALYTICS_WORKSPACE_ID is required")
	}

	if v := strings.TrimSpace(os.Getenv("QUERY_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("QUERY_TIMEOUT: %w", err)
		}
		cfg.QueryTimeout = d
	}

	cfg.SubscriptionID = strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	cfg.ResourceGroup = strings.TrimSpace(os.Getenv("AZURE_RESOURCE_GROUP"))
	cfg.Region = strings.TrimSpace(getEnvDefault("AZURE_REGION", "eastus2"))
	cfg.WorkspaceResourceID = strings.TrimSpace(os.Getenv("WORKSPACE_RESOURCE_ID"))
	cfg.ActionGroupID = strings.TrimSpace(os.Getenv("ACTION_GROUP_ID"))
	cfg.ObserverURL = strings.TrimSpace(os.Getenv("OBSERVER_URL"))

	missing := []string{}
	if cfg.SubscriptionID == "" {
		missing = append(missing, "AZURE_SUBSCRIPTION_ID")
	}
	if cfg.ResourceGroup == "" {
		missing = append(missing, "AZURE_RESOURCE_GROUP")
	}
	if cfg.WorkspaceResourceID == "" {
		missing = append(missing, "WORKSPACE_RESOURCE_ID")
	}
	if cfg.ActionGroupID == "" {
		missing = append(missing, "ACTION_GROUP_ID")
	}
	if cfg.ObserverURL == "" {
		missing = append(missing, "OBSERVER_URL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required alerting config: %s", strings.Join(missing, ", "))
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
