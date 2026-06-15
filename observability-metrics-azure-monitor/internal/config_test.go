// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"testing"
)

func TestLoadConfig_RequiresWorkspaceID(t *testing.T) {
	t.Setenv("LOG_ANALYTICS_WORKSPACE_ID", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected error when workspace ID missing")
	}
}

// setAlertingEnv sets the alerting config vars that LoadConfig now always
// requires, so tests that only care about other behaviour load cleanly.
func setAlertingEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub")
	t.Setenv("AZURE_RESOURCE_GROUP", "rg")
	t.Setenv("WORKSPACE_RESOURCE_ID", "/subscriptions/sub/...")
	t.Setenv("ACTION_GROUP_ID", "/subscriptions/sub/.../actionGroups/ag")
	t.Setenv("OBSERVER_URL", "http://observer:8080")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")
	t.Setenv("WEBHOOK_SHARED_SECRET", "0123456789abcdef")
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("LOG_ANALYTICS_WORKSPACE_ID", "ws-guid")
	setAlertingEnv(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServerPort != "9099" {
		t.Errorf("default port = %q, want 9099", cfg.ServerPort)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("default log level = %v, want info", cfg.LogLevel)
	}
	if cfg.QueryTimeout.Seconds() != 30 {
		t.Errorf("default query timeout = %v, want 30s", cfg.QueryTimeout)
	}
}

func TestLoadConfig_RequiresAzureVars(t *testing.T) {
	t.Setenv("LOG_ANALYTICS_WORKSPACE_ID", "ws-guid")
	// Explicitly blank the alerting vars so the test is deterministic even when
	// the runner environment already has Azure vars set.
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	t.Setenv("AZURE_RESOURCE_GROUP", "")
	t.Setenv("WORKSPACE_RESOURCE_ID", "")
	t.Setenv("ACTION_GROUP_ID", "")
	t.Setenv("OBSERVER_URL", "")
	// No AZURE_* vars set: alerting config is mandatory, so this must error.
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected error when alerting Azure vars missing")
	}
}

func TestLoadConfig_AlertingComplete(t *testing.T) {
	t.Setenv("LOG_ANALYTICS_WORKSPACE_ID", "ws-guid")
	setAlertingEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ActionGroupID == "" || cfg.ObserverURL == "" {
		t.Error("expected alerting config to be populated")
	}
}

func TestLoadConfig_WebhookSecretTooShort(t *testing.T) {
	t.Setenv("LOG_ANALYTICS_WORKSPACE_ID", "ws-guid")
	setAlertingEnv(t)
	t.Setenv("WEBHOOK_SHARED_SECRET", "short")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected error for short webhook secret")
	}
}
