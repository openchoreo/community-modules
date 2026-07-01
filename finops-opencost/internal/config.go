// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	ServerPort                     string
	OpenCostURL                    string
	ObserverURL                    string
	MetricsStep                    string
	RecommendationCPUPercentile    float64
	RecommendationMemoryPercentile float64
	RecommendationCPUHeadroom      float64
	RecommendationMemoryHeadroom   float64
	RecommendationMinCPURequest    float64
	RecommendationMinMemRequest    float64
	LogLevel                       slog.Level
}

func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9101")
	openCostURL := getEnv("OPENCOST_URL", "http://opencost:9003")
	observerURL := getEnv("OBSERVER_URL", "http://observer:8080")
	metricsStep := getEnv("METRICS_STEP", "5m")

	cpuPercentile, err := getEnvFloat("RECOMMENDATION_CPU_PERCENTILE", 95)
	if err != nil {
		return nil, err
	}
	memoryPercentile, err := getEnvFloat("RECOMMENDATION_MEMORY_PERCENTILE", 95)
	if err != nil {
		return nil, err
	}
	cpuHeadroom, err := getEnvFloat("RECOMMENDATION_CPU_HEADROOM", 0.2)
	if err != nil {
		return nil, err
	}
	memoryHeadroom, err := getEnvFloat("RECOMMENDATION_MEMORY_HEADROOM", 0.2)
	if err != nil {
		return nil, err
	}
	// Minimum requests are configured in Kubernetes-friendly units (millicores
	// and mebibytes) and converted to cores and bytes for the algorithm.
	minCPUMillicores, err := getEnvFloat("RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES", 1)
	if err != nil {
		return nil, err
	}
	minMemMi, err := getEnvFloat("RECOMMENDATION_MEMORY_MIN_REQUEST_MI", 5)
	if err != nil {
		return nil, err
	}

	for name, p := range map[string]float64{"RECOMMENDATION_CPU_PERCENTILE": cpuPercentile, "RECOMMENDATION_MEMORY_PERCENTILE": memoryPercentile} {
		if p <= 0 || p > 100 {
			return nil, fmt.Errorf("%s must be in the range (0, 100], got %v", name, p)
		}
	}
	for name, v := range map[string]float64{"RECOMMENDATION_CPU_HEADROOM": cpuHeadroom, "RECOMMENDATION_MEMORY_HEADROOM": memoryHeadroom, "RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES": minCPUMillicores, "RECOMMENDATION_MEMORY_MIN_REQUEST_MI": minMemMi} {
		if v < 0 {
			return nil, fmt.Errorf("%s must be >= 0, got %v", name, v)
		}
	}

	if err := validateURL("OPENCOST_URL", openCostURL); err != nil {
		return nil, err
	}
	if err := validateURL("OBSERVER_URL", observerURL); err != nil {
		return nil, err
	}
	if port, err := strconv.Atoi(serverPort); err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("SERVER_PORT must be an integer in the range [1, 65535], got: %q", serverPort)
	}
	if err := validateStep("METRICS_STEP", metricsStep); err != nil {
		return nil, err
	}

	logLevel := slog.LevelInfo
	switch strings.ToUpper(os.Getenv("LOG_LEVEL")) {
	case "DEBUG":
		logLevel = slog.LevelDebug
	case "WARN", "WARNING":
		logLevel = slog.LevelWarn
	case "ERROR":
		logLevel = slog.LevelError
	}

	return &Config{
		ServerPort:                     serverPort,
		OpenCostURL:                    openCostURL,
		ObserverURL:                    observerURL,
		MetricsStep:                    metricsStep,
		RecommendationCPUPercentile:    cpuPercentile,
		RecommendationMemoryPercentile: memoryPercentile,
		RecommendationCPUHeadroom:      cpuHeadroom,
		RecommendationMemoryHeadroom:   memoryHeadroom,
		RecommendationMinCPURequest:    minCPUMillicores / 1000,
		RecommendationMinMemRequest:    minMemMi * 1024 * 1024,
		LogLevel:                       logLevel,
	}, nil
}

// stepPattern matches a positive integer followed by a time unit (seconds,
// minutes, hours, or days), e.g. "30s", "5m", "1h", "1d".
var stepPattern = regexp.MustCompile(`^[1-9][0-9]*[smhd]$`)

func validateStep(name, value string) error {
	if !stepPattern.MatchString(value) {
		return fmt.Errorf("%s must be a positive integer followed by s/m/h/d (e.g. 5m), got: %q", name, value)
	}
	return nil
}

func validateURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be a valid URL with scheme and host, got: %q", name, value)
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) (float64, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", key, value, err)
	}
	return parsed, nil
}
