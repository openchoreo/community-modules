// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"os"
	"testing"
)

// setEnvVars sets multiple environment variables and returns a cleanup function.
func setEnvVars(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// validEnvVars returns the minimal set of environment variables required for LoadConfig.
func validEnvVars() map[string]string {
	return map[string]string{
		"OPENOBSERVE_URL":      "http://localhost:5080",
		"OPENOBSERVE_USER":     "admin",
		"OPENOBSERVE_PASSWORD": "fakeOpenObservePassword",
		"OBSERVER_URL":         "http://observer-internal:8081",
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
	if cfg.OpenObserveURL != "http://localhost:5080" {
		t.Errorf("unexpected OpenObserveURL: %s", cfg.OpenObserveURL)
	}
	if cfg.OpenObserveOrg != "default" {
		t.Errorf("expected default OpenObserveOrg, got %s", cfg.OpenObserveOrg)
	}
	if cfg.OpenObserveStream != "default" {
		t.Errorf("expected default OpenObserveStream, got %s", cfg.OpenObserveStream)
	}
	if cfg.OpenObserveUser != "admin" {
		t.Errorf("unexpected OpenObserveUser: %s", cfg.OpenObserveUser)
	}
	if cfg.OpenObservePassword != "fakeOpenObservePassword" {
		t.Errorf("unexpected OpenObservePassword: %s", cfg.OpenObservePassword)
	}
	if cfg.ObserverURL != "http://observer-internal:8081" {
		t.Errorf("unexpected ObserverURL: %s", cfg.ObserverURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel Info, got %v", cfg.LogLevel)
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	vars := validEnvVars()
	vars["SERVER_PORT"] = "3000"
	vars["OPENOBSERVE_ORG"] = "myorg"
	vars["OPENOBSERVE_STREAM"] = "mystream"
	setEnvVars(t, vars)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ServerPort != "3000" {
		t.Errorf("expected ServerPort 3000, got %s", cfg.ServerPort)
	}
	if cfg.OpenObserveOrg != "myorg" {
		t.Errorf("expected OpenObserveOrg myorg, got %s", cfg.OpenObserveOrg)
	}
	if cfg.OpenObserveStream != "mystream" {
		t.Errorf("expected OpenObserveStream mystream, got %s", cfg.OpenObserveStream)
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

func TestLoadConfig_MissingRequired(t *testing.T) {
	tests := []struct {
		name    string
		unset   string
		wantErr string
	}{
		{"missing OPENOBSERVE_URL", "OPENOBSERVE_URL", "OPENOBSERVE_URL"},
		{"missing OPENOBSERVE_USER", "OPENOBSERVE_USER", "OPENOBSERVE_USER"},
		{"missing OPENOBSERVE_PASSWORD", "OPENOBSERVE_PASSWORD", "OPENOBSERVE_PASSWORD"},
		{"missing OBSERVER_URL", "OBSERVER_URL", "OBSERVER_URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := validEnvVars()
			delete(vars, tt.unset)
			setEnvVars(t, vars)
			// Ensure the unset variable is actually cleared
			os.Unsetenv(tt.unset)

			_, err := LoadConfig()
			if err == nil {
				t.Fatal("expected error, got nil")
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

func TestLoadConfig_InvalidObserverURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "localhost:8080"},
		{"no host", "http://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := validEnvVars()
			vars["OBSERVER_URL"] = tt.url
			setEnvVars(t, vars)

			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("expected error for OBSERVER_URL=%q, got nil", tt.url)
			}
		})
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
