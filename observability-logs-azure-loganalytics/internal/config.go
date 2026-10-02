// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

// kqlTableName matches a Log Analytics table name. EVENTS_TABLE is written
// into queries as an identifier, which no quoting can make safe, so anything
// else is refused at startup.
var kqlTableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config holds the runtime configuration for the adapter, populated
// from environment variables.
type Config struct {
	// ServerPort is the HTTP listener port. Default 8080.
	ServerPort string

	// LogLevel for slog. One of debug|info|warn|error. Default info.
	LogLevel slog.Level

	// WorkspaceID is the Log Analytics workspace customerId GUID.
	// REQUIRED.
	WorkspaceID string

	// QueryTimeout caps a single Log Analytics query. Default 30s.
	QueryTimeout time.Duration

	// SubscriptionID is the Azure subscription that hosts the
	// scheduledQueryRules and actionGroups.
	SubscriptionID string

	// ResourceGroup is the RG that holds the rules and action group.
	ResourceGroup string

	// Region is the Azure region for newly created rules (must match
	// the workspace region in practice).
	Region string

	// WorkspaceResourceID is the fully-qualified ARM ID of the Log
	// Analytics workspace. Used as the rule's `scopes`.
	WorkspaceResourceID string

	// ActionGroupID is the ARM ID of the shared Action Group that all
	// rules invoke when they fire.
	ActionGroupID string

	// ObserverURL is where fired alerts are forwarded after the adapter
	// receives them on its webhook.
	ObserverURL string

	// WebhookAuthEnabled toggles the X-OpenChoreo-Webhook-Token check.
	// When true, WebhookSharedSecret must be set.
	WebhookAuthEnabled bool

	// WebhookSharedSecret is the bearer token compared against the
	// X-OpenChoreo-Webhook-Token header.
	WebhookSharedSecret string

	// DefaultEvaluationFrequency is the ISO 8601 duration used when a
	// request omits one. Default PT5M.
	DefaultEvaluationFrequency string

	// DefaultWindowSize is the ISO 8601 duration used when a request
	// omits one. Default PT5M.
	DefaultWindowSize string

	// EventsTable is the table Kubernetes events are read from. Default
	// OTelLogs, where Azure Monitor's native OTLP ingestion writes them.
	EventsTable string

	// EventsScopeName is the instrumentation scope marking a record in
	// EventsTable as a Kubernetes event. Default: the k8s events receiver's.
	EventsScopeName string
}

// LoadConfig reads environment variables and returns a populated Config
// or an error if a required variable is missing or malformed.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ServerPort:                 getEnvDefault("SERVER_PORT", "8080"),
		LogLevel:                   parseLogLevel(getEnvDefault("LOG_LEVEL", "info")),
		WorkspaceID:                strings.TrimSpace(os.Getenv("LOG_ANALYTICS_WORKSPACE_ID")),
		QueryTimeout:               30 * time.Second,
		DefaultEvaluationFrequency: getEnvDefault("DEFAULT_EVALUATION_FREQUENCY", "PT5M"),
		DefaultWindowSize:          getEnvDefault("DEFAULT_WINDOW_SIZE", "PT5M"),
		EventsTable:                getEnvDefault("EVENTS_TABLE", loganalytics.DefaultEventsTable),
		EventsScopeName:            getEnvDefault("EVENTS_SCOPE_NAME", loganalytics.DefaultEventsScopeName),
	}

	if cfg.WorkspaceID == "" {
		return nil, errors.New("LOG_ANALYTICS_WORKSPACE_ID is required")
	}

	if !kqlTableName.MatchString(cfg.EventsTable) {
		return nil, fmt.Errorf("EVENTS_TABLE %q is not a valid table name", cfg.EventsTable)
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
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
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
