// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/azuremonitor"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

// scopeCases are the component scopes, with what the KQL must carry for each
// and the header the response must set.
var scopeCases = map[string]struct {
	component  *string
	line       *string
	allLines   *bool
	wantInKQL  string
	wantHeader string
}{
	"main line":                {component: strPtr(evComponentUID), wantInKQL: "isempty(", wantHeader: "true"},
	"every line":               {component: strPtr(evComponentUID), allLines: boolPtr(true), wantInKQL: "release-line-component-uid", wantHeader: "true"},
	"one line":                 {component: strPtr(evComponentUID), line: strPtr(testReleaseLineUID), wantInKQL: testReleaseLineUID, wantHeader: "true"},
	"no component, every line": {allLines: boolPtr(true), wantHeader: ""},
}

func boolPtr(b bool) *bool { return &b }

func checkScope(t *testing.T, kql, wantInKQL, header, wantHeader string) {
	t.Helper()
	if wantInKQL == "" {
		if strings.Contains(kql, "release-line-uid\"])") || strings.Contains(kql, "component-uid\"]) ==") {
			t.Errorf("KQL scopes by component or release line without either:\n%s", kql)
		}
	} else if !strings.Contains(kql, wantInKQL) {
		t.Errorf("the scope did not reach the KQL, want %q in:\n%s", wantInKQL, kql)
	}
	if header != wantHeader {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", header, wantHeader)
	}
}

func TestQueryLogs_ReleaseLineScope(t *testing.T) {
	for name, tc := range scopeCases {
		t.Run(name, func(t *testing.T) {
			api := &stubQueryAPI{}
			start, end := window()
			var scope gen.LogsQueryRequest_SearchScope
			if err := scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: tc.component, ReleaseLineUid: tc.line, AllReleaseLines: tc.allLines,
			}); err != nil {
				t.Fatalf("building scope: %v", err)
			}
			resp, err := platformHandler(api).QueryLogs(context.Background(), gen.QueryLogsRequestObject{
				Body: &gen.LogsQueryRequest{StartTime: start, EndTime: end, SearchScope: scope},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.QueryLogs200JSONResponse)
			if !is {
				t.Fatalf("got %T, want 200", resp)
			}
			kql := api.lastKQL[:strings.Index(api.lastKQL, "| extend _labels")]
			checkScope(t, kql, tc.wantInKQL, ok.Headers.OpenChoreoReleaseLineApplied, tc.wantHeader)
		})
	}
}

// Scope UIDs are trimmed as the events endpoint trims them, so padding neither
// reaches the KQL nor reads as a scope.
func TestQueryLogs_TrimsScopeUIDs(t *testing.T) {
	for name, tc := range map[string]struct {
		line       string
		wantInKQL  string
		wantHeader string
	}{
		"padded line":      {line: " " + testReleaseLineUID + " ", wantInKQL: `== "` + testReleaseLineUID + `"`, wantHeader: "true"},
		"blank line, none": {line: "  ", wantHeader: ""},
	} {
		t.Run(name, func(t *testing.T) {
			api := &stubQueryAPI{}
			start, end := window()
			var scope gen.LogsQueryRequest_SearchScope
			if err := scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: strPtr("  "), ReleaseLineUid: strPtr(tc.line),
			}); err != nil {
				t.Fatalf("building scope: %v", err)
			}
			resp, err := platformHandler(api).QueryLogs(context.Background(), gen.QueryLogsRequestObject{
				Body: &gen.LogsQueryRequest{StartTime: start, EndTime: end, SearchScope: scope},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.QueryLogs200JSONResponse)
			if !is {
				t.Fatalf("got %T, want 200", resp)
			}
			kql := api.lastKQL[:strings.Index(api.lastKQL, "| extend _labels")]
			checkScope(t, kql, tc.wantInKQL, ok.Headers.OpenChoreoReleaseLineApplied, tc.wantHeader)
		})
	}
}

