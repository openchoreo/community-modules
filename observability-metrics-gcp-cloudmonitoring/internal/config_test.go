// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "my-project")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServerPort != "9099" {
		t.Errorf("ServerPort = %q, want 9099", cfg.ServerPort)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.QueryTimeout != 30*time.Second {
		t.Errorf("QueryTimeout = %v, want 30s", cfg.QueryTimeout)
	}
}

func TestLoadConfigMissingProject(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected error for missing GCP_PROJECT_ID")
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "my-project")
	t.Setenv("SERVER_PORT", "9200")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("QUERY_TIMEOUT", "45s")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServerPort != "9200" || cfg.LogLevel != slog.LevelDebug || cfg.QueryTimeout != 45*time.Second {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadConfigBadTimeout(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "my-project")
	t.Setenv("QUERY_TIMEOUT", "abc")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected error for malformed QUERY_TIMEOUT")
	}
}
