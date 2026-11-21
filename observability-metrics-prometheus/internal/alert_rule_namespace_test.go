// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/k8s"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
)

const sharedRuleName = "api-development-1a2b3c4d-cpu-high"

func namespacedRuleRequest(namespace string, threshold float32) gen.AlertRuleRequest {
	req := testAlertRuleRequest(sharedRuleName, true)
	req.Metadata.Namespace = namespace
	req.Condition.Threshold = threshold
	return req
}

func ruleThreshold(t *testing.T, c *k8s.Client, objectName string) string {
	t.Helper()
	rule, err := c.GetPrometheusRule(context.Background(), objectName)
	if err != nil {
		t.Fatalf("get PrometheusRule %q: %v", objectName, err)
	}
	return rule.Spec.Groups[0].Rules[0].Expr.String()
}

func mustCreate(t *testing.T, h *MetricsHandler, req gen.AlertRuleRequest) {
	t.Helper()
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &req})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule201JSONResponse); !ok {
		t.Fatalf("expected 201, got %T", resp)
	}
}

// createLegacyRule creates a PrometheusRule the way earlier versions did: named after the rule alone.
func createLegacyRule(t *testing.T, c *k8s.Client, namespace string) {
	t.Helper()
	createLegacyRuleNamed(t, c, namespace, sharedRuleName)
}

// createLegacyRuleNamed creates the legacy PrometheusRule of the rule with the given namespace and name.
func createLegacyRuleNamed(t *testing.T, c *k8s.Client, namespace, ruleName string) {
	t.Helper()
	req := namespacedRuleRequest(namespace, 5)
	req.Metadata.Name = ruleName
	rule, err := prometheus.BuildPrometheusRule(alertRuleParamsFromRequest(req), testAlertNamespace)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rule.Name = ruleName
	rule.Labels = nil
	delete(rule.Spec.Groups[0].Rules[0].Labels, prometheus.RuleNamespaceAlertLabel)
	if err := c.CreatePrometheusRule(context.Background(), rule); err != nil {
		t.Fatalf("create legacy rule: %v", err)
	}
}

// requireRuleOf fails unless the PrometheusRule with the given name exists and was built from the
// rule with the given namespace and name.
func requireRuleOf(t *testing.T, c *k8s.Client, objectName, namespace, name string) {
	t.Helper()
	rule, err := c.GetPrometheusRule(context.Background(), objectName)
	if err != nil {
		t.Fatalf("get PrometheusRule %q: %v", objectName, err)
	}
	if ns, n := prometheus.RuleIdentity(rule); ns != namespace || n != name {
		t.Fatalf("PrometheusRule %q belongs to %s/%s, want %s/%s", objectName, ns, n, namespace, name)
	}
}

func TestAlertRules_SameNameInTwoNamespacesAreIndependent(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	objA := prometheus.ObjectName("tenant-a", sharedRuleName)
	objB := prometheus.ObjectName("tenant-b", sharedRuleName)

	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))
	mustCreate(t, h, namespacedRuleRequest("tenant-b", 80))
	exprA := ruleThreshold(t, c, objA)
	if exprA == ruleThreshold(t, c, objB) {
		t.Fatalf("expected different expressions for the two namespaces")
	}

	nsB := "tenant-b"
	reqB := namespacedRuleRequest("tenant-b", 90)
	resp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsB}, Body: &reqB,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if got := ruleThreshold(t, c, objA); got != exprA {
		t.Fatalf("updating tenant-b changed tenant-a's rule: %q", got)
	}

	getResp, err := h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.GetAlertRuleParams{Namespace: strPtr("tenant-a")},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, ok := getResp.(gen.GetAlertRule200JSONResponse)
	if !ok || got.Metadata == nil || derefString(got.Metadata.Namespace) != "tenant-a" {
		t.Fatalf("expected tenant-a's rule, got %#v", getResp)
	}

	delResp, err := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsB},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := delResp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", delResp)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, objB); exists {
		t.Fatalf("tenant-b's rule was not deleted")
	}
	if exists, _ := c.PrometheusRuleExists(ctx, objA); !exists {
		t.Fatalf("deleting tenant-b's rule deleted tenant-a's rule")
	}

	// A namespace without the rule gets a 404, not another namespace's rule.
	getResp, err = h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.GetAlertRuleParams{Namespace: &nsB},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 for tenant-b, got %T", getResp)
	}
}

