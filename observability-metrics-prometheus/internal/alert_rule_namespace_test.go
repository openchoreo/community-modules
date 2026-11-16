// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
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
	req := namespacedRuleRequest(namespace, 5)
	rule, err := prometheus.BuildPrometheusRule(alertRuleParamsFromRequest(req), testAlertNamespace)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rule.Name = sharedRuleName
	rule.Labels = nil
	if err := c.CreatePrometheusRule(context.Background(), rule); err != nil {
		t.Fatalf("create legacy rule: %v", err)
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
