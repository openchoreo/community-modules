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
	ServerPort          string
	OpenObserveURL      string
	OpenObserveOrg      string
	OpenObserveStream   string
	OpenObserveUser     string
	OpenObservePassword string
	LogLevel            slog.Level
	// RawQueryUser and RawQueryPassword are the OpenObserve user raw queries
	// run as, never the adapter's own. Raw queries are not supported when
	// they are unset.
	RawQueryUser     string
	RawQueryPassword string
	RawQuery         RawQueryLimits
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9100")
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

	port, err := strconv.Atoi(serverPort)
	if err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535: %w", err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535")
	}

	rawQuery, err := loadRawQueryLimits()
	if err != nil {
		return nil, err
	}
	// OpenObserve trims and lowercases a user's email when it creates the user,
	// as the setup job does, so the adapter logs in with the same email.
	rawQueryUser := strings.ToLower(strings.TrimSpace(getEnv("OPENOBSERVE_RAW_QUERY_USER", "")))
	rawQueryPassword := getEnv("OPENOBSERVE_RAW_QUERY_PASSWORD", "")
	if (rawQueryUser == "") != (rawQueryPassword == "") {
		return nil, fmt.Errorf("OPENOBSERVE_RAW_QUERY_USER and OPENOBSERVE_RAW_QUERY_PASSWORD must be set together")
	}
	if rawQueryUser != "" && rawQueryUser == strings.ToLower(strings.TrimSpace(openObserveUser)) {
		return nil, fmt.Errorf("OPENOBSERVE_RAW_QUERY_USER is the root user; raw trace queries need a user of their own")
	}

	return &Config{
		RawQueryUser:        rawQueryUser,
		RawQueryPassword:    rawQueryPassword,
		RawQuery:            rawQuery,
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
