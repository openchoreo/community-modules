// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SERVER_PORT", "OPENCOST_URL", "OBSERVER_URL", "METRICS_STEP",
		"RECOMMENDATION_CPU_PERCENTILE", "RECOMMENDATION_MEMORY_PERCENTILE",
		"RECOMMENDATION_CPU_HEADROOM", "RECOMMENDATION_MEMORY_HEADROOM",
		"RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES", "RECOMMENDATION_MEMORY_MIN_REQUEST_MI",
		"LOG_LEVEL",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.ServerPort != "9101" {
		t.Errorf("ServerPort = %q, want 9101", cfg.ServerPort)
	}
	if cfg.OpenCostURL != "http://opencost:9003" {
		t.Errorf("OpenCostURL = %q", cfg.OpenCostURL)
	}
	if cfg.ObserverURL != "http://observer:8080" {
		t.Errorf("ObserverURL = %q", cfg.ObserverURL)
	}
	if cfg.MetricsStep != "5m" {
		t.Errorf("MetricsStep = %q", cfg.MetricsStep)
	}
	if cfg.RecommendationCPUPercentile != 95 || cfg.RecommendationMemoryPercentile != 95 {
		t.Errorf("percentile defaults wrong: %v %v", cfg.RecommendationCPUPercentile, cfg.RecommendationMemoryPercentile)
	}
	if cfg.RecommendationCPUHeadroom != 0.2 || cfg.RecommendationMemoryHeadroom != 0.2 {
		t.Errorf("headroom defaults wrong")
	}
	// 1 millicore -> 0.001 cores, 5 Mi -> 5*1024*1024 bytes.
	if cfg.RecommendationMinCPURequest != 0.001 {
		t.Errorf("min cpu = %v, want 0.001", cfg.RecommendationMinCPURequest)
	}
	if cfg.RecommendationMinMemRequest != 5*1024*1024 {
		t.Errorf("min mem = %v", cfg.RecommendationMinMemRequest)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("default log level = %v, want Info", cfg.LogLevel)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SERVER_PORT", "8080")
	t.Setenv("OPENCOST_URL", "https://oc.example.com")
	t.Setenv("OBSERVER_URL", "https://obs.example.com")
	t.Setenv("METRICS_STEP", "1h")
	t.Setenv("RECOMMENDATION_CPU_PERCENTILE", "90")
	t.Setenv("RECOMMENDATION_MEMORY_PERCENTILE", "80")
	t.Setenv("RECOMMENDATION_CPU_HEADROOM", "0.3")
	t.Setenv("RECOMMENDATION_MEMORY_HEADROOM", "0.4")
	t.Setenv("RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES", "10")
	t.Setenv("RECOMMENDATION_MEMORY_MIN_REQUEST_MI", "20")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.ServerPort != "8080" || cfg.MetricsStep != "1h" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.RecommendationCPUPercentile != 90 || cfg.RecommendationMemoryPercentile != 80 {
		t.Errorf("percentile overrides wrong")
	}
	if cfg.RecommendationMinCPURequest != 0.01 {
		t.Errorf("min cpu = %v, want 0.01", cfg.RecommendationMinCPURequest)
	}
	if cfg.RecommendationMinMemRequest != 20*1024*1024 {
		t.Errorf("min mem = %v", cfg.RecommendationMinMemRequest)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("log level = %v, want Debug", cfg.LogLevel)
	}
}