func TestUpdateAlertRule_NamespaceMismatch(t *testing.T) {
	h, _ := newAlertRuleTestHandler(t)
	req := namespacedRuleRequest("tenant-a", 5)
	other := "tenant-b"

	resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &other}, Body: &req,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestAlertRules_LegacyRuleIsMigratedByItsOwner(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	createLegacyRule(t, c, "tenant-a")
	nsA, nsB := "tenant-a", "tenant-b"

	// Another namespace does not see the legacy rule and can create its own.
	getResp, err := h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.GetAlertRuleParams{Namespace: &nsB},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 for tenant-b, got %T", getResp)
	}
	mustCreate(t, h, namespacedRuleRequest("tenant-b", 80))

	// The owner finds the legacy rule, and its next update migrates it.
	getResp, err = h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.GetAlertRuleParams{Namespace: &nsA},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 for tenant-a, got %T", getResp)
	}

	reqA := namespacedRuleRequest("tenant-a", 5)
	resp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsA}, Body: &reqA,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, ok := resp.(gen.UpdateAlertRule200JSONResponse)
	if !ok || *updated.Body.Action != gen.Updated {
		t.Fatalf("expected 200 updated, got %#v", resp)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, sharedRuleName); exists {
		t.Fatalf("legacy rule was not removed")
	}
	if exists, _ := c.PrometheusRuleExists(ctx, prometheus.ObjectName("tenant-a", sharedRuleName)); !exists {
		t.Fatalf("migrated rule was not created")
	}
	if exists, _ := c.PrometheusRuleExists(ctx, prometheus.ObjectName("tenant-b", sharedRuleName)); !exists {
		t.Fatalf("tenant-b's rule was lost")
	}
}

func TestDeleteAlertRule_RemovesCurrentAndOwnedLegacyRule(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	// Both exist when a migration was interrupted before the legacy rule was deleted.
	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))
	createLegacyRule(t, c, "tenant-a")
	nsA := "tenant-a"

	resp, err := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsA},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	rules, err := c.ListPrometheusRules(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected no rules left, got %d", len(rules))
	}
}

func TestDeleteAlertRule_KeepsLegacyRuleOfAnotherNamespace(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	createLegacyRule(t, c, "tenant-a")
	nsB := "tenant-b"

	resp, err := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsB},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", resp)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, sharedRuleName); !exists {
		t.Fatalf("tenant-b deleted tenant-a's legacy rule")
	}
}

// TestGetAlertRule_WithoutNamespace covers observers that predate the namespace parameter.
func TestGetAlertRule_WithoutNamespace(t *testing.T) {
	h, _ := newAlertRuleTestHandler(t)
	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))

	resp, err := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: sharedRuleName})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := resp.(gen.GetAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

// TestUpdateAlertRule_RemovesLeftoverLegacyRule covers a legacy PrometheusRule left next to the
// current one, as an interrupted migration or a module downgrade leaves it.
func TestUpdateAlertRule_RemovesLeftoverLegacyRule(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))
	createLegacyRule(t, c, "tenant-a")

	nsA := "tenant-a"
	reqA := namespacedRuleRequest("tenant-a", 5)
	resp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsA}, Body: &reqA,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, sharedRuleName); exists {
		t.Fatalf("leftover legacy rule was not removed")
	}
	if exists, _ := c.PrometheusRuleExists(ctx, prometheus.ObjectName("tenant-a", sharedRuleName)); !exists {
		t.Fatalf("current rule was removed")
	}
}

