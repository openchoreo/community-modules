// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"testing"
	"time"
)

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{
		"SERVER_PORT", "LOG_LEVEL", "DT_PLATFORM_URL", "DT_AUTH_MODE", "DT_PLATFORM_TOKEN", "DT_QUERY_TIMEOUT",
		"DT_OAUTH_CLIENT_ID", "DT_OAUTH_CLIENT_SECRET", "DT_OAUTH_TOKEN_URL", "DT_OAUTH_SCOPE", "DT_OAUTH_RESOURCE",
		"DT_CONTAINER_LOGS_SOURCE", "DT_AUDIT_LOGS_SOURCE", "DT_AUDIT_BUCKET", "DT_ALLOW_INSECURE_HTTP",
	} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DT_PLATFORM_URL": "https://abc.apps.dynatrace.com/", "DT_PLATFORM_TOKEN": "tok"})
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlatformURL != "https://abc.apps.dynatrace.com" || cfg.ServerPort != "9098" || cfg.AuthMode != AuthModePlatformToken ||
		cfg.QueryTimeout != 30*time.Second || cfg.ContainerLogsSource != "openchoreo-container-logs" ||
		cfg.AuditLogsSource != "openchoreo-audit-logs" || cfg.LogLevel != slog.LevelInfo || cfg.AllowInsecureHTTP {
		t.Errorf("unexpected defaults %+v", cfg)
	}
}

func TestLoadConfigAllowInsecureHTTP(t *testing.T) {
	setEnv(t, map[string]string{
		"DT_PLATFORM_URL": "http://dtmock.dt-mock:8080", "DT_AUTH_MODE": "oauth",
		"DT_OAUTH_CLIENT_ID": "id", "DT_OAUTH_CLIENT_SECRET": "s", "DT_OAUTH_TOKEN_URL": "http://dtmock.dt-mock:8080/token",
		"DT_ALLOW_INSECURE_HTTP": "true",
	})
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowInsecureHTTP || cfg.PlatformURL != "http://dtmock.dt-mock:8080" {
		t.Errorf("unexpected %+v", cfg)
	}
}

func TestLoadConfigOAuth(t *testing.T) {
	setEnv(t, map[string]string{
		"DT_PLATFORM_URL": "https://abc.apps.dynatrace.com", "DT_AUTH_MODE": "oauth",
		"DT_OAUTH_CLIENT_ID": "id", "DT_OAUTH_CLIENT_SECRET": "s", "DT_OAUTH_RESOURCE": "urn:dtaccount:x",
		"LOG_LEVEL": "debug", "DT_QUERY_TIMEOUT": "45s",
	})
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth.ClientID != "id" || cfg.OAuth.Resource != "urn:dtaccount:x" || cfg.LogLevel != slog.LevelDebug || cfg.QueryTimeout != 45*time.Second {
		t.Errorf("unexpected %+v", cfg)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	base := map[string]string{"DT_PLATFORM_URL": "https://abc.apps.dynatrace.com", "DT_PLATFORM_TOKEN": "tok"}
	with := func(overrides map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range overrides {
			m[k] = v
		}
		return m
	}
	for name, env := range map[string]map[string]string{
		"missing url":  {"DT_PLATFORM_TOKEN": "tok"},
		"bad url":      with(map[string]string{"DT_PLATFORM_URL": "abc.apps.dynatrace.com"}),
		"ftp url":      with(map[string]string{"DT_PLATFORM_URL": "ftp://abc"}),
		"http url":     with(map[string]string{"DT_PLATFORM_URL": "http://abc.apps.dynatrace.com"}),
		"no host url":  with(map[string]string{"DT_PLATFORM_URL": "https://"}),
		"insecure ftp": with(map[string]string{"DT_PLATFORM_URL": "ftp://abc", "DT_ALLOW_INSECURE_HTTP": "true"}),
		"bad insecure": with(map[string]string{"DT_ALLOW_INSECURE_HTTP": "maybe"}),
		"oauth http token": with(map[string]string{
			"DT_AUTH_MODE": "oauth", "DT_OAUTH_CLIENT_ID": "id", "DT_OAUTH_CLIENT_SECRET": "s",
			"DT_OAUTH_TOKEN_URL": "http://sso.dynatrace.com/sso/oauth2/token",
		}),
		"missing token":   {"DT_PLATFORM_URL": "https://abc.apps.dynatrace.com"},
		"oauth no client": with(map[string]string{"DT_AUTH_MODE": "oauth"}),
		"bad auth mode":   with(map[string]string{"DT_AUTH_MODE": "basic"}),
		"bad port":        with(map[string]string{"SERVER_PORT": "http"}),
		"bad timeout":     with(map[string]string{"DT_QUERY_TIMEOUT": "soon"}),
		"same sources":    with(map[string]string{"DT_CONTAINER_LOGS_SOURCE": "x", "DT_AUDIT_LOGS_SOURCE": "x"}),
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, env)
			if _, err := LoadConfig(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"": slog.LevelInfo, "warning": slog.LevelWarn, "ERROR": slog.LevelError, "x": slog.LevelInfo} {
		if got := parseLogLevel(in); got != want {
			t.Errorf("parseLogLevel(%q) = %v", in, got)
		}
	}
}
