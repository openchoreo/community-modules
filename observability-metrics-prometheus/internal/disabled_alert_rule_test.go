// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/k8s"
)

const testAlertNamespace = "test-ns"

func newAlertRuleTestHandler(t *testing.T) (*MetricsHandler, *k8s.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := monitoringv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add monitoring scheme: %v", err)
	}
	k8sClient := k8s.NewClientFromRuntimeClient(fake.NewClientBuilder().WithScheme(scheme).Build(), testAlertNamespace)
	return NewMetricsHandler(nil, k8sClient, nil, testAlertNamespace, testLogger()), k8sClient
}

func testAlertRuleRequest(name string, enabled bool) gen.AlertRuleRequest {
	req := gen.AlertRuleRequest{}
	req.Metadata.Name = name
	req.Metadata.Namespace = "default"
	req.Metadata.ComponentUid = openapi_types.UUID(uuid.MustParse("a7f3c9e2-4b1d-4a8e-9c3f-2d5e7b8a1c4f"))
	req.Metadata.ProjectUid = openapi_types.UUID(uuid.MustParse("b8e4d0f3-5c2e-4b9f-8d4a-3e6f8c9b2d5e"))
	req.Metadata.EnvironmentUid = openapi_types.UUID(uuid.MustParse("c9f5e1a4-6d3f-4c0a-9e5b-4f7a9d0c3e6f"))
	req.Source.Metric = gen.AlertRuleRequestSourceMetricMemoryUsage
	req.Condition.Enabled = enabled
	req.Condition.Window = "5m"
	req.Condition.Interval = "1m"
	req.Condition.Operator = gen.AlertRuleRequestConditionOperatorGt
	req.Condition.Threshold = 80
	return req
}

func mustRuleExists(t *testing.T, c *k8s.Client, name string, want bool) {
	t.Helper()
	got, err := c.PrometheusRuleExists(context.Background(), name)
	if err != nil {
		t.Fatalf("PrometheusRuleExists: %v", err)
	}
	if got != want {
		t.Fatalf("PrometheusRule %q exists = %v, want %v", name, got, want)
	}
}

func TestCreateAlertRule_DisabledDoesNotCreatePrometheusRule(t *testing.T) {
	handler, k8sClient := newAlertRuleTestHandler(t)
	req := testAlertRuleRequest("mem-rule", false)

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &req})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	created, ok := resp.(gen.CreateAlertRule201JSONResponse)
	if !ok {
		t.Fatalf("expected 201 response, got %T", resp)
	}
	if *created.Body.Action != gen.Unchanged || *created.Body.Status != gen.Synced {
		t.Errorf("expected unchanged/synced, got %s/%s", *created.Body.Action, *created.Body.Status)
	}
	mustRuleExists(t, k8sClient, "mem-rule", false)
}

func TestCreateAlertRule_DisabledInvalidRuleRejected(t *testing.T) {
	handler, _ := newAlertRuleTestHandler(t)
	req := testAlertRuleRequest("mem-rule", false)
	req.Condition.Window = "not-a-duration"

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &req})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestUpdateAlertRule_DisableDeletesAndReenableRecreates(t *testing.T) {
	ctx := context.Background()
	handler, k8sClient := newAlertRuleTestHandler(t)

	enabledReq := testAlertRuleRequest("mem-rule", true)
	if _, err := handler.CreateAlertRule(ctx, gen.CreateAlertRuleRequestObject{Body: &enabledReq}); err != nil {
		t.Fatalf("create: %v", err)
	}
	mustRuleExists(t, k8sClient, "mem-rule", true)

	disabledReq := testAlertRuleRequest("mem-rule", false)
	resp, err := handler.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{RuleName: "mem-rule", Body: &disabledReq})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, ok := resp.(gen.UpdateAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if *updated.Body.Action != gen.Deleted || *updated.Body.Status != gen.Synced {
		t.Errorf("expected deleted/synced, got %s/%s", *updated.Body.Action, *updated.Body.Status)
	}
	mustRuleExists(t, k8sClient, "mem-rule", false)

	getResp, err := handler.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{RuleName: "mem-rule"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 for disabled rule, got %T", getResp)
	}

	// A repeated disable is a no-op.
	resp, err = handler.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{RuleName: "mem-rule", Body: &disabledReq})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, ok = resp.(gen.UpdateAlertRule200JSONResponse)
	if !ok || *updated.Body.Action != gen.Unchanged {
		t.Fatalf("expected 200 unchanged, got %T %+v", resp, resp)
	}

	// Re-enabling goes through create, since GET returns 404.
	createResp, err := handler.CreateAlertRule(ctx, gen.CreateAlertRuleRequestObject{Body: &enabledReq})
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	created, ok := createResp.(gen.CreateAlertRule201JSONResponse)
	if !ok || *created.Body.Action != gen.Created {
		t.Fatalf("expected 201 created, got %T %+v", createResp, createResp)
	}
	mustRuleExists(t, k8sClient, "mem-rule", true)
}