func TestUpdateAlertRule_KeepsLegacyRuleOfAnotherNamespace(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	mustCreate(t, h, namespacedRuleRequest("tenant-b", 80))
	createLegacyRule(t, c, "tenant-a")

	nsB := "tenant-b"
	reqB := namespacedRuleRequest("tenant-b", 80)
	if _, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsB}, Body: &reqB,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, sharedRuleName); !exists {
		t.Fatalf("tenant-b removed tenant-a's legacy rule")
	}
}

// TestAlertRules_SameNameAlertsHaveDistinctLabels covers Alertmanager, which identifies an alert by
// its labels. The alert expressions aggregate away every series label, so without the rule
// namespace label the alerts of same-named rules from different namespaces would be merged.
func TestAlertRules_SameNameAlertsHaveDistinctLabels(t *testing.T) {
	h, c := newAlertRuleTestHandler(t)
	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))
	mustCreate(t, h, namespacedRuleRequest("tenant-b", 5))

	labels := func(namespace string) map[string]string {
		rule, err := c.GetPrometheusRule(context.Background(), prometheus.ObjectName(namespace, sharedRuleName))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return rule.Spec.Groups[0].Rules[0].Labels
	}
	labelsA, labelsB := labels("tenant-a"), labels("tenant-b")
	if maps.Equal(labelsA, labelsB) {
		t.Fatalf("alerts of the two namespaces have the same labels: %v", labelsA)
	}
	if got := labelsA[prometheus.RuleNamespaceAlertLabel]; got != "tenant-a" {
		t.Fatalf("expected %s=tenant-a, got %q", prometheus.RuleNamespaceAlertLabel, got)
	}
}

// TestAlertRules_GeneratedNameHeldByAnotherRule covers a legacy PrometheusRule whose rule is named
// like another rule's generated PrometheusRule name. That PrometheusRule is not the other rule's.
func TestAlertRules_GeneratedNameHeldByAnotherRule(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	objA := prometheus.ObjectName("tenant-a", sharedRuleName)
	createLegacyRuleNamed(t, c, "tenant-b", objA)
	nsA := "tenant-a"

	getResp, err := h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.GetAlertRuleParams{Namespace: &nsA},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", getResp)
	}

	reqA := namespacedRuleRequest("tenant-a", 50)
	updResp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsA}, Body: &reqA,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := updResp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 not found, got %T", updResp)
	}

	createResp, err := h.CreateAlertRule(ctx, gen.CreateAlertRuleRequestObject{Body: &reqA})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	conflict, ok := createResp.(gen.CreateAlertRule409JSONResponse)
	if !ok {
		t.Fatalf("expected 409, got %T", createResp)
	}
	if detail := derefString(conflict.Detail); !strings.Contains(detail, "held by another alert rule") || strings.Contains(detail, "tenant-b") {
		t.Fatalf("unexpected 409 detail: %q", detail)
	}

	delResp, err := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsA},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := delResp.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", delResp)
	}

	requireRuleOf(t, c, objA, "tenant-b", objA)
	if got := ruleThreshold(t, c, objA); !strings.HasSuffix(got, " > 5") {
		t.Fatalf("tenant-a changed the PrometheusRule of tenant-b: %q", got)
	}
}

// TestUpdateAlertRule_MigrationKeepsLegacyRuleWhenNameIsHeld covers migrating a legacy rule whose
// generated name is held by another rule's PrometheusRule. Neither PrometheusRule may be lost.
func TestUpdateAlertRule_MigrationKeepsLegacyRuleWhenNameIsHeld(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	objA := prometheus.ObjectName("tenant-a", sharedRuleName)
	createLegacyRule(t, c, "tenant-a")
	createLegacyRuleNamed(t, c, "tenant-b", objA)
	nsA := "tenant-a"

	reqA := namespacedRuleRequest("tenant-a", 50)
	resp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsA}, Body: &reqA,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	failed, ok := resp.(gen.UpdateAlertRule500JSONResponse)
	if !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
	if detail := derefString(failed.Detail); !strings.Contains(detail, "held by another alert rule") {
		t.Fatalf("unexpected 500 detail: %q", detail)
	}

	requireRuleOf(t, c, sharedRuleName, "tenant-a", sharedRuleName)
	requireRuleOf(t, c, objA, "tenant-b", objA)
	if got := ruleThreshold(t, c, objA); !strings.HasSuffix(got, " > 5") {
		t.Fatalf("tenant-a changed the PrometheusRule of tenant-b: %q", got)
	}
}