func TestQueryLogs_WorkflowScopeNeverSetsReleaseLineHeader(t *testing.T) {
	start, end := window()
	var scope gen.LogsQueryRequest_SearchScope
	if err := scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace: "default", WorkflowRunName: strPtr("run-1"),
	}); err != nil {
		t.Fatalf("building scope: %v", err)
	}
	resp, err := platformHandler(&stubQueryAPI{}).QueryLogs(context.Background(), gen.QueryLogsRequestObject{
		Body: &gen.LogsQueryRequest{StartTime: start, EndTime: end, SearchScope: scope},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.QueryLogs200JSONResponse)
	if !is {
		t.Fatalf("got %T, want 200", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q on a workflow query, want empty", got)
	}
}

func TestQueryEvents_ReleaseLineScope(t *testing.T) {
	for name, tc := range scopeCases {
		t.Run(name, func(t *testing.T) {
			api := &stubQueryAPI{tables: eventTables(0)}
			body := eventsBody()
			body.SearchScope = componentScope(t, gen.ComponentSearchScope{
				Namespace: "acme", ComponentUid: tc.component, ReleaseLineUid: tc.line, AllReleaseLines: tc.allLines,
			})
			r := expectEvents200(t, queryEvents(t, api, body))
			base := api.lastKQL[:strings.Index(api.lastKQL, ";\n")]
			checkScope(t, base, tc.wantInKQL, r.Headers.OpenChoreoReleaseLineApplied, tc.wantHeader)
		})
	}
}

func TestQueryEvents_UnscopedNeverSetsReleaseLineHeader(t *testing.T) {
	body := eventsBody()
	body.Reasons = &[]string{"BackOff"}
	r := expectEvents200(t, queryEvents(t, &stubQueryAPI{tables: eventTables(0)}, body))
	if got := r.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q on an unscoped sweep, want empty", got)
	}
}

// A release line's events are attributed to their component.
func TestQueryEvents_ReleaseLineEventReportsItsComponent(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(1, evRow("2026-09-01T10:00:00Z", "BackOff"))}
	body := eventsBody()
	body.SearchScope = componentScope(t, gen.ComponentSearchScope{
		Namespace: "acme", ComponentUid: strPtr(evComponentUID), ReleaseLineUid: strPtr(testReleaseLineUID),
	})
	r := expectEvents200(t, queryEvents(t, api, body))
	if !strings.Contains(api.lastKQL, `ComponentUID    = coalesce(`) {
		t.Errorf("the component UID is not read from the release line label as well:\n%s", api.lastKQL)
	}
	if m := (*r.Body.Events)[0].Metadata; m == nil || m.ComponentUid == nil || m.ComponentUid.String() != evComponentUID {
		t.Errorf("component UID = %v, want %s", m, evComponentUID)
	}
}

// armRecorder answers every ARM call with an empty scheduled query rule and
// keeps the last request body.
type armRecorder struct{ lastBody string }

func (a *armRecorder) Do(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		a.lastBody = string(b)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"/rules/oc-x","properties":{}}`)),
		Request:    req,
	}, nil
}

type staticCred struct{}

func (staticCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "t", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func alertHandler(t *testing.T) (*LogsHandler, *armRecorder) {
	t.Helper()
	rec := &armRecorder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := azuremonitor.NewClientWithOptions(staticCred{}, azuremonitor.Config{
		SubscriptionID:      "sub",
		ResourceGroup:       "rg",
		Region:              "eastus2",
		WorkspaceResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws",
		ActionGroupID:       "/subscriptions/sub/resourceGroups/rg/providers/microsoft.insights/actionGroups/ag",
	}, &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: rec,
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}, logger)
	if err != nil {
		t.Fatalf("building alert client: %v", err)
	}
	return NewLogsHandler(nil, client, nil, logger), rec
}

func alertRuleRequest(t *testing.T, line *string) *gen.AlertRuleRequest {
	t.Helper()
	req := &gen.AlertRuleRequest{}
	req.Metadata.Name = "high-error-rate"
	req.Metadata.Namespace = "default"
	req.Metadata.ComponentUid, _ = parseUUID(evComponentUID)
	if line != nil {
		uid, ok := parseUUID(*line)
		if !ok {
			t.Fatalf("bad UUID %q", *line)
		}
		req.Metadata.ReleaseLineUid = &uid
	}
	req.Source.Query = "connection refused"
	req.Condition.Operator = "gt"
	req.Condition.Threshold = 5
	req.Condition.Interval = "5m"
	req.Condition.Window = "5m"
	req.Condition.Enabled = true
	return req
}

// lineCases maps a case name to the release line UID a rule is scoped to.
var lineCases = map[string]*string{"one line": strPtr(testReleaseLineUID), "main line": nil}

func wantAlertScope(t *testing.T, line *string, armBody, header string) {
	t.Helper()
	want := `release-line-uid\"]) == \"` + testReleaseLineUID + `\"`
	if line == nil {
		want = `isempty(tostring(parse_json(tostring(KubernetesMetadata.podLabels))[\"openchoreo.dev/release-line-uid\"]))`
	}
	if !strings.Contains(armBody, want) {
		t.Errorf("rule sent to ARM missing %s:\n%s", want, armBody)
	}
	if header != "true" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", header)
	}
}

