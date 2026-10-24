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

// lineCases maps a case name to the release line UID set on the scope.
var lineCases = map[string]*string{"with a line": strPtr(testReleaseLineUID), "without a line": nil}

// Label expressions the logs and events KQL read the release line through.
const (
	podLabelLine   = `tostring(parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/release-line-uid"])`
	eventLabelLine = `tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/release-line-uid"])`
)

func wantLineHeader(t *testing.T, line *string, kql, labelExpr, got string) {
	t.Helper()
	want := ""
	if line != nil {
		want = "true"
		if !strings.Contains(kql, labelExpr+` == "`+*line+`"`) {
			t.Errorf("the release line did not reach the KQL:\n%s", kql)
		}
	} else if strings.Contains(kql, "release-line-uid") {
		t.Errorf("KQL filters on a release line without one:\n%s", kql)
	}
	if got != want {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
	}
}

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases {
		t.Run(name, func(t *testing.T) {
			api := &stubQueryAPI{}
			start, end := window()
			var scope gen.LogsQueryRequest_SearchScope
			if err := scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: strPtr(evComponentUID), ReleaseLineUid: line,
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
			wantLineHeader(t, line, api.lastKQL, podLabelLine, ok.Headers.OpenChoreoReleaseLineApplied)
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

func TestQueryEvents_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases {
		t.Run(name, func(t *testing.T) {
			api := &stubQueryAPI{tables: eventTables(0)}
			body := eventsBody()
			body.SearchScope = componentScope(t, gen.ComponentSearchScope{
				Namespace: "acme", ComponentUid: strPtr(evComponentUID), ReleaseLineUid: line,
			})
			r := expectEvents200(t, queryEvents(t, api, body))
			wantLineHeader(t, line, api.lastKQL, eventLabelLine, r.Headers.OpenChoreoReleaseLineApplied)
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

func TestCreateAlertRule_ReleaseLineHeader(t *testing.T) {
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
			wantAlertLine(t, line, rec.lastBody, ok.Headers.OpenChoreoReleaseLineApplied)
		})
	}
}

func TestUpdateAlertRule_ReleaseLineHeader(t *testing.T) {
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
			wantAlertLine(t, line, rec.lastBody, ok.Headers.OpenChoreoReleaseLineApplied)
		})
	}
}

// A full KQL statement is sent as is, so the rule is not scoped to the line.
func TestCreateAlertRule_FullKQLStatementLeavesHeaderUnset(t *testing.T) {
	h, _ := alertHandler(t)
	req := alertRuleRequest(t, strPtr(testReleaseLineUID))
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
}

func wantAlertLine(t *testing.T, line *string, armBody, header string) {
	t.Helper()
	want := ""
	if line != nil {
		want = "true"
		for _, s := range []string{
			`openchoreo.dev/release-line-uid`,
			`"openchoreo-release-line-uid":"` + *line + `"`,
		} {
			if !strings.Contains(armBody, s) {
				t.Errorf("rule sent to ARM missing %q:\n%s", s, armBody)
			}
		}
	} else if strings.Contains(armBody, "release-line-uid") {
		t.Errorf("rule sent to ARM carries a release line without one:\n%s", armBody)
	}
	if header != want {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", header, want)
	}
}
