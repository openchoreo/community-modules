// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"strings"
	"testing"
)

// requiredEnv lists every variable LoadConfig consults; tests start each case
// from a clean slate so leakage from the host shell can't mask a regression.
var requiredEnv = []string{
	"SERVER_PORT",
	"AWS_REGION",
	"INSTANCE_NAME",
	"METRIC_NAMESPACE",
	"LOG_LEVEL",
	"ALARM_ACTION_ARNS",
	"OK_ACTION_ARNS",
	"INSUFFICIENT_DATA_ACTION_ARNS",
	"OBSERVER_URL",
	"SNS_ALLOW_SUBSCRIBE_CONFIRM",
	"FORWARD_RECOVERY",
	"WEBHOOK_AUTH_ENABLED",
	"WEBHOOK_SHARED_SECRET",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range requiredEnv {
		t.Setenv(k, "")
	}
}

func TestLoadConfigHappyPath(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("INSTANCE_NAME", "openchoreo-dev")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ALARM_ACTION_ARNS", "arn:aws:sns:eu-north-1:123:alerts , arn:aws:sns:eu-north-1:123:ops")
	t.Setenv("OBSERVER_URL", "http://observer:8081")
	t.Setenv("SNS_ALLOW_SUBSCRIBE_CONFIRM", "TRUE")
	t.Setenv("FORWARD_RECOVERY", "true")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")
	t.Setenv("WEBHOOK_SHARED_SECRET", "0123456789abcdef")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.ServerPort != "9099" {
		t.Errorf("ServerPort = %q, want default 9099", cfg.ServerPort)
	}
	if cfg.MetricNamespace != "OpenChoreo/Metrics" {
		t.Errorf("MetricNamespace = %q, want default", cfg.MetricNamespace)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want Debug", cfg.LogLevel)
	}
	if !cfg.SNSAllowSubscribeConfirm || !cfg.ForwardRecovery || !cfg.WebhookAuthEnabled {
		t.Errorf("expected booleans true, got %+v", cfg)
	}
	if got, want := len(cfg.AlarmActionARNs), 2; got != want {
		t.Fatalf("AlarmActionARNs count = %d, want %d", got, want)
	}
	if cfg.AlarmActionARNs[0] != "arn:aws:sns:eu-north-1:123:alerts" {
		t.Errorf("AlarmActionARNs[0] = %q, want trimmed value", cfg.AlarmActionARNs[0])
	}
}

func TestLoadConfigRequiresAWSRegion(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INSTANCE_NAME", "openchoreo-dev")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "AWS_REGION") {
		t.Fatalf("expected AWS_REGION error, got %v", err)
	}
}

func TestLoadConfigRequiresInstanceName(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "INSTANCE_NAME") {
		t.Fatalf("expected INSTANCE_NAME error, got %v", err)
	}
}

func TestLoadConfigRejectsNonNumericPort(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("INSTANCE_NAME", "openchoreo-dev")
	t.Setenv("SERVER_PORT", "not-a-number")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "SERVER_PORT") {
		t.Fatalf("expected SERVER_PORT error, got %v", err)
	}
}

func TestLoadConfigRejectsBadARN(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("INSTANCE_NAME", "openchoreo-dev")
	t.Setenv("ALARM_ACTION_ARNS", "not-an-arn")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "ALARM_ACTION_ARNS") {
		t.Fatalf("expected ALARM_ACTION_ARNS error, got %v", err)
	}
}

func TestLoadConfigRejectsTooManyARNs(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("INSTANCE_NAME", "openchoreo-dev")
	t.Setenv("OK_ACTION_ARNS", strings.Join([]string{
		"arn:aws:sns:eu-north-1:1:a",
		"arn:aws:sns:eu-north-1:1:b",
		"arn:aws:sns:eu-north-1:1:c",
		"arn:aws:sns:eu-north-1:1:d",
		"arn:aws:sns:eu-north-1:1:e",
		"arn:aws:sns:eu-north-1:1:f",
	}, ","))
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "at most 5") {
		t.Fatalf("expected ARN-count error, got %v", err)
	}
}

func TestLoadConfigRequiresLongWebhookSecret(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("INSTANCE_NAME", "openchoreo-dev")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")
	t.Setenv("WEBHOOK_SHARED_SECRET", "tooshort")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "WEBHOOK_SHARED_SECRET") {
		t.Fatalf("expected WEBHOOK_SHARED_SECRET error, got %v", err)
	}
}

func TestLoadConfigLogLevelMapping(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"WARNING": slog.LevelWarn,
		"error":   slog.LevelError,
		"DEBUG":   slog.LevelDebug,
		"weird":   slog.LevelInfo, // unknown values fall back to default.
	}
	for raw, want := range cases {
		t.Run("level="+raw, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("AWS_REGION", "eu-north-1")
			t.Setenv("INSTANCE_NAME", "openchoreo-dev")
			if raw != "" {
				t.Setenv("LOG_LEVEL", raw)
			}
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.LogLevel != want {
				t.Fatalf("LogLevel = %v, want %v", cfg.LogLevel, want)
			}
		})
	}
}

func TestParseARNList(t *testing.T) {
	t.Run("empty returns nil", func(t *testing.T) {
		t.Setenv("ALARM_ACTION_ARNS", "   ")
		got, err := parseARNList("ALARM_ACTION_ARNS")
		if err != nil {
			t.Fatalf("parseARNList() error = %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
	t.Run("skips blanks between commas", func(t *testing.T) {
		t.Setenv("ALARM_ACTION_ARNS", "arn:aws:sns:eu-north-1:1:a, , arn:aws:sns:eu-north-1:1:b")
		got, err := parseARNList("ALARM_ACTION_ARNS")
		if err != nil {
			t.Fatalf("parseARNList() error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 ARNs, got %v", got)
		}
	})
}

func TestGetEnvFallback(t *testing.T) {
	t.Setenv("FOO_BAR_BAZ", "")
	if got := getEnv("FOO_BAR_BAZ", "fallback"); got != "fallback" {
		t.Errorf("expected fallback, got %q", got)
	}
	t.Setenv("FOO_BAR_BAZ", "value")
	if got := getEnv("FOO_BAR_BAZ", "fallback"); got != "value" {
		t.Errorf("expected value, got %q", got)
	}
}
