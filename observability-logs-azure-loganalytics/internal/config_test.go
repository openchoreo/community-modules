// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

// setRequiredEnv sets the variables LoadConfig refuses to start without.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"LOG_ANALYTICS_WORKSPACE_ID": "00000000-0000-0000-0000-000000000000",
		"AZURE_SUBSCRIPTION_ID":      "sub",
		"AZURE_RESOURCE_GROUP":       "rg",
		"WORKSPACE_RESOURCE_ID":      "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws",
		"ACTION_GROUP_ID":            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/actionGroups/ag",
		"OBSERVER_URL":               "http://observer:8081",
		"WEBHOOK_AUTH_ENABLED":       "false",
	} {
		t.Setenv(k, v)
	}
}

func TestLoadConfig_EventsDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EventsTable != loganalytics.DefaultEventsTable {
		t.Errorf("EventsTable = %q, want %q", cfg.EventsTable, loganalytics.DefaultEventsTable)
	}
	if cfg.EventsScopeName != loganalytics.DefaultEventsScopeName {
		t.Errorf("EventsScopeName = %q, want %q", cfg.EventsScopeName, loganalytics.DefaultEventsScopeName)
	}
}

func TestLoadConfig_EventsOverrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EVENTS_TABLE", "KubeEvents_CL")
	t.Setenv("EVENTS_SCOPE_NAME", "custom/scope")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EventsTable != "KubeEvents_CL" || cfg.EventsScopeName != "custom/scope" {
		t.Errorf("got table=%q scope=%q", cfg.EventsTable, cfg.EventsScopeName)
	}
}

// The table is written into queries as an identifier, so anything that is not
// a plain table name is refused at startup.
func TestLoadConfig_RejectsInvalidEventsTable(t *testing.T) {
	for _, table := range []string{"OTelLogs | take 1", "1Table", "Table;drop", "OTel-Logs"} {
		t.Run(table, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("EVENTS_TABLE", table)

			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), "EVENTS_TABLE") {
				t.Errorf("want an EVENTS_TABLE error, got %v", err)
			}
		})
	}
}

func TestLoadConfig_AuditDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Audit is opt-in: without a workspace the adapter declines rather than
	// querying the container-logs workspace for a table that is not there.
	if cfg.AuditWorkspaceID != "" {
		t.Errorf("AuditWorkspaceID = %q, want empty", cfg.AuditWorkspaceID)
	}
	if cfg.AuditTable != loganalytics.DefaultAuditTable {
		t.Errorf("AuditTable = %q, want %q", cfg.AuditTable, loganalytics.DefaultAuditTable)
	}
}

func TestLoadConfig_AuditOverrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("AUDIT_WORKSPACE_ID", "audit-ws-guid")
	t.Setenv("AUDIT_TABLE", "MyAudit_CL")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AuditWorkspaceID != "audit-ws-guid" || cfg.AuditTable != "MyAudit_CL" {
		t.Errorf("got workspace=%q table=%q", cfg.AuditWorkspaceID, cfg.AuditTable)
	}
}

// Validated even when audit is off, so a typo surfaces at startup rather than
// the first time someone opens the audit trail.
func TestLoadConfig_RejectsInvalidAuditTable(t *testing.T) {
	for _, table := range []string{"Audit | take 1", "1Audit", "Audit;drop", "Audit-Log"} {
		t.Run(table, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("AUDIT_TABLE", table)

			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), "AUDIT_TABLE") {
				t.Errorf("want an AUDIT_TABLE error, got %v", err)
			}
		})
	}
}
