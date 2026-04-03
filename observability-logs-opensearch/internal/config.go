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

// Config holds the application configuration.
type Config struct {
	ServerPort         string
	OpenSearchAddress  string
	OpenSearchUsername string
	OpenSearchPassword string
	OpenSearchIndexPrefix string
	TLSSkipVerify      bool
	ObserverURL        string
	LogLevel           slog.Level
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9098")
	openSearchAddress := getEnv("OPENSEARCH_ADDRESS", "")
	openSearchUsername := getEnv("OPENSEARCH_USERNAME", "")
	openSearchPassword := getEnv("OPENSEARCH_PASSWORD", "")
	openSearchIndexPrefix := getEnv("OPENSEARCH_INDEX_PREFIX", "container-logs-")
	observerURL := getEnv("OBSERVER_URL", "")

	tlsSkipVerify := true
	if v := os.Getenv("OPENSEARCH_TLS_SKIP_VERIFY"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid OPENSEARCH_TLS_SKIP_VERIFY value: %w", err)
		}
		tlsSkipVerify = parsed
	}

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

	if openSearchAddress == "" {
		return nil, fmt.Errorf("environment variable OPENSEARCH_ADDRESS is required")
	}

	if openSearchUsername == "" {
		return nil, fmt.Errorf("environment variable OPENSEARCH_USERNAME is required")
	}

	if openSearchPassword == "" {
		return nil, fmt.Errorf("environment variable OPENSEARCH_PASSWORD is required")
	}

	if observerURL == "" {
		return nil, fmt.Errorf("environment variable OBSERVER_URL is required")
	}
	parsedURL, err := url.Parse(observerURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("OBSERVER_URL must be a valid URL with scheme and host, got: %q", observerURL)
	}

	if _, err := strconv.Atoi(serverPort); err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: %w", err)
	}

	return &Config{
		ServerPort:         serverPort,
		OpenSearchAddress:  openSearchAddress,
		OpenSearchUsername: openSearchUsername,
		OpenSearchPassword: openSearchPassword,
		OpenSearchIndexPrefix: openSearchIndexPrefix,
		TLSSkipVerify:      tlsSkipVerify,
		ObserverURL:        observerURL,
		LogLevel:           logLevel,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
