// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	osearch "github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

const sharedRuleName = "api-development-1a2b3c4d-error-logs"

// fakeAlerting is an in-memory stand-in for the OpenSearch Alerting monitor API. Like a phrase
// query, its search returns every monitor whose name contains the searched name.
type fakeAlerting struct {
	mu       sync.Mutex
	nextID   int
	monitors map[string]map[string]interface{}
}

func newFakeAlerting(t *testing.T) (*fakeAlerting, *httptest.Server) {
	t.Helper()
	f := &fakeAlerting{monitors: map[string]map[string]interface{}{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeAlerting) add(monitor map[string]interface{}) string {
	f.nextID++
	id := fmt.Sprintf("monitor-%d", f.nextID)
	f.monitors[id] = monitor
	return id
}

func (f *fakeAlerting) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, m := range f.monitors {
		names = append(names, m["name"].(string))
	}
	return names
}

func (f *fakeAlerting) monitorNamed(name string) map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.monitors {
		if m["name"] == name {
			return m
		}
	}
	return nil
}

func (f *fakeAlerting) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	const base = "/_plugins/_alerting/monitors"
	id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")

	switch {
	case r.URL.Path == base+"/_search":
		var q struct {
			Query struct {
				Bool struct {
					Should []map[string]map[string]string `json:"should"`
				} `json:"bool"`
			} `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		phrase := q.Query.Bool.Should[1]["match_phrase"]["monitor.name"]
		var hits []map[string]interface{}
		for mid, m := range f.monitors {
			if strings.Contains(m["name"].(string), phrase) {
				hits = append(hits, map[string]interface{}{"_id": mid, "_source": map[string]interface{}{"name": m["name"]}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"hits": map[string]interface{}{"total": map[string]interface{}{"value": len(hits)}, "hits": hits},
		})
	case r.URL.Path == base && r.Method == http.MethodPost:
		var m map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&m)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"_id": f.add(m), "monitor": map[string]interface{}{}})
	case f.monitors[id] == nil:
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"_id": id, "monitor": f.monitors[id]})
	case r.Method == http.MethodPut:
		var m map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.monitors[id] = m
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"_id": id, "monitor": map[string]interface{}{}})
	case r.Method == http.MethodDelete:
		delete(f.monitors, id)
	}
}

func newNamespaceTestHandler(t *testing.T) (*LogsHandler, *fakeAlerting) {
	t.Helper()
	f, server := newFakeAlerting(t)
	return NewLogsHandler(newTestOSClient(t, server.URL), osearch.NewQueryBuilder("logs-"), nil, nil, nil, testLogger()), f
}

func ruleRequest(namespace, query string) gen.AlertRuleRequest {
	req := gen.AlertRuleRequest{}
	req.Metadata.Name = sharedRuleName
	req.Metadata.Namespace = namespace
	req.Metadata.ComponentUid = openapi_types.UUID(uuid.MustParse("a7f3c9e2-4b1d-4a8e-9c3f-2d5e7b8a1c4f"))
	req.Metadata.ProjectUid = openapi_types.UUID(uuid.MustParse("b8e4d0f3-5c2e-4b9f-8d4a-3e6f8c9b2d5e"))
	req.Metadata.EnvironmentUid = openapi_types.UUID(uuid.MustParse("c9f5e1a4-6d3f-4c0a-9e5b-4f7a9d0c3e6f"))
	req.Source.Query = query
	req.Condition.Enabled = true
	req.Condition.Window = "1h"
	req.Condition.Interval = "5m"
	req.Condition.Operator = gen.AlertRuleRequestConditionOperatorGt
	req.Condition.Threshold = 10
	return req
}

// addLegacyMonitor adds a monitor the way earlier versions did: named after the rule alone.
func addLegacyMonitor(t *testing.T, f *fakeAlerting, namespace string) {
	t.Helper()
	req := ruleRequest(namespace, "legacy")
	body, err := osearch.NewQueryBuilder("logs-").BuildLogAlertingRuleMonitorBody(toAlertingRuleRequest(&req))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	body["name"] = sharedRuleName
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(body)
}

func monitorQuery(m map[string]interface{}) string {
	b, _ := json.Marshal(m["inputs"])
	return string(b)
}

func mustCreateRule(t *testing.T, h *LogsHandler, req gen.AlertRuleRequest) {
	t.Helper()
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &req})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule201JSONResponse); !ok {
		t.Fatalf("expected 201, got %T", resp)
	}
}

func getRule(t *testing.T, h *LogsHandler, namespace string) gen.GetAlertRuleResponseObject {
	t.Helper()
	resp, err := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.GetAlertRuleParams{Namespace: &namespace},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return resp
}

func TestAlertRules_SameNameInTwoNamespacesAreIndependent(t *testing.T) {
	ctx := context.Background()
	h, f := newNamespaceTestHandler(t)

	mustCreateRule(t, h, ruleRequest("tenant-a", "timeout"))
	mustCreateRule(t, h, ruleRequest("tenant-b", "panic"))
	queryA := monitorQuery(f.monitorNamed("tenant-a/" + sharedRuleName))

	nsB := "tenant-b"
	reqB := ruleRequest("tenant-b", "oom")
	resp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsB}, Body: &reqB,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if got := monitorQuery(f.monitorNamed("tenant-a/" + sharedRuleName)); got != queryA {
		t.Fatalf("updating tenant-b changed tenant-a's monitor")
	}

	got, ok := getRule(t, h, "tenant-a").(gen.GetAlertRule200JSONResponse)
	if !ok || got.Metadata == nil || *got.Metadata.Namespace != "tenant-a" || *got.Metadata.Name != sharedRuleName {
		t.Fatalf("expected tenant-a's rule, got %#v", got)
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
	if names := f.names(); len(names) != 1 || names[0] != "tenant-a/"+sharedRuleName {
		t.Fatalf("expected only tenant-a's monitor to remain, got %v", names)
	}
	if _, ok := getRule(t, h, "tenant-b").(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 for tenant-b")
	}
}

func TestCreateAlertRule_ConflictsWithExistingRule(t *testing.T) {
	h, _ := newNamespaceTestHandler(t)
	mustCreateRule(t, h, ruleRequest("tenant-a", "timeout"))

	req := ruleRequest("tenant-a", "timeout")
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &req})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule409JSONResponse); !ok {
		t.Fatalf("expected 409, got %T", resp)
	}
}

// TestGetAlertRule_IgnoresMonitorsWhoseNameOnlyContainsTheRuleName covers the phrase search, which
// also matches longer names.
func TestGetAlertRule_IgnoresMonitorsWhoseNameOnlyContainsTheRuleName(t *testing.T) {
	h, f := newNamespaceTestHandler(t)
	req := ruleRequest("tenant-a", "timeout")
	req.Metadata.Name = sharedRuleName + "-2"
	mustCreateRule(t, h, req)

	if _, ok := getRule(t, h, "tenant-a").(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, monitors: %v", f.names())
	}
}

func TestAlertRules_LegacyMonitorIsMigratedByItsOwner(t *testing.T) {
	ctx := context.Background()
	h, f := newNamespaceTestHandler(t)
	addLegacyMonitor(t, f, "tenant-a")

	// Another namespace does not see the legacy monitor and can create its own.
	if _, ok := getRule(t, h, "tenant-b").(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 for tenant-b")
	}
	mustCreateRule(t, h, ruleRequest("tenant-b", "panic"))

	// The owner finds the legacy monitor, and its next update migrates it.
	got, ok := getRule(t, h, "tenant-a").(gen.GetAlertRule200JSONResponse)
	if !ok || *got.Metadata.Namespace != "tenant-a" {
		t.Fatalf("expected tenant-a's legacy rule, got %#v", got)
	}

	nsA := "tenant-a"
	reqA := ruleRequest("tenant-a", "timeout")
	resp, err := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsA}, Body: &reqA,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if f.monitorNamed(sharedRuleName) != nil {
		t.Fatalf("legacy monitor was not removed")
	}
	if f.monitorNamed("tenant-a/"+sharedRuleName) == nil || f.monitorNamed("tenant-b/"+sharedRuleName) == nil {
		t.Fatalf("expected both namespaced monitors, got %v", f.names())
	}
}

func TestDeleteAlertRule_RemovesCurrentAndOwnedLegacyMonitor(t *testing.T) {
	h, f := newNamespaceTestHandler(t)
	// Both exist when a migration was interrupted before the legacy monitor was deleted.
	mustCreateRule(t, h, ruleRequest("tenant-a", "timeout"))
	addLegacyMonitor(t, f, "tenant-a")
	nsA := "tenant-a"

	resp, err := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsA},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if names := f.names(); len(names) != 0 {
		t.Fatalf("expected no monitors left, got %v", names)
	}
}

func TestDeleteAlertRule_KeepsLegacyMonitorOfAnotherNamespace(t *testing.T) {
	h, f := newNamespaceTestHandler(t)
	addLegacyMonitor(t, f, "tenant-a")
	nsB := "tenant-b"

	resp, err := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.DeleteAlertRuleParams{Namespace: &nsB},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404, got %T", resp)
	}
	if f.monitorNamed(sharedRuleName) == nil {
		t.Fatalf("tenant-b deleted tenant-a's legacy monitor")
	}
}

func TestUpdateAlertRule_NamespaceMismatch(t *testing.T) {
	h, _ := newNamespaceTestHandler(t)
	req := ruleRequest("tenant-a", "timeout")
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

// TestUpdateAlertRule_RemovesLeftoverLegacyMonitor covers a legacy monitor left next to the current
// one, as an interrupted migration or a module downgrade leaves it.
func TestUpdateAlertRule_RemovesLeftoverLegacyMonitor(t *testing.T) {
	h, f := newNamespaceTestHandler(t)
	mustCreateRule(t, h, ruleRequest("tenant-a", "timeout"))
	addLegacyMonitor(t, f, "tenant-a")
	nsA := "tenant-a"
	reqA := ruleRequest("tenant-a", "timeout")

	resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsA}, Body: &reqA,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if names := f.names(); len(names) != 1 || names[0] != "tenant-a/"+sharedRuleName {
		t.Fatalf("expected only the current monitor to remain, got %v", names)
	}
}

func TestUpdateAlertRule_KeepsLegacyMonitorOfAnotherNamespace(t *testing.T) {
	h, f := newNamespaceTestHandler(t)
	mustCreateRule(t, h, ruleRequest("tenant-b", "panic"))
	addLegacyMonitor(t, f, "tenant-a")
	nsB := "tenant-b"
	reqB := ruleRequest("tenant-b", "panic")

	if _, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: sharedRuleName, Params: gen.UpdateAlertRuleParams{Namespace: &nsB}, Body: &reqB,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if f.monitorNamed(sharedRuleName) == nil {
		t.Fatalf("tenant-b removed tenant-a's legacy monitor")
	}
}