// TestMigrateLegacyRule_UpdatesCurrentRuleCreatedConcurrently covers a migration that finds its
// current PrometheusRule already created, as a concurrent migration of the same rule does.
func TestMigrateLegacyRule_UpdatesCurrentRuleCreatedConcurrently(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	objA := prometheus.ObjectName("tenant-a", sharedRuleName)
	createLegacyRule(t, c, "tenant-a")
	concurrent, err := prometheus.BuildPrometheusRule(alertRuleParamsFromRequest(namespacedRuleRequest("tenant-a", 5)), testAlertNamespace)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := c.CreatePrometheusRule(ctx, concurrent); err != nil {
		t.Fatalf("create current rule: %v", err)
	}

	legacy, err := c.GetPrometheusRule(ctx, sharedRuleName)
	if err != nil {
		t.Fatalf("get legacy rule: %v", err)
	}
	params := alertRuleParamsFromRequest(namespacedRuleRequest("tenant-a", 50))
	newRule, err := prometheus.BuildPrometheusRule(params, testAlertNamespace)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	resp, err := h.migrateLegacyRule(ctx, legacy, newRule, params)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %#v", resp)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, sharedRuleName); exists {
		t.Fatalf("legacy rule was not removed")
	}
	if got := ruleThreshold(t, c, objA); !strings.HasSuffix(got, " > 50") {
		t.Fatalf("current rule was not updated: %q", got)
	}
}

// TestAlertRules_WithoutNamespaceSkipsAnotherRulesCurrentRule covers observers without the
// namespace parameter. A rule named like another rule's generated PrometheusRule name must not
// resolve to that PrometheusRule.
func TestAlertRules_WithoutNamespaceSkipsAnotherRulesCurrentRule(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))
	objA := prometheus.ObjectName("tenant-a", sharedRuleName)

	getResp, err := h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{RuleName: objA})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", getResp)
	}

	delResp, err := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{RuleName: objA})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := delResp.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", delResp)
	}
	requireRuleOf(t, c, objA, "tenant-a", sharedRuleName)
}

// TestDeleteAlertRule_RemovesLegacyRuleWhileCurrentRuleDeletionIsPending covers a current
// PrometheusRule that a finalizer keeps readable after its deletion. The legacy PrometheusRule must
// still be deleted.
func TestDeleteAlertRule_RemovesLegacyRuleWhileCurrentRuleDeletionIsPending(t *testing.T) {
	ctx := context.Background()
	h, c := newAlertRuleTestHandler(t)
	objA := prometheus.ObjectName("tenant-a", sharedRuleName)
	mustCreate(t, h, namespacedRuleRequest("tenant-a", 5))
	current, err := c.GetPrometheusRule(ctx, objA)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	current.Finalizers = []string{"example.com/hold"}
	if err := c.UpdatePrometheusRule(ctx, current); err != nil {
		t.Fatalf("add finalizer: %v", err)
	}
	createLegacyRule(t, c, "tenant-a")
	nsA := "tenant-a"

	resp, err := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsA},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if exists, _ := c.PrometheusRuleExists(ctx, sharedRuleName); exists {
		t.Fatalf("legacy rule was not deleted")
	}
	pending, err := c.GetPrometheusRule(ctx, objA)
	if err != nil {
		t.Fatalf("get current rule: %v", err)
	}
	if pending.DeletionTimestamp == nil {
		t.Fatalf("current rule was not deleted")
	}
}
