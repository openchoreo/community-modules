// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"os"
	"testing"
)

func setEnvs(t *testing.T, envs map[string]string) {
	t.Helper()
	for k, v := range envs {
		t.Setenv(k, v)
	}
}

func clearEnvs(t *testing.T, keys []string) {
	t.Helper()
	for _, k := range keys {
		os.Unsetenv(k)
	}
}

func TestLoadConfig_Success(t *testing.T) {
	setEnvs(t, map[string]string{
		"AWS_REGION": "eu-north-1",
	})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AWSRegion != "eu-north-1" {
		t.Errorf("expected region eu-north-1, got %s", cfg.AWSRegion)
	}
	if cfg.ServerPort != "9100" {
		t.Errorf("expected port 9100, got %s", cfg.ServerPort)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected log level INFO, got %s", cfg.LogLevel.String())
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	setEnvs(t, map[string]string{
		"AWS_REGION":  "us-east-1",
		"SERVER_PORT": "8080",
		"LOG_LEVEL":   "DEBUG",
	})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AWSRegion != "us-east-1" {
		t.Errorf("expected region us-east-1, got %s", cfg.AWSRegion)
	}
	if cfg.ServerPort != "8080" {
		t.Errorf("expected port 8080, got %s", cfg.ServerPort)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected log level DEBUG, got %s", cfg.LogLevel.String())
	}
}

func TestLoadConfig_MissingRegion(t *testing.T) {
	clearEnvs(t, []string{"AWS_REGION"})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing AWS_REGION")
	}
}

func TestLoadConfig_InvalidPort(t *testing.T) {
	setEnvs(t, map[string]string{
		"AWS_REGION":  "us-east-1",
		"SERVER_PORT": "not-a-number",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestLoadConfig_PortOutOfRange(t *testing.T) {
	setEnvs(t, map[string]string{
		"AWS_REGION":  "us-east-1",
		"SERVER_PORT": "70000",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for port out of range")
	}
}
