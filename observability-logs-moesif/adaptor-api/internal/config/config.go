// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	AuthModeAPIKey = "api_key"
	AuthModeBearer = "bearer"
)

type Config struct {
	ServerPort        string
	SearchEndpoint    string
	SearchAuthMode    string
	EnvAPIBaseURL     string
	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
	EnvTokens         map[string]string // environment name -> bearer token
}

// AuthMode returns the configured authentication mode.
func (c *Config) AuthMode() string {
	return c.SearchAuthMode
}

// LoadConfig loads and validates configuration from environment variables.
func LoadConfig() (*Config, error) {
	serverPort := getEnv("SERVER_PORT", "9098")
	searchEndpoint := getEnv("SEARCH_ENDPOINT", "https://api.moesif.com")
	searchAuthMode := getEnv("SEARCH_AUTH_MODE", AuthModeBearer)
	envAPIBaseURL := getEnv("ENV_API_BASE_URL", "http://openchoreo-api.openchoreo-control-plane:8080")
	oauthTokenURL := getEnv("OAUTH_TOKEN_URL", "http://thunder.openchoreo.localhost:8080/oauth2/token")
	oauthClientID := getEnv("OAUTH_CLIENT_ID", "openchoreo-observer-resource-reader-client")
	oauthClientSecret := getEnv("OAUTH_CLIENT_SECRET", "openchoreo-observer-resource-reader-client-secret")

	if _, err := strconv.Atoi(serverPort); err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT %q: %w", serverPort, err)
	}

	if searchEndpoint == "" {
		return nil, fmt.Errorf("environment variable SEARCH_ENDPOINT is required")
	}
	parsed, err := url.Parse(searchEndpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("SEARCH_ENDPOINT must be a valid URL with scheme and host, got: %q", searchEndpoint)
	}

	return &Config{
		ServerPort:        serverPort,
		SearchEndpoint:    searchEndpoint,
		SearchAuthMode:    searchAuthMode,
		EnvAPIBaseURL:     envAPIBaseURL,
		OAuthTokenURL:     oauthTokenURL,
		OAuthClientID:     oauthClientID,
		OAuthClientSecret: oauthClientSecret,
		EnvTokens:         loadEnvTokens(),
	}, nil
}

// loadEnvTokens reads token files from /etc/moesif/env directory.
// Each file name is the environment name and its content is the token.
// e.g. /etc/moesif/env/development contains "dev.token" → {"development": "dev.token"}
func loadEnvTokens() map[string]string {
	const tokenDir = "/etc/moesif/env"
	tokens := make(map[string]string)
	entries, err := os.ReadDir(tokenDir)
	if err != nil {
		return tokens
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		data, err := os.ReadFile(tokenDir + "/" + entry.Name())
		if err != nil {
			continue
		}
		tokens[name] = strings.TrimSpace(string(data))
	}
	return tokens
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
