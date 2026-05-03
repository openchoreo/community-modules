// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestLoadConfigParsesAlertingEnv(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("ALERT_METRIC_NAMESPACE", "Custom/Logs")
	t.Setenv("ALARM_ACTION_ARNS", "arn:aws:sns:eu-north-1:123456789012:alerts")
	t.Setenv("OK_ACTION_ARNS", "arn:aws:lambda:eu-north-1:123456789012:function:ok")
	t.Setenv("INSUFFICIENT_DATA_ACTION_ARNS", "arn:aws:sns:eu-north-1:123456789012:insufficient")
	t.Setenv("OBSERVER_URL", "http://observer.internal")
	t.Setenv("SNS_ALLOW_SUBSCRIBE_CONFIRM", "true")
	t.Setenv("FORWARD_RECOVERY", "true")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")
	t.Setenv("WEBHOOK_SHARED_SECRET", "0123456789abcdef")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AlertMetricNamespace != "Custom/Logs" {
		t.Fatalf("unexpected metric namespace %q", cfg.AlertMetricNamespace)
	}
	if len(cfg.AlarmActionARNs) != 1 || cfg.AlarmActionARNs[0] == "" {
		t.Fatalf("unexpected alarm action ARNs %#v", cfg.AlarmActionARNs)
	}
	if !cfg.SNSAllowSubscribeConfirm || !cfg.ForwardRecovery {
		t.Fatalf("unexpected alerting config %#v", cfg)
	}
	if !cfg.WebhookAuthEnabled || cfg.WebhookSharedSecret != "0123456789abcdef" {
		t.Fatalf("unexpected webhook auth config %#v", cfg)
	}
}

func TestLoadConfigRejectsInvalidActionARN(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("ALARM_ACTION_ARNS", "not-an-arn")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected invalid ARN error")
	}
}

func TestLoadConfigRejectsTooManyActionARNs(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("ALARM_ACTION_ARNS",
		"arn:aws:sns:eu-north-1:123456789012:a,"+
			"arn:aws:sns:eu-north-1:123456789012:b,"+
			"arn:aws:sns:eu-north-1:123456789012:c,"+
			"arn:aws:sns:eu-north-1:123456789012:d,"+
			"arn:aws:sns:eu-north-1:123456789012:e,"+
			"arn:aws:sns:eu-north-1:123456789012:f",
	)

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected too-many-ARNs error")
	}
}

func TestLoadConfigRejectsMissingWebhookSecretWhenAuthEnabled(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "true")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected missing webhook secret error")
	}
}

func resetCoreEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_REGION", "")
	t.Setenv("CLUSTER_NAME", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("ALARM_ACTION_ARNS", "")
	t.Setenv("OK_ACTION_ARNS", "")
	t.Setenv("INSUFFICIENT_DATA_ACTION_ARNS", "")
	t.Setenv("OBSERVER_URL", "")
	t.Setenv("SNS_ALLOW_SUBSCRIBE_CONFIRM", "")
	t.Setenv("FORWARD_RECOVERY", "")
	t.Setenv("WEBHOOK_AUTH_ENABLED", "")
	t.Setenv("WEBHOOK_SHARED_SECRET", "")
	t.Setenv("QUERY_TIMEOUT_SECONDS", "")
	t.Setenv("QUERY_POLL_MILLISECONDS", "")
	t.Setenv("SERVER_PORT", "")
	t.Setenv("ALERT_METRIC_NAMESPACE", "")
}

func TestLoadConfigDefaultsAreApplied(t *testing.T) {
	resetCoreEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.ServerPort != "9098" {
		t.Fatalf("ServerPort = %q", cfg.ServerPort)
	}
	if cfg.LogGroupPrefix != "/aws/containerinsights" {
		t.Fatalf("LogGroupPrefix = %q", cfg.LogGroupPrefix)
	}
	if cfg.QueryTimeout != 30*time.Second {
		t.Fatalf("QueryTimeout = %s", cfg.QueryTimeout)
	}
	if cfg.QueryPollEvery != 500*time.Millisecond {
		t.Fatalf("QueryPollEvery = %s", cfg.QueryPollEvery)
	}
	if cfg.AlertMetricNamespace != "OpenChoreo/Logs" {
		t.Fatalf("AlertMetricNamespace = %q", cfg.AlertMetricNamespace)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %s", cfg.LogLevel)
	}
}

func TestLoadConfigRequiresAWSRegion(t *testing.T) {
	resetCoreEnv(t)
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected missing AWS_REGION error")
	}
}

func TestLoadConfigRequiresClusterName(t *testing.T) {
	resetCoreEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected missing CLUSTER_NAME error")
	}
}

func TestLoadConfigRejectsInvalidQueryTimeout(t *testing.T) {
	resetCoreEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("QUERY_TIMEOUT_SECONDS", "0")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected invalid QUERY_TIMEOUT_SECONDS error")
	}

	t.Setenv("QUERY_TIMEOUT_SECONDS", "10")
	t.Setenv("QUERY_POLL_MILLISECONDS", "abc")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected invalid QUERY_POLL_MILLISECONDS error")
	}
}

func TestLoadConfigRejectsNonNumericServerPort(t *testing.T) {
	resetCoreEnv(t)
	t.Setenv("AWS_REGION", "eu-north-1")
	t.Setenv("CLUSTER_NAME", "openchoreo-test")
	t.Setenv("SERVER_PORT", "not-a-port")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected invalid SERVER_PORT error")
	}
}

func TestLoadConfigParsesLogLevels(t *testing.T) {
	cases := map[string]slog.Level{
		"DEBUG":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"WARN":    slog.LevelWarn,
		"WARNING": slog.LevelWarn,
		"ERROR":   slog.LevelError,
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			resetCoreEnv(t)
			t.Setenv("AWS_REGION", "eu-north-1")
			t.Setenv("CLUSTER_NAME", "openchoreo-test")
			t.Setenv("LOG_LEVEL", input)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.LogLevel != want {
				t.Fatalf("LogLevel = %s, want %s", cfg.LogLevel, want)
			}
		})
	}
}

func TestParseARNListIgnoresWhitespace(t *testing.T) {
	t.Setenv("X_ARNS", "  arn:aws:sns:eu-north-1:123456789012:a , arn:aws:sns:eu-north-1:123456789012:b , ")
	got, err := parseARNList("X_ARNS")
	if err != nil {
		t.Fatalf("parseARNList() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 ARNs, got %v", got)
	}
}

func TestParseARNListEmptyReturnsNil(t *testing.T) {
	_ = os.Unsetenv("X_ARNS_EMPTY")
	got, err := parseARNList("X_ARNS_EMPTY")
	if err != nil || got != nil {
		t.Fatalf("expected nil result for empty env, got %v / err %v", got, err)
	}
}

func TestGetEnvFallsBackToDefault(t *testing.T) {
	_ = os.Unsetenv("DOES_NOT_EXIST")
	if got := getEnv("DOES_NOT_EXIST", "fallback"); got != "fallback" {
		t.Fatalf("getEnv() = %q", got)
	}
}