func TestCreateAlertRule_ReleaseLineScope(t *testing.T) {
	for name, line := range lineCases {
		t.Run(name, func(t *testing.T) {
			h, rec := alertHandler(t)
			resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: alertRuleRequest(t, line)})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.CreateAlertRule201JSONResponse)
			if !is {
				t.Fatalf("got %T, want 201", resp)
			}
			wantAlertScope(t, line, rec.lastBody, ok.Headers.OpenChoreoReleaseLineApplied)
		})
	}
}

func TestUpdateAlertRule_ReleaseLineScope(t *testing.T) {
	for name, line := range lineCases {
		t.Run(name, func(t *testing.T) {
			h, rec := alertHandler(t)
			resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
				RuleName: "high-error-rate", Body: alertRuleRequest(t, line),
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.UpdateAlertRule200JSONResponse)
			if !is {
				t.Fatalf("got %T, want 200", resp)
			}
			wantAlertScope(t, line, rec.lastBody, ok.Headers.OpenChoreoReleaseLineApplied)
		})
	}
}

// A full KQL statement is sent as is, so a release line rule can't use one.
func TestAlertRule_ReleaseLineRejectsFullKQLStatement(t *testing.T) {
	const fullKQL = `ContainerLogV2 | where LogLevel == "ERROR"`
	wantRejected := func(t *testing.T, got gen.ErrorResponse, rec *armRecorder) {
		t.Helper()
		if got.Message == nil || !strings.Contains(*got.Message, "full KQL query") {
			t.Errorf("400 message = %v, want it to name the full KQL query", got.Message)
		}
		if rec.lastBody != "" {
			t.Errorf("the rule reached ARM:\n%s", rec.lastBody)
		}
	}

	t.Run("create", func(t *testing.T) {
		h, rec := alertHandler(t)
		req := alertRuleRequest(t, strPtr(testReleaseLineUID))
		req.Source.Query = fullKQL
		resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, is := resp.(gen.CreateAlertRule400JSONResponse)
		if !is {
			t.Fatalf("got %T, want 400", resp)
		}
		wantRejected(t, gen.ErrorResponse(got), rec)
	})
	t.Run("update", func(t *testing.T) {
		h, rec := alertHandler(t)
		req := alertRuleRequest(t, strPtr(testReleaseLineUID))
		req.Source.Query = fullKQL
		resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
			RuleName: "high-error-rate", Body: req,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, is := resp.(gen.UpdateAlertRule400JSONResponse)
		if !is {
			t.Fatalf("got %T, want 400", resp)
		}
		wantRejected(t, gen.ErrorResponse(got), rec)
	})
}

// A main line rule may still use a full KQL statement, which is not scoped.
func TestCreateAlertRule_MainLineFullKQLStatementLeavesHeaderUnset(t *testing.T) {
	h, rec := alertHandler(t)
	req := alertRuleRequest(t, nil)
	req.Source.Query = `ContainerLogV2 | where LogLevel == "ERROR"`
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.CreateAlertRule201JSONResponse)
	if !is {
		t.Fatalf("got %T, want 201", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
	if rec.lastBody == "" {
		t.Error("the rule never reached ARM")
	}
}
