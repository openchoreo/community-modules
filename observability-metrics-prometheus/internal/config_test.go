// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"testing"
)

// setEnvVars sets multiple environment variables for the test.
func setEnvVars(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// validEnvVars returns the minimal set of environment variables required for LoadConfig.
func validEnvVars() map[string]string {
	return map[string]string{
		"OBSERVER_API_INTERNAL_URL": "http://localhost:8080",
	}
}

func TestLoadConfig_Success(t *testing.T) {
	setEnvVars(t, validEnvVars())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ServerPort != "9098" {
		t.Errorf("expected default ServerPort 9098, got %s", cfg.ServerPort)
	}
	if cfg.ObserverAPIInternalURL != "http://localhost:8080" {
		t.Errorf("unexpected ObserverAPIInternalURL: %s", cfg.ObserverAPIInternalURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel Info, got %v", cfg.LogLevel)
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	vars := validEnvVars()
	vars["SERVER_PORT"] = "3000"
	vars["LOG_LEVEL"] = "DEBUG"
	setEnvVars(t, vars)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ServerPort != "3000" {
		t.Errorf("expected ServerPort 3000, got %s", cfg.ServerPort)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected LogLevel Debug, got %v", cfg.LogLevel)
	}
}

func TestLoadConfig_LogLevels(t *testing.T) {
	tests := []struct {
		level    string
		expected slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			vars := validEnvVars()
			vars["LOG_LEVEL"] = tt.level
			setEnvVars(t, vars)

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.LogLevel != tt.expected {
				t.Errorf("for LOG_LEVEL=%s expected %v, got %v", tt.level, tt.expected, cfg.LogLevel)
			}
		})
	}
}

func TestLoadConfig_MissingObserverURL(t *testing.T) {
	// Explicitly set OBSERVER_API_INTERNAL_URL to empty to ensure it's treated as missing
	t.Setenv("OBSERVER_API_INTERNAL_URL", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing OBSERVER_API_INTERNAL_URL, got nil")
	}
}

func TestLoadConfig_InvalidObserverURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "localhost:8080"},
		{"no host", "http://"},
		{"empty after trim", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnvVars(t, map[string]string{
				"OBSERVER_API_INTERNAL_URL": tt.url,
			})

			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("expected error for OBSERVER_API_INTERNAL_URL=%q, got nil", tt.url)
			}
		})
	}
}

func TestLoadConfig_InvalidServerPort(t *testing.T) {
	vars := validEnvVars()
	vars["SERVER_PORT"] = "not-a-number"
	setEnvVars(t, vars)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid SERVER_PORT, got nil")
	}
}

func TestLoadConfig_UnknownLogLevel(t *testing.T) {
	vars := validEnvVars()
	vars["LOG_LEVEL"] = "TRACE"
	setEnvVars(t, vars)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel Info for unknown level, got %v", cfg.LogLevel)
	}
}

func TestLoadConfig_EmptyLogLevel(t *testing.T) {
	vars := validEnvVars()
	vars["LOG_LEVEL"] = ""
	setEnvVars(t, vars)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel Info for empty level, got %v", cfg.LogLevel)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("TEST_GET_ENV_EXISTS", "value")

	if got := getEnv("TEST_GET_ENV_EXISTS", "default"); got != "value" {
		t.Errorf("expected 'value', got %q", got)
	}
	if got := getEnv("TEST_GET_ENV_MISSING", "default"); got != "default" {
		t.Errorf("expected 'default', got %q", got)
	}
}
