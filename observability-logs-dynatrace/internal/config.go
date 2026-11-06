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
	"time"

	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/dynatrace"
)

// Auth modes for the Grail query API.
const (
	AuthModePlatformToken = "platformToken"
	AuthModeOAuth         = "oauth"
)

// Config is the adapter configuration, read from the environment.
type Config struct {
	ServerPort string
	LogLevel   slog.Level

	PlatformURL  string
	AuthMode     string
	QueryTimeout time.Duration

	// AllowInsecureHTTP accepts http:// for the credential-bearing URLs. For test doubles
	// only: credentials then cross the network in cleartext.
	AllowInsecureHTTP bool

	PlatformToken string
	OAuth         dynatrace.OAuthConfig

	ContainerLogsSource string
	AuditLogsSource     string
	AuditBucket         string
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ServerPort:          getEnv("SERVER_PORT", "9098"),
		LogLevel:            parseLogLevel(os.Getenv("LOG_LEVEL")),
		PlatformURL:         strings.TrimRight(getEnv("DT_PLATFORM_URL", ""), "/"),
		AuthMode:            getEnv("DT_AUTH_MODE", AuthModePlatformToken),
		PlatformToken:       os.Getenv("DT_PLATFORM_TOKEN"),
		ContainerLogsSource: getEnv("DT_CONTAINER_LOGS_SOURCE", "openchoreo-container-logs"),
		AuditLogsSource:     getEnv("DT_AUDIT_LOGS_SOURCE", "openchoreo-audit-logs"),
		AuditBucket:         os.Getenv("DT_AUDIT_BUCKET"),
		OAuth: dynatrace.OAuthConfig{
			TokenURL:     getEnv("DT_OAUTH_TOKEN_URL", dynatrace.DefaultOAuthTokenURL),
			ClientID:     os.Getenv("DT_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("DT_OAUTH_CLIENT_SECRET"),
			Scope:        getEnv("DT_OAUTH_SCOPE", dynatrace.DefaultOAuthScope),
			Resource:     os.Getenv("DT_OAUTH_RESOURCE"),
		},
	}

	if _, err := strconv.Atoi(cfg.ServerPort); err != nil {
		return nil, fmt.Errorf("invalid SERVER_PORT: %w", err)
	}

	allowInsecure, err := strconv.ParseBool(getEnv("DT_ALLOW_INSECURE_HTTP", "false"))
	if err != nil {
		return nil, fmt.Errorf("DT_ALLOW_INSECURE_HTTP must be true or false: %w", err)
	}
	cfg.AllowInsecureHTTP = allowInsecure

	if cfg.PlatformURL == "" {
		return nil, fmt.Errorf("environment variable DT_PLATFORM_URL is required")
	}
	if err := checkCredentialURL("DT_PLATFORM_URL", cfg.PlatformURL, cfg.AllowInsecureHTTP); err != nil {
		return nil, err
	}

	timeout, err := time.ParseDuration(getEnv("DT_QUERY_TIMEOUT", "30s"))
	if err != nil || timeout <= 0 {
		return nil, fmt.Errorf("DT_QUERY_TIMEOUT must be a positive duration such as 30s")
	}
	cfg.QueryTimeout = timeout

	switch cfg.AuthMode {
	case AuthModePlatformToken:
		if cfg.PlatformToken == "" {
			return nil, fmt.Errorf("environment variable DT_PLATFORM_TOKEN is required when DT_AUTH_MODE=%s", AuthModePlatformToken)
		}
	case AuthModeOAuth:
		if cfg.OAuth.ClientID == "" || cfg.OAuth.ClientSecret == "" {
			return nil, fmt.Errorf("DT_OAUTH_CLIENT_ID and DT_OAUTH_CLIENT_SECRET are required when DT_AUTH_MODE=%s", AuthModeOAuth)
		}
		if err := checkCredentialURL("DT_OAUTH_TOKEN_URL", cfg.OAuth.TokenURL, cfg.AllowInsecureHTTP); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("DT_AUTH_MODE must be %q or %q, got %q", AuthModePlatformToken, AuthModeOAuth, cfg.AuthMode)
	}

	if cfg.ContainerLogsSource == cfg.AuditLogsSource {
		return nil, fmt.Errorf("DT_CONTAINER_LOGS_SOURCE and DT_AUDIT_LOGS_SOURCE must differ, both are %q", cfg.AuditLogsSource)
	}

	return cfg, nil
}

// checkCredentialURL rejects a URL credentials are sent to unless it is https with a host.
// allowInsecure also accepts http.
func checkCredentialURL(name, raw string, allowInsecure bool) error {
	u, err := url.Parse(raw)
	if err == nil && u.Host != "" && (u.Scheme == "https" || (allowInsecure && u.Scheme == "http")) {
		return nil
	}
	if allowInsecure {
		return fmt.Errorf("%s must be an http(s) URL with a host, got: %q", name, raw)
	}
	return fmt.Errorf("%s must be an https URL with a host, got: %q (set DT_ALLOW_INSECURE_HTTP=true to allow http)", name, raw)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
