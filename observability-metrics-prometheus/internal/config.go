// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServerPort             string
	PrometheusAddress      string
	ObserverAPIInternalURL string
	AlertRuleNamespace     string
	LogLevel               slog.Level
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9099")
	observerAPIInternalURL := getEnv("OBSERVER_INTERNAL_URL", "")
	prometheusAddress := getEnv("PROMETHEUS_ADDRESS", "")
	alertRuleNamespace := getEnv("OBSERVABILITY_NAMESPACE", "openchoreo-observability-plane")

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

	if observerAPIInternalURL == "" {
		return nil, fmt.Errorf("environment variable OBSERVER_INTERNAL_URL is required")
	}
	parsedURL, err := url.Parse(observerAPIInternalURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("OBSERVER_INTERNAL_URL must be a valid URL with scheme and host, got: %q", observerAPIInternalURL)
	}

	if prometheusAddress == "" {
		return nil, fmt.Errorf("environment variable PROMETHEUS_ADDRESS is required")
	}
	parsedPromURL, err := url.Parse(prometheusAddress)
	if err != nil || parsedPromURL.Scheme == "" || parsedPromURL.Host == "" {
		return nil, fmt.Errorf("PROMETHEUS_ADDRESS must be a valid URL with scheme and host, got: %q", prometheusAddress)
	}

	if _, err := strconv.Atoi(serverPort); err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: %w", err)
	}

	return &Config{
		ServerPort:             serverPort,
		PrometheusAddress:      prometheusAddress,
		ObserverAPIInternalURL: observerAPIInternalURL,
		AlertRuleNamespace:     alertRuleNamespace,
		LogLevel:               logLevel,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
