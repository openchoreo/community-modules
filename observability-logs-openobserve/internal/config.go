// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServerPort          string
	OpenObserveURL      string
	OpenObserveOrg      string
	OpenObserveStream   string
	OpenObserveUser     string
	OpenObservePassword string
	LogLevel            slog.Level
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9098")
	openObserveURL := getEnv("OPENOBSERVE_URL", "")
	openObserveOrg := getEnv("OPENOBSERVE_ORG", "default")
	openObserveStream := getEnv("OPENOBSERVE_STREAM", "default")
	openObserveUser := getEnv("OPENOBSERVE_USER", "")
	openObservePassword := getEnv("OPENOBSERVE_PASSWORD", "")

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
		}
	}

	if openObserveURL == "" {
		return nil, fmt.Errorf("Environment variable OPENOBSERVE_URL is required")
	}

	if openObserveUser == "" {
		return nil, fmt.Errorf("Environment variable OPENOBSERVE_USER is required")
	}

	if openObservePassword == "" {
		return nil, fmt.Errorf("Environment variable OPENOBSERVE_PASSWORD is required")
	}

	if _, err := strconv.Atoi(serverPort); err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: %w", err)
	}

	return &Config{
		ServerPort:          serverPort,
		OpenObserveURL:      openObserveURL,
		OpenObserveOrg:      openObserveOrg,
		OpenObserveStream:   openObserveStream,
		OpenObserveUser:     openObserveUser,
		OpenObservePassword: openObservePassword,
		LogLevel:            logLevel,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
