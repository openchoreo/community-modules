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
	ServerPort              string
	OpenSearchAddress       string
	OpenSearchUsername      string
	OpenSearchPassword      string
	OpenSearchIndexPrefix   string
	OpenSearchTLSSkipVerify bool
	LogLevel                slog.Level
	// RawQueryUsername and RawQueryPassword are the read-only OpenSearch user
	// raw queries run as. Raw queries are not supported when they are unset.
	RawQueryUsername string
	RawQueryPassword string
	RawQuery         RawQueryLimits
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9100")
	openSearchAddress := getEnv("OPENSEARCH_ADDRESS", "")
	openSearchUsername := getEnv("OPENSEARCH_USERNAME", "")
	openSearchPassword := getEnv("OPENSEARCH_PASSWORD", "")
	openSearchIndexPrefix := getEnv("OPENSEARCH_INDEX_PREFIX", "otel-traces-")
	tlsSkipVerify := getEnv("OPENSEARCH_TLS_SKIP_VERIFY", "true")

	// Parse log level
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
		default:
			return nil, fmt.Errorf("invalid LOG_LEVEL %q: must be one of DEBUG, INFO, WARN, WARNING, ERROR", level)
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

	port, err := strconv.Atoi(serverPort)
	if err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535: %w", err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535")
	}

	skipVerify := strings.ToLower(tlsSkipVerify) == "true"

	rawQuery, err := loadRawQueryLimits()
	if err != nil {
		return nil, err
	}
	rawQueryUsername := getEnv("OPENSEARCH_RAW_QUERY_USERNAME", "")
	rawQueryPassword := getEnv("OPENSEARCH_RAW_QUERY_PASSWORD", "")
	if (rawQueryUsername == "") != (rawQueryPassword == "") {
		return nil, fmt.Errorf("OPENSEARCH_RAW_QUERY_USERNAME and OPENSEARCH_RAW_QUERY_PASSWORD must be set together")
	}

	return &Config{
		ServerPort:              serverPort,
		OpenSearchAddress:       openSearchAddress,
		OpenSearchUsername:      openSearchUsername,
		OpenSearchPassword:      openSearchPassword,
		OpenSearchIndexPrefix:   openSearchIndexPrefix,
		OpenSearchTLSSkipVerify: skipVerify,
		LogLevel:                logLevel,
		RawQueryUsername:        rawQueryUsername,
		RawQueryPassword:        rawQueryPassword,
		RawQuery:                rawQuery,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// loadRawQueryLimits reads RAW_QUERY_TIMEOUT and RAW_QUERY_MAX_RESPONSE_BYTES.
func loadRawQueryLimits() (RawQueryLimits, error) {
	limits := DefaultRawQueryLimits
	if v := os.Getenv("RAW_QUERY_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return limits, fmt.Errorf("invalid RAW_QUERY_TIMEOUT %q: must be a positive duration", v)
		}
		limits.Timeout = d
	}
	if v := os.Getenv("RAW_QUERY_MAX_RESPONSE_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return limits, fmt.Errorf("invalid RAW_QUERY_MAX_RESPONSE_BYTES %q: must be a positive integer", v)
		}
		limits.MaxResponseBytes = n
	}
	return limits, nil
}
