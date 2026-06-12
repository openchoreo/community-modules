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
	ServerPort              string
	OpenObserveURL          string
	OpenObserveOrg          string
	OpenObserveStream       string
	OpenObserveEventsStream string
	OpenObserveUser         string
	OpenObservePassword     string
	ObserverURL             string
	LogLevel                slog.Level
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9098")
	openObserveURL := getEnv("OPENOBSERVE_URL", "")
	openObserveOrg := getEnv("OPENOBSERVE_ORG", "default")
	openObserveStream := getEnv("OPENOBSERVE_STREAM", "default")
	openObserveEventsStream := getEnv("OPENOBSERVE_EVENTS_STREAM", "k8s_events")
	openObserveUser := getEnv("OPENOBSERVE_USER", "")
	openObservePassword := getEnv("OPENOBSERVE_PASSWORD", "")
	observerURL := getEnv("OBSERVER_URL", "")

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
		ServerPort:              serverPort,
		OpenObserveURL:          openObserveURL,
		OpenObserveOrg:          openObserveOrg,
		OpenObserveStream:       openObserveStream,
		OpenObserveEventsStream: openObserveEventsStream,
		OpenObserveUser:         openObserveUser,
		OpenObservePassword:     openObservePassword,
		ObserverURL:             observerURL,
		LogLevel:                logLevel,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
