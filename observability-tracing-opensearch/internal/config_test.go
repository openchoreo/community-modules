// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"testing"
)

func setEnvVars(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func clearEnvVars(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SERVER_PORT", "OPENSEARCH_ADDRESS", "OPENSEARCH_USERNAME",
		"OPENSEARCH_PASSWORD", "OPENSEARCH_INDEX_PREFIX", "OPENSEARCH_TLS_SKIP_VERIFY", "LOG_LEVEL",
	} {
		t.Setenv(key, "")
	}
}

func requiredEnvVars() map[string]string {
	return map[string]string{
		"OPENSEARCH_ADDRESS":  "https://opensearch:9200",
		"OPENSEARCH_USERNAME": "admin",
		"OPENSEARCH_PASSWORD": "admin123",
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearEnvVars(t)
	setEnvVars(t, requiredEnvVars())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ServerPort != "9100" {
		t.Errorf("expected default server port '9100', got %q", cfg.ServerPort)
	}
	if cfg.OpenSearchIndexPrefix != "otel-traces-" {
		t.Errorf("expected default index prefix 'otel-traces-', got %q", cfg.OpenSearchIndexPrefix)
	}
	if !cfg.OpenSearchTLSSkipVerify {
		t.Error("expected default TLS skip verify true")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default log level INFO, got %v", cfg.LogLevel)
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	clearEnvVars(t)
	setEnvVars(t, map[string]string{
		"SERVER_PORT":              "8080",
		"OPENSEARCH_ADDRESS":      "https://my-os:9200",
		"OPENSEARCH_USERNAME":     "myuser",
		"OPENSEARCH_PASSWORD":     "mypass",
		"OPENSEARCH_INDEX_PREFIX": "custom-traces-",
		"OPENSEARCH_TLS_SKIP_VERIFY": "false",
		"LOG_LEVEL":               "DEBUG",
	})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ServerPort != "8080" {
		t.Errorf("expected server port '8080', got %q", cfg.ServerPort)
	}
	if cfg.OpenSearchAddress != "https://my-os:9200" {
		t.Errorf("expected address 'https://my-os:9200', got %q", cfg.OpenSearchAddress)
	}
	if cfg.OpenSearchUsername != "myuser" {
		t.Errorf("expected username 'myuser', got %q", cfg.OpenSearchUsername)
	}
	if cfg.OpenSearchPassword != "mypass" {
		t.Errorf("expected password 'mypass', got %q", cfg.OpenSearchPassword)
	}
	if cfg.OpenSearchIndexPrefix != "custom-traces-" {
		t.Errorf("expected index prefix 'custom-traces-', got %q", cfg.OpenSearchIndexPrefix)
	}
	if cfg.OpenSearchTLSSkipVerify {
		t.Error("expected TLS skip verify false")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected log level DEBUG, got %v", cfg.LogLevel)
	}
}

func TestLoadConfig_MissingAddress(t *testing.T) {
	clearEnvVars(t)
	setEnvVars(t, map[string]string{
		"OPENSEARCH_USERNAME": "admin",
		"OPENSEARCH_PASSWORD": "admin123",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing address")
	}
}

func TestLoadConfig_MissingUsername(t *testing.T) {
	clearEnvVars(t)
	setEnvVars(t, map[string]string{
		"OPENSEARCH_ADDRESS":  "https://opensearch:9200",
		"OPENSEARCH_PASSWORD": "admin123",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing username")
	}
}

func TestLoadConfig_MissingPassword(t *testing.T) {
	clearEnvVars(t)
	setEnvVars(t, map[string]string{
		"OPENSEARCH_ADDRESS":  "https://opensearch:9200",
		"OPENSEARCH_USERNAME": "admin",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing password")
	}
}

func TestLoadConfig_InvalidPort(t *testing.T) {
	clearEnvVars(t)
	vars := requiredEnvVars()
	vars["SERVER_PORT"] = "not-a-number"
	setEnvVars(t, vars)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestLoadConfig_PortOutOfRange(t *testing.T) {
	clearEnvVars(t)
	vars := requiredEnvVars()
	vars["SERVER_PORT"] = "99999"
	setEnvVars(t, vars)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for port out of range")
	}
}

func TestLoadConfig_InvalidLogLevel(t *testing.T) {
	clearEnvVars(t)
	vars := requiredEnvVars()
	vars["LOG_LEVEL"] = "TRACE"
	setEnvVars(t, vars)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid log level")
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
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			clearEnvVars(t)
			vars := requiredEnvVars()
			vars["LOG_LEVEL"] = tt.level
			setEnvVars(t, vars)

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.LogLevel != tt.expected {
				t.Errorf("expected log level %v, got %v", tt.expected, cfg.LogLevel)
			}
		})
	}
}
