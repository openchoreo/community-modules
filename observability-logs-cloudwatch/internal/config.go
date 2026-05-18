// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerPort     string
	AWSRegion      string
	LogGroupPrefix string
	LogGroupName   string
	QueryTimeout   time.Duration
	QueryPollEvery time.Duration
	LogLevel       slog.Level

	// Alerting configuration
	AlertMetricNamespace       string
	AlarmActionARNs            []string
	OKActionARNs               []string
	InsufficientDataActionARNs []string
	ObserverURL                string
	SNSAllowSubscribeConfirm   bool
	ForwardRecovery            bool
	WebhookAuthEnabled         bool
	WebhookSharedSecret        string
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9098")
	awsRegion := getEnv("AWS_REGION", "")
	logGroupPrefix := getEnv("LOG_GROUP_PREFIX", "/aws/containerinsights")
	logGroupName := getEnv("LOG_GROUP_NAME", "")
	queryTimeoutStr := getEnv("QUERY_TIMEOUT_SECONDS", "30")
	queryPollStr := getEnv("QUERY_POLL_MILLISECONDS", "500")

	logLevel := slog.LevelInfo
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		switch strings.ToUpper(level) {
		case "DEBUG":
			logLevel = slog.LevelDebug
		case "INFO":
			logLevel = slog.LevelInfo
		case "WARN", "WARNING":
			logLevel = slog.LevelWarn
		case "ERROR":
			logLevel = slog.LevelError
		}
	}

	if awsRegion == "" {
		return nil, fmt.Errorf("environment variable AWS_REGION is required")
	}

	logGroupPrefix = strings.TrimRight(logGroupPrefix, "/")
	if logGroupName == "" {
		logGroupName = logGroupPrefix + "/application"
	}

	queryTimeoutSec, err := strconv.Atoi(queryTimeoutStr)
	if err != nil || queryTimeoutSec <= 0 {
		return nil, fmt.Errorf("invalid QUERY_TIMEOUT_SECONDS: %q", queryTimeoutStr)
	}

	queryPollMs, err := strconv.Atoi(queryPollStr)
	if err != nil || queryPollMs <= 0 {
		return nil, fmt.Errorf("invalid QUERY_POLL_MILLISECONDS: %q", queryPollStr)
	}

	port, err := strconv.Atoi(serverPort)
	if err != nil || strconv.Itoa(port) != serverPort {
		return nil, fmt.Errorf("invalid SERVER_PORT: %q", serverPort)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid SERVER_PORT: %d out of range", port)
	}

	alertMetricNamespace := getEnv("ALERT_METRIC_NAMESPACE", "OpenChoreo/Logs")
	alarmActionARNs, err := parseARNList("ALARM_ACTION_ARNS")
	if err != nil {
		return nil, err
	}
	okActionARNs, err := parseARNList("OK_ACTION_ARNS")
	if err != nil {
		return nil, err
	}
	insufficientDataActionARNs, err := parseARNList("INSUFFICIENT_DATA_ACTION_ARNS")
	if err != nil {
		return nil, err
	}
	observerURL := getEnv("OBSERVER_URL", "")
	snsAllowSubscribeConfirm := strings.EqualFold(getEnv("SNS_ALLOW_SUBSCRIBE_CONFIRM", "false"), "true")
	forwardRecovery := strings.EqualFold(getEnv("FORWARD_RECOVERY", "false"), "true")
	webhookAuthEnabled := strings.EqualFold(getEnv("WEBHOOK_AUTH_ENABLED", "false"), "true")
	webhookSharedSecret := os.Getenv("WEBHOOK_SHARED_SECRET")
	if webhookAuthEnabled && len(webhookSharedSecret) < 16 {
		return nil, fmt.Errorf("invalid WEBHOOK_SHARED_SECRET: must be at least 16 bytes when WEBHOOK_AUTH_ENABLED=true")
	}

	return &Config{
		ServerPort:                 serverPort,
		AWSRegion:                  awsRegion,
		LogGroupPrefix:             logGroupPrefix,
		LogGroupName:               logGroupName,
		QueryTimeout:               time.Duration(queryTimeoutSec) * time.Second,
		QueryPollEvery:             time.Duration(queryPollMs) * time.Millisecond,
		LogLevel:                   logLevel,
		AlertMetricNamespace:       alertMetricNamespace,
		AlarmActionARNs:            alarmActionARNs,
		OKActionARNs:               okActionARNs,
		InsufficientDataActionARNs: insufficientDataActionARNs,
		ObserverURL:                observerURL,
		SNSAllowSubscribeConfirm:   snsAllowSubscribeConfirm,
		ForwardRecovery:            forwardRecovery,
		WebhookAuthEnabled:         webhookAuthEnabled,
		WebhookSharedSecret:        webhookSharedSecret,
	}, nil
}

func parseARNList(envKey string) ([]string, error) {
	raw := os.Getenv(envKey)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		arn := strings.TrimSpace(p)
		if arn == "" {
			continue
		}
		if !strings.HasPrefix(arn, "arn:") || len(strings.Split(arn, ":")) < 6 {
			return nil, fmt.Errorf("invalid %s entry %q: must be a well-formed ARN (arn:<partition>:<service>:<region>:<account-id>:<resource>)", envKey, arn)
		}
		out = append(out, arn)
	}
	if len(out) > 5 {
		return nil, fmt.Errorf("invalid %s: CloudWatch allows at most 5 action ARNs (got %d)", envKey, len(out))
	}
	return out, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
