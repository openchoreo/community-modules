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
	ServerPort string
	AWSRegion  string
	LogLevel   slog.Level
}

func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9100")
	awsRegion := getEnv("AWS_REGION", "")

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

	port, err := strconv.Atoi(serverPort)
	if err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535: %w", err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid SERVER_PORT: must be integer in 1..65535")
	}

	return &Config{
		ServerPort: serverPort,
		AWSRegion:  awsRegion,
		LogLevel:   logLevel,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
