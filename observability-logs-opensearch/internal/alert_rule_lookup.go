// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

// ruleMonitor is the monitor of an alert rule.
type ruleMonitor struct {
	id string
	// legacy marks a monitor named after the rule alone, as earlier versions named them.
	legacy bool
}

var (
	webhookRuleName      = regexp.MustCompile(`"ruleName":("(?:[^"\\]|\\.)*")`)
	webhookRuleNamespace = regexp.MustCompile(`"ruleNamespace":("(?:[^"\\]|\\.)*")`)
)

// findRuleMonitor returns the monitor of the alert rule with the given namespace and name, or nil
// when there is none. See findRuleMonitors.
func (h *LogsHandler) findRuleMonitor(ctx context.Context, namespace, name string) (*ruleMonitor, error) {
	monitors, err := h.findRuleMonitors(ctx, namespace, name)
	if err != nil || len(monitors) == 0 {
		return nil, err
	}
	return &monitors[0], nil
}

// findRuleMonitors returns the monitors of the alert rule with the given namespace and name: the
// current one first, then a legacy one. A legacy monitor is only returned when it was built from
// this namespace's rule. Without a namespace, it returns the first monitor of a rule with that
// name, in any namespace, for observers that predate namespaced rule identity.
func (h *LogsHandler) findRuleMonitors(ctx context.Context, namespace, name string) ([]ruleMonitor, error) {
	if namespace == "" {
		return h.findRuleMonitorsByName(ctx, name)
	}

	var monitors []ruleMonitor
	refs, err := h.osClient.SearchMonitorsByName(ctx, opensearch.MonitorName(namespace, name))
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref.Name == opensearch.MonitorName(namespace, name) {
			monitors = append(monitors, ruleMonitor{id: ref.ID})
			break
		}
	}

	refs, err = h.osClient.SearchMonitorsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref.Name != name {
			continue
		}
		monitor, err := h.osClient.GetMonitorByID(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		if ns, n := ruleIdentity(monitor); ns == namespace && n == name {
			monitors = append(monitors, ruleMonitor{id: ref.ID, legacy: true})
			break
		}
	}
	return monitors, nil
}

// findRuleMonitorsByName returns the first monitor of a rule with the given name, in any namespace.
func (h *LogsHandler) findRuleMonitorsByName(ctx context.Context, name string) ([]ruleMonitor, error) {
	refs, err := h.osClient.SearchMonitorsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref.Name == name {
			return []ruleMonitor{{id: ref.ID, legacy: true}}, nil
		}
	}
	for _, ref := range refs {
		if strings.HasSuffix(ref.Name, "/"+name) {
			return []ruleMonitor{{id: ref.ID}}, nil
		}
	}
	return nil, nil
}

// deleteRuleMonitors deletes the monitors findRuleMonitors returns. It returns the ID of the first
// deleted monitor and whether any was deleted.
func (h *LogsHandler) deleteRuleMonitors(ctx context.Context, namespace, name string) (string, bool, error) {
	monitors, err := h.findRuleMonitors(ctx, namespace, name)
	if err != nil {
		return "", false, fmt.Errorf("failed to find monitor: %w", err)
	}
	for _, m := range monitors {
		if err := h.osClient.DeleteMonitor(ctx, m.id); err != nil {
			return "", false, fmt.Errorf("failed to delete monitor: %w", err)
		}
	}
	if len(monitors) == 0 {
		return "", false, nil
	}
	return monitors[0].id, true, nil
}

// ruleIdentity reads the namespace and name of the alert rule a monitor was built from, out of the
// webhook message of its trigger.
func ruleIdentity(monitor map[string]interface{}) (namespace, name string) {
	triggers, _ := monitor["triggers"].([]interface{})
	for _, t := range triggers {
		trigger, _ := t.(map[string]interface{})
		qlt, _ := trigger["query_level_trigger"].(map[string]interface{})
		actions, _ := qlt["actions"].([]interface{})
		for _, a := range actions {
			action, _ := a.(map[string]interface{})
			tmpl, _ := action["message_template"].(map[string]interface{})
			source := getStringFromMap(tmpl, "source")
			name = jsonStringMatch(webhookRuleName, source)
			if name != "" {
				return jsonStringMatch(webhookRuleNamespace, source), name
			}
		}
	}
	return "", ""
}

// jsonStringMatch returns the JSON string literal the pattern's first group captures, decoded.
func jsonStringMatch(pattern *regexp.Regexp, s string) string {
	m := pattern.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	var v string
	if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
		return ""
	}
	return v
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
