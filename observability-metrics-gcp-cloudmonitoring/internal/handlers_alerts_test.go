// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/cloudmonitoring"
)

type fakeAlertClient struct {
	createErr error
	updateErr error
	findErr   error
	deleteErr error
	findNS    string
	lastInput cloudmonitoring.RuleInput
	backendID string
}

func (f *fakeAlertClient) CreateRule(_ context.Context, in cloudmonitoring.RuleInput) (*cloudmonitoring.RuleResult, error) {
	f.lastInput = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &cloudmonitoring.RuleResult{BackendID: f.backendID, LogicalID: in.RuleName, LastSynced: "2026-07-08T00:00:00Z"}, nil
}

func (f *fakeAlertClient) UpdateRule(_ context.Context, in cloudmonitoring.RuleInput) (*cloudmonitoring.RuleResult, error) {
	f.lastInput = in
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &cloudmonitoring.RuleResult{BackendID: f.backendID, LogicalID: in.RuleName, LastSynced: "2026-07-08T00:00:00Z"}, nil
}

func (f *fakeAlertClient) FindRuleByName(_ context.Context, ruleName string) (*cloudmonitoring.RuleResult, string, error) {
	if f.findErr != nil {
		return nil, "", f.findErr
	}
	return &cloudmonitoring.RuleResult{BackendID: f.backendID, LogicalID: ruleName}, f.findNS, nil
}

func (f *fakeAlertClient) DeleteRule(_ context.Context, ruleName string) (*cloudmonitoring.RuleResult, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &cloudmonitoring.RuleResult{BackendID: f.backendID, LogicalID: ruleName, LastSynced: "2026-07-08T00:00:00Z"}, nil
}

type fakeObserver struct {
	mu    sync.Mutex
	calls int
	err   error
	last  struct {
		ruleName, ns string
		value        float64
	}
}

func (f *fakeObserver) ForwardAlert(_ context.Context, ruleName, ns string, value float64, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last.ruleName = ruleName
	f.last.ns = ns
	f.last.value = value
	return f.err
}

func (f *fakeObserver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newAlertingHandler(ac alertClient, obs observerForwarder) *MetricsHandler {
	return NewMetricsHandlerWithAlerting(&fakeMetricsClient{}, ac, obs, slog.New(slog.DiscardHandler))
}

func validAlertRequestBody() *gen.AlertRuleRequest {
	b := &gen.AlertRuleRequest{}
	b.Metadata.Name = "high-cpu"
	b.Metadata.Namespace = "default"
	b.Metadata.ComponentUid = uuid.MustParse("f3b8e2a4-6c1d-4e9f-9a2b-3d5c7e8f0a1c")
	b.Metadata.ProjectUid = uuid.MustParse("9d4c2b1a-8e7f-4a3b-b6c5-2f1e0d9c8b7a")
	b.Metadata.EnvironmentUid = uuid.MustParse("5e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f7a8b9")
	b.Source.Metric = "cpu_usage"
	b.Condition.Operator = "gt"
	b.Condition.Threshold = 0.8
	b.Condition.Enabled = true
	return b
}

func TestCreateAlertRuleSuccess(t *testing.T) {
	ac := &fakeAlertClient{backendID: "projects/p/alertPolicies/1"}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertRequestBody()})
	r, ok := resp.(gen.CreateAlertRule201JSONResponse)
	if !ok {
		t.Fatalf("resp = %T, want 201", resp)
	}
	if r.Action == nil || *r.Action != gen.Created {
		t.Errorf("action = %v, want created", r.Action)
	}
	if r.Status == nil || *r.Status != gen.Synced {
		t.Errorf("status = %v, want synced", r.Status)
	}
	if r.RuleBackendId == nil || *r.RuleBackendId != "projects/p/alertPolicies/1" {
		t.Errorf("backend ID = %v", r.RuleBackendId)
	}
}

func TestCreateAlertRuleConflict(t *testing.T) {
	ac := &fakeAlertClient{createErr: cloudmonitoring.ErrRuleAlreadyExists}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertRequestBody()})
	if _, ok := resp.(gen.CreateAlertRule409JSONResponse); !ok {
		t.Errorf("resp = %T, want 409", resp)
	}
}

func TestCreateAlertRuleValidation400(t *testing.T) {
	ac := &fakeAlertClient{createErr: cloudmonitoring.ErrValidation}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertRequestBody()})
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Errorf("resp = %T, want 400", resp)
	}
}

func TestCreateAlertRuleNilBodyAndMissingFields(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{}, &fakeObserver{})
	if resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{}); func() bool {
		_, ok := resp.(gen.CreateAlertRule400JSONResponse)
		return !ok
	}() {
		t.Errorf("nil body should be 400, got %T", nil)
	}
	// Missing metric.
	b := validAlertRequestBody()
	b.Source.Metric = ""
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: b})
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Errorf("missing metric should be 400, got %T", resp)
	}
}