func TestLoadConfigLogLevels(t *testing.T) {
	cases := map[string]slog.Level{
		"DEBUG":   slog.LevelDebug,
		"WARN":    slog.LevelWarn,
		"WARNING": slog.LevelWarn,
		"ERROR":   slog.LevelError,
		"unknown": slog.LevelInfo,
	}
	for value, want := range cases {
		clearConfigEnv(t)
		t.Setenv("LOG_LEVEL", value)
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig(%q) error: %v", value, err)
		}
		if cfg.LogLevel != want {
			t.Errorf("LOG_LEVEL=%q => %v, want %v", value, cfg.LogLevel, want)
		}
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"bad cpu percentile float", "RECOMMENDATION_CPU_PERCENTILE", "abc"},
		{"bad memory percentile float", "RECOMMENDATION_MEMORY_PERCENTILE", "abc"},
		{"bad cpu headroom float", "RECOMMENDATION_CPU_HEADROOM", "abc"},
		{"bad memory headroom float", "RECOMMENDATION_MEMORY_HEADROOM", "abc"},
		{"bad min cpu float", "RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES", "abc"},
		{"bad min mem float", "RECOMMENDATION_MEMORY_MIN_REQUEST_MI", "abc"},
		{"cpu percentile zero", "RECOMMENDATION_CPU_PERCENTILE", "0"},
		{"cpu percentile over 100", "RECOMMENDATION_CPU_PERCENTILE", "101"},
		{"memory percentile over 100", "RECOMMENDATION_MEMORY_PERCENTILE", "150"},
		{"negative cpu headroom", "RECOMMENDATION_CPU_HEADROOM", "-1"},
		{"negative memory headroom", "RECOMMENDATION_MEMORY_HEADROOM", "-1"},
		{"negative min cpu", "RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES", "-1"},
		{"negative min mem", "RECOMMENDATION_MEMORY_MIN_REQUEST_MI", "-1"},
		{"bad opencost url", "OPENCOST_URL", "not-a-url"},
		{"bad observer url", "OBSERVER_URL", "not-a-url"},
		{"port not integer", "SERVER_PORT", "abc"},
		{"port too low", "SERVER_PORT", "0"},
		{"port too high", "SERVER_PORT", "70000"},
		{"bad metrics step", "METRICS_STEP", "5x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.key, tc.val)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("expected error for %s=%q", tc.key, tc.val)
			}
		})
	}
}

func TestValidateStep(t *testing.T) {
	valid := []string{"30s", "5m", "1h", "1d", "10m"}
	for _, v := range valid {
		if err := validateStep("METRICS_STEP", v); err != nil {
			t.Errorf("validateStep(%q) unexpected error: %v", v, err)
		}
	}
	invalid := []string{"", "5", "0m", "5x", "m5", "-5m", "5mm", "1.5h"}
	for _, v := range invalid {
		if err := validateStep("METRICS_STEP", v); err == nil {
			t.Errorf("validateStep(%q) expected error", v)
		}
	}
}

func TestValidateURL(t *testing.T) {
	valid := []string{"http://a.com", "https://a.com:9003", "http://opencost"}
	for _, v := range valid {
		if err := validateURL("URL", v); err != nil {
			t.Errorf("validateURL(%q) unexpected error: %v", v, err)
		}
	}
	invalid := []string{"", "no-scheme.com", "http://", "://host"}
	for _, v := range invalid {
		if err := validateURL("URL", v); err == nil {
			t.Errorf("validateURL(%q) expected error", v)
		}
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("SOME_KEY", "value")
	if got := getEnv("SOME_KEY", "fallback"); got != "value" {
		t.Errorf("getEnv = %q, want value", got)
	}
	t.Setenv("SOME_KEY", "")
	if got := getEnv("SOME_KEY", "fallback"); got != "fallback" {
		t.Errorf("getEnv = %q, want fallback", got)
	}
}

func TestGetEnvFloat(t *testing.T) {
	t.Setenv("F", "")
	if v, err := getEnvFloat("F", 1.5); err != nil || v != 1.5 {
		t.Errorf("getEnvFloat default = %v, %v", v, err)
	}
	t.Setenv("F", "2.5")
	if v, err := getEnvFloat("F", 1.5); err != nil || v != 2.5 {
		t.Errorf("getEnvFloat parsed = %v, %v", v, err)
	}
	t.Setenv("F", "notafloat")
	if _, err := getEnvFloat("F", 1.5); err == nil {
		t.Errorf("getEnvFloat expected error for bad value")
	}
}
