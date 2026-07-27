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
	ServerPort   string
	ProjectID    string
	QueryTimeout time.Duration
	LogLevel     slog.Level
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9100")
	projectID := getEnv("GCP_PROJECT_ID", "")
	queryTimeoutSeconds := getEnv("QUERY_TIMEOUT_SECONDS", "30")

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

	if projectID == "" {
		return nil, fmt.Errorf("environment variable GCP_PROJECT_ID is required")
	}

	port, err := strconv.Atoi(serverPort)
	if err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535: %w", err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535")
	}

	timeoutSeconds, err := strconv.Atoi(queryTimeoutSeconds)
	if err != nil || timeoutSeconds < 1 {
		return nil, fmt.Errorf("invalid QUERY_TIMEOUT_SECONDS: must be a positive integer")
	}

	return &Config{
		ServerPort:   serverPort,
		ProjectID:    projectID,
		QueryTimeout: time.Duration(timeoutSeconds) * time.Second,
		LogLevel:     logLevel,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