func TestCreateAlertRuleDisabled500(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{}) // no alerting
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertRequestBody()})
	if _, ok := resp.(gen.CreateAlertRule500JSONResponse); !ok {
		t.Errorf("resp = %T, want 500 not-implemented", resp)
	}
}

func TestGetAlertRuleNotFound404(t *testing.T) {
	ac := &fakeAlertClient{findErr: cloudmonitoring.ErrRuleNotFound}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "x"})
	if _, ok := resp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Errorf("resp = %T, want 404", resp)
	}
}

func TestGetAlertRuleSuccess(t *testing.T) {
	ac := &fakeAlertClient{findNS: "default"}
	h := newAlertingHandler(ac, &fakeObserver{})
	resp, _ := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "high-cpu"})
	r, ok := resp.(gen.GetAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("resp = %T, want 200", resp)
	}
	if r.Metadata == nil || r.Metadata.Name == nil || *r.Metadata.Name != "high-cpu" {
		t.Errorf("metadata name missing")
	}
	if r.Metadata.Namespace == nil || *r.Metadata.Namespace != "default" {
		t.Errorf("namespace = %v", r.Metadata.Namespace)
	}
}

func TestUpdateAlertRulePathNameWins(t *testing.T) {
	ac := &fakeAlertClient{backendID: "id"}
	h := newAlertingHandler(ac, &fakeObserver{})
	body := gen.UpdateAlertRuleJSONRequestBody(*validAlertRequestBody())
	resp, _ := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{RuleName: "path-name", Body: &body})
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("resp = %T, want 200", resp)
	}
	if ac.lastInput.RuleName != "path-name" {
		t.Errorf("path rule name should override body: got %q", ac.lastInput.RuleName)
	}
}

func TestDeleteAlertRuleSuccessAndNotFound(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{backendID: "id"}, &fakeObserver{})
	resp, _ := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "r"})
	if _, ok := resp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Errorf("resp = %T, want 200", resp)
	}

	h2 := newAlertingHandler(&fakeAlertClient{deleteErr: cloudmonitoring.ErrRuleNotFound}, &fakeObserver{})
	resp2, _ := h2.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "r"})
	if _, ok := resp2.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Errorf("resp = %T, want 404", resp2)
	}
}

func TestHandleWebhookForwardsFiring(t *testing.T) {
	obs := &fakeObserver{}
	h := newAlertingHandler(&fakeAlertClient{}, obs)
	body := gen.HandleAlertWebhookJSONRequestBody(map[string]interface{}{
		"incident": map[string]interface{}{
			"state":          "open",
			"observed_value": "0.9",
			"policy_user_labels": map[string]interface{}{
				"openchoreo_namespace": "default",
				"openchoreo_rule_name": "high-cpu",
			},
		},
	})
	resp, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("resp = %T, want 200", resp)
	}
	if obs.callCount() != 1 {
		t.Fatalf("forward calls = %d, want 1", obs.callCount())
	}
	if obs.last.ruleName != "high-cpu" || obs.last.ns != "default" || obs.last.value != 0.9 {
		t.Errorf("forwarded = %+v", obs.last)
	}
}

func TestHandleWebhookForwardFailureIs500(t *testing.T) {
	obs := &fakeObserver{err: errors.New("observer down")}
	h := newAlertingHandler(&fakeAlertClient{}, obs)
	body := gen.HandleAlertWebhookJSONRequestBody(map[string]interface{}{
		"incident": map[string]interface{}{
			"state":          "open",
			"observed_value": "0.9",
			"policy_user_labels": map[string]interface{}{
				"openchoreo_namespace": "default",
				"openchoreo_rule_name": "high-cpu",
			},
		},
	})
	resp, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if _, ok := resp.(gen.HandleAlertWebhook500JSONResponse); !ok {
		t.Fatalf("resp = %T, want 500 so Cloud Monitoring redelivers", resp)
	}
}

func TestHandleWebhookSkipsResolved(t *testing.T) {
	obs := &fakeObserver{}
	h := newAlertingHandler(&fakeAlertClient{}, obs)
	body := gen.HandleAlertWebhookJSONRequestBody(map[string]interface{}{
		"incident": map[string]interface{}{
			"state": "closed",
			"policy_user_labels": map[string]interface{}{
				"openchoreo_namespace": "default",
				"openchoreo_rule_name": "high-cpu",
			},
		},
	})
	resp, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("resp = %T, want 200 ack", resp)
	}
	if obs.callCount() != 0 {
		t.Errorf("resolved incident should not forward, got %d calls", obs.callCount())
	}
}

func TestHandleWebhookRejectsUnparseable(t *testing.T) {
	obs := &fakeObserver{}
	h := newAlertingHandler(&fakeAlertClient{}, obs)
	body := gen.HandleAlertWebhookJSONRequestBody(map[string]interface{}{"garbage": true})
	resp, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if _, ok := resp.(gen.HandleAlertWebhook400JSONResponse); !ok {
		t.Errorf("resp = %T, want 400 on unparseable payload", resp)
	}
	if obs.callCount() != 0 {
		t.Errorf("unparseable payload should not forward, got %d calls", obs.callCount())
	}
}
