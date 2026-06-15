// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/azuremonitor"
)

type fakeAlertClient struct {
	createErr   error
	findErr     error
	deleteErr   error
	findNS      string
	lastCreated azuremonitor.RuleInput
}

func (f *fakeAlertClient) CreateRule(_ context.Context, in azuremonitor.RuleInput) (*azuremonitor.RuleResult, error) {
	f.lastCreated = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &azuremonitor.RuleResult{BackendID: "/arm/id", LogicalID: "oc-abc", LastSynced: "2026-06-05T00:00:00Z"}, nil
}

func (f *fakeAlertClient) UpdateRule(_ context.Context, in azuremonitor.RuleInput) (*azuremonitor.RuleResult, error) {
	return f.CreateRule(context.Background(), in)
}

func (f *fakeAlertClient) FindRuleByName(_ context.Context, _ string) (*azuremonitor.RuleResult, string, error) {
	if f.findErr != nil {
		return nil, "", f.findErr
	}
	return &azuremonitor.RuleResult{BackendID: "/arm/id", LogicalID: "oc-abc"}, f.findNS, nil
}

func (f *fakeAlertClient) DeleteRuleByAzureName(_ context.Context, _ string) error {
	return f.deleteErr
}

type fakeObserver struct {
	called bool
	err    error
}

func (f *fakeObserver) ForwardAlert(_ context.Context, _, _ string, _ float64, _ time.Time) error {
	f.called = true
	return f.err
}

func newAlertingHandler(ac alertClient, of observerForwarder) *MetricsHandler {
	return NewMetricsHandlerWithAlerting(&fakeClient{}, ac, of, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func validAlertBody() *gen.AlertRuleRequest {
	b := &gen.AlertRuleRequest{}
	b.Metadata.Name = "high-cpu"
	b.Metadata.Namespace = "dp-acme-dev"
	b.Metadata.ComponentUid = openapi_types.UUID{}
	b.Source.Metric = "cpu_usage"
	b.Condition.Operator = "gt"
	b.Condition.Threshold = 0.8
	b.Condition.Interval = "5m"
	b.Condition.Window = "10m"
	b.Condition.Enabled = true
	return b
}

func TestCreateAlertRule_OK(t *testing.T) {
	ac := &fakeAlertClient{}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertBody()})
	if _, ok := resp.(gen.CreateAlertRule201JSONResponse); !ok {
		t.Fatalf("expected 201, got %T", resp)
	}
	if ac.lastCreated.Metric != "cpu_usage" {
		t.Errorf("metric not passed through: %q", ac.lastCreated.Metric)
	}
}

func TestCreateAlertRule_Conflict(t *testing.T) {
	ac := &fakeAlertClient{createErr: azuremonitor.ErrAlreadyExists}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertBody()})
	if _, ok := resp.(gen.CreateAlertRule409JSONResponse); !ok {
		t.Fatalf("expected 409 when rule already exists, got %T", resp)
	}
}

func TestCreateAlertRule_BadMetric(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{}, &fakeObserver{})
	body := validAlertBody()
	body.Source.Metric = "budget"
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: body})
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 for unsupported metric, got %T", resp)
	}
}

func TestCreateAlertRule_MissingFields(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{}, &fakeObserver{})
	body := validAlertBody()
	body.Metadata.Namespace = ""
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: body})
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 for missing namespace, got %T", resp)
	}
}

func TestGetAlertRule_NotFound(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{findErr: azuremonitor.ErrNotFound}, &fakeObserver{})
	resp, _ := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "nope"})
	if _, ok := resp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", resp)
	}
}

func TestGetAlertRule_OK(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{findNS: "dp-acme-dev"}, &fakeObserver{})
	resp, _ := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "high-cpu"})
	ok, isType := resp.(gen.GetAlertRule200JSONResponse)
	if !isType {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.Metadata == nil || ok.Metadata.Name == nil || *ok.Metadata.Name != "high-cpu" {
		t.Errorf("expected rule name echoed back")
	}
}

func TestDeleteAlertRule_OK(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{}, &fakeObserver{})
	resp, _ := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "high-cpu"})
	if _, ok := resp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestDeleteAlertRule_NotFound(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{findErr: azuremonitor.ErrNotFound}, &fakeObserver{})
	resp, _ := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "nope"})
	if _, ok := resp.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", resp)
	}
}

func TestHandleAlertWebhook_ForwardsToObserver(t *testing.T) {
	obs := &fakeObserver{}
	h := newAlertingHandler(&fakeAlertClient{}, obs)
	// Minimal Common Alert Schema body with the custom properties the parser reads.
	body := gen.HandleAlertWebhookJSONRequestBody(map[string]interface{}{
		"schemaId": "azureMonitorCommonAlertSchema",
		"data": map[string]interface{}{
			"essentials": map[string]interface{}{
				"alertRule":        "oc-abc",
				"firedDateTime":    "2026-06-05T00:00:00Z",
				"monitorCondition": "Fired",
			},
			"alertContext": map[string]interface{}{
				"condition": map[string]interface{}{
					"allOf": []interface{}{
						map[string]interface{}{"metricValue": 0.95, "searchQuery": "Perf"},
					},
				},
			},
			"customProperties": map[string]interface{}{
				"openchoreo-namespace": "dp-acme-dev",
				"openchoreo-rule-name": "high-cpu",
			},
		},
	})
	resp, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if !obs.called {
		t.Error("expected observer ForwardAlert to be called")
	}
}

func TestHandleAlertWebhook_ObserverError(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{}, &fakeObserver{err: errors.New("down")})
	body := gen.HandleAlertWebhookJSONRequestBody(map[string]interface{}{
		"schemaId": "azureMonitorCommonAlertSchema",
		"data": map[string]interface{}{
			"essentials":   map[string]interface{}{"alertRule": "oc-abc", "firedDateTime": "2026-06-05T00:00:00Z"},
			"alertContext": map[string]interface{}{"condition": map[string]interface{}{"allOf": []interface{}{map[string]interface{}{"metricValue": 1.0}}}},
			"customProperties": map[string]interface{}{
				"openchoreo-namespace": "ns", "openchoreo-rule-name": "r",
			},
		},
	})
	resp, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if _, ok := resp.(gen.HandleAlertWebhook500JSONResponse); !ok {
		t.Fatalf("expected 500 on observer error, got %T", resp)
	}
}
