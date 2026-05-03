package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/cloudwatch"
)

type stubLogsClient struct {
	createAlertFn      func(context.Context, cloudwatch.LogAlertParams) (string, error)
	getAlertFn         func(context.Context, string, string) (*cloudwatch.AlertDetail, error)
	updateAlertFn      func(context.Context, string, string, cloudwatch.LogAlertParams) (string, error)
	deleteAlertFn      func(context.Context, string, string) (string, error)
	getAlarmTagsByName func(context.Context, string) (map[string]string, error)
}

func (s *stubLogsClient) Ping(context.Context) error { return nil }
func (s *stubLogsClient) GetComponentLogs(context.Context, cloudwatch.ComponentLogsParams) (*cloudwatch.ComponentLogsResult, error) {
	return nil, errors.New("unexpected GetComponentLogs call")
}
func (s *stubLogsClient) GetWorkflowLogs(context.Context, cloudwatch.WorkflowLogsParams) (*cloudwatch.WorkflowLogsResult, error) {
	return nil, errors.New("unexpected GetWorkflowLogs call")
}
func (s *stubLogsClient) CreateAlert(ctx context.Context, params cloudwatch.LogAlertParams) (string, error) {
	if s.createAlertFn == nil {
		return "", errors.New("unexpected CreateAlert call")
	}
	return s.createAlertFn(ctx, params)
}
func (s *stubLogsClient) GetAlert(ctx context.Context, namespace, name string) (*cloudwatch.AlertDetail, error) {
	if s.getAlertFn == nil {
		return nil, errors.New("unexpected GetAlert call")
	}
	return s.getAlertFn(ctx, namespace, name)
}
func (s *stubLogsClient) UpdateAlert(ctx context.Context, namespace, name string, params cloudwatch.LogAlertParams) (string, error) {
	if s.updateAlertFn == nil {
		return "", errors.New("unexpected UpdateAlert call")
	}
	return s.updateAlertFn(ctx, namespace, name, params)
}
func (s *stubLogsClient) DeleteAlert(ctx context.Context, namespace, name string) (string, error) {
	if s.deleteAlertFn == nil {
		return "", errors.New("unexpected DeleteAlert call")
	}
	return s.deleteAlertFn(ctx, namespace, name)
}
func (s *stubLogsClient) GetAlarmTagsByName(ctx context.Context, name string) (map[string]string, error) {
	if s.getAlarmTagsByName == nil {
		return nil, errors.New("unexpected GetAlarmTagsByName call")
	}
	return s.getAlarmTagsByName(ctx, name)
}

type stubObserver struct {
	forwarded chan forwardCall
	err       error
}

type forwardCall struct {
	ruleName      string
	ruleNamespace string
	alertValue    float64
	alertTime     time.Time
}

func (s *stubObserver) ForwardAlert(_ context.Context, ruleName, ruleNamespace string, alertValue float64, alertTimestamp time.Time) error {
	if s.forwarded != nil {
		s.forwarded <- forwardCall{
			ruleName:      ruleName,
			ruleNamespace: ruleNamespace,
			alertValue:    alertValue,
			alertTime:     alertTimestamp,
		}
	}
	return s.err
}

func newTestHandler(client logsClient, observer observerForwarder) *LogsHandler {
	return NewLogsHandlerWithOptions(client, HandlerOptions{
		ObserverClient: observer,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func alertRuleRequest() *gen.AlertRuleRequest {
	project := openapi_types.UUID(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	environment := openapi_types.UUID(uuid.MustParse("22222222-2222-2222-2222-222222222222"))
	component := openapi_types.UUID(uuid.MustParse("33333333-3333-3333-3333-333333333333"))

	req := &gen.AlertRuleRequest{}
	req.Metadata.Name = "high-error-rate"
	req.Metadata.Namespace = "payments"
	req.Metadata.ProjectUid = project
	req.Metadata.EnvironmentUid = environment
	req.Metadata.ComponentUid = component
	req.Source.Query = "ERROR"
	req.Condition.Enabled = true
	req.Condition.Window = "5m"
	req.Condition.Interval = "1m"
	req.Condition.Operator = gen.AlertRuleRequestConditionOperatorGt
	req.Condition.Threshold = 5
	return req
}

func TestCreateAlertRuleReturnsConflictWhenRuleExists(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatch.AlertDetail, error) {
			return &cloudwatch.AlertDetail{Name: "high-error-rate"}, nil
		},
	}, nil)

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{
		Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule409JSONResponse); !ok {
		t.Fatalf("expected 409 response, got %T", resp)
	}
}

func TestCreateAlertRuleReturnsBadRequestOnValidationError(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatch.AlertDetail, error) {
			return nil, cloudwatch.ErrAlertNotFound
		},
		createAlertFn: func(context.Context, cloudwatch.LogAlertParams) (string, error) {
			return "", errors.New("invalid: operator eq is not supported")
		},
	}, nil)

	req := alertRuleRequest()
	req.Condition.Operator = gen.AlertRuleRequestConditionOperatorEq

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestGetUpdateDeleteAlertRuleResponses(t *testing.T) {
	client := &stubLogsClient{
		getAlertFn: func(_ context.Context, _, name string) (*cloudwatch.AlertDetail, error) {
			switch name {
			case "missing":
				return nil, cloudwatch.ErrAlertNotFound
			default:
				return &cloudwatch.AlertDetail{
					Name:           "high-error-rate",
					Namespace:      "payments",
					ProjectUID:     "11111111-1111-1111-1111-111111111111",
					EnvironmentUID: "22222222-2222-2222-2222-222222222222",
					ComponentUID:   "33333333-3333-3333-3333-333333333333",
					SearchPattern:  "ERROR",
					Operator:       "gt",
					Threshold:      5,
					Window:         5 * time.Minute,
					Interval:       time.Minute,
					Enabled:        true,
					AlarmARN:       "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test",
				}, nil
			}
		},
		updateAlertFn: func(_ context.Context, _, name string, _ cloudwatch.LogAlertParams) (string, error) {
			if name == "missing" {
				return "", cloudwatch.ErrAlertNotFound
			}
			return "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test", nil
		},
		deleteAlertFn: func(_ context.Context, _, name string) (string, error) {
			if name == "missing" {
				return "", cloudwatch.ErrAlertNotFound
			}
			return "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test", nil
		},
	}
	handler := newTestHandler(client, nil)

	getResp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "high-error-rate"})
	if err != nil {
		t.Fatalf("GetAlertRule() error = %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 get response, got %T", getResp)
	}

	getMissing, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "missing"})
	if err != nil {
		t.Fatalf("GetAlertRule(missing) error = %v", err)
	}
	if _, ok := getMissing.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 get response, got %T", getMissing)
	}

	updateResp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "high-error-rate",
		Body:     alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := updateResp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 update response, got %T", updateResp)
	}

	updateMissing, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "missing",
		Body:     alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule(missing) error = %v", err)
	}
	if _, ok := updateMissing.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 update response, got %T", updateMissing)
	}

	deleteResp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "high-error-rate"})
	if err != nil {
		t.Fatalf("DeleteAlertRule() error = %v", err)
	}
	if _, ok := deleteResp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 delete response, got %T", deleteResp)
	}

	deleteMissing, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "missing"})
	if err != nil {
		t.Fatalf("DeleteAlertRule(missing) error = %v", err)
	}
	if _, ok := deleteMissing.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 delete response, got %T", deleteMissing)
	}
}

func TestHandleAlertWebhookForwardsAlarm(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	handler := newTestHandler(&stubLogsClient{}, observer)
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-logs-alert-123",
		"ruleName":       "high-error-rate",
		"ruleNamespace":  "payments",
		"state":          "ALARM",
		"alertValue":     7.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}

	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200 webhook response, got %T", resp)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "high-error-rate" || call.ruleNamespace != "payments" || call.alertValue != 7 {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded alert")
	}
}

func TestHandleAlertWebhookIgnoresRecoveryWhenDisabled(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	handler := NewLogsHandlerWithOptions(&stubLogsClient{}, HandlerOptions{
		ObserverClient:  observer,
		ForwardRecovery: false,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-logs-alert-123",
		"ruleName":       "high-error-rate",
		"ruleNamespace":  "payments",
		"state":          "OK",
		"alertValue":     0.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}

	if _, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		t.Fatalf("unexpected forwarded recovery event: %#v", call)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleAlertWebhookDropsWhenObserverClientIsTypedNil(t *testing.T) {
	var observer *stubObserver
	handler := NewLogsHandlerWithOptions(&stubLogsClient{}, HandlerOptions{
		ObserverClient: observer,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-logs-alert-123",
		"ruleName":       "high-error-rate",
		"ruleNamespace":  "payments",
		"state":          "ALARM",
		"alertValue":     7.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}

	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200 webhook response, got %T", resp)
	}
}

// --- shared helpers -------------------------------------------------------

func makeComponentSearchScopeBody(t *testing.T, scope gen.ComponentSearchScope) gen.LogsQueryRequest_SearchScope {
	t.Helper()
	var s gen.LogsQueryRequest_SearchScope
	if err := s.FromComponentSearchScope(scope); err != nil {
		t.Fatalf("FromComponentSearchScope: %v", err)
	}
	return s
}

func makeWorkflowSearchScopeBody(t *testing.T, scope gen.WorkflowSearchScope) gen.LogsQueryRequest_SearchScope {
	t.Helper()
	var s gen.LogsQueryRequest_SearchScope
	if err := s.FromWorkflowSearchScope(scope); err != nil {
		t.Fatalf("FromWorkflowSearchScope: %v", err)
	}
	return s
}

// --- QueryLogs handler ----------------------------------------------------

type queryStubClient struct {
	stubLogsClient
	componentResult *cloudwatch.ComponentLogsResult
	componentErr    error
	workflowResult  *cloudwatch.WorkflowLogsResult
	workflowErr     error
}

func (c *queryStubClient) GetComponentLogs(_ context.Context, _ cloudwatch.ComponentLogsParams) (*cloudwatch.ComponentLogsResult, error) {
	return c.componentResult, c.componentErr
}

func (c *queryStubClient) GetWorkflowLogs(_ context.Context, _ cloudwatch.WorkflowLogsParams) (*cloudwatch.WorkflowLogsResult, error) {
	return c.workflowResult, c.workflowErr
}

func TestHealthReturnsHealthy(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{}, nil)
	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	body, ok := resp.(gen.Health200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 health response, got %T", resp)
	}
	if body.Status == nil || *body.Status != "healthy" {
		t.Fatalf("unexpected status: %#v", body.Status)
	}
}

func TestQueryLogsRejectsNilBody(t *testing.T) {
	handler := newTestHandler(&queryStubClient{}, nil)
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryLogsRejectsEmptyComponentNamespace(t *testing.T) {
	handler := newTestHandler(&queryStubClient{}, nil)
	scope := makeComponentSearchScopeBody(t, gen.ComponentSearchScope{Namespace: ""})
	body := &gen.LogsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryLogsComponentReturnsResults(t *testing.T) {
	now := time.Now().UTC()
	client := &queryStubClient{
		componentResult: &cloudwatch.ComponentLogsResult{
			Logs: []cloudwatch.ComponentLogsEntry{{
				Timestamp:       now,
				Log:             "hello",
				LogLevel:        "INFO",
				Namespace:       "default",
				PodName:         "pod-1",
				ComponentUID:    "33333333-3333-3333-3333-333333333333",
				ProjectUID:      "11111111-1111-1111-1111-111111111111",
				EnvironmentUID:  "22222222-2222-2222-2222-222222222222",
				ComponentName:   "comp",
				ContainerName:   "container",
				EnvironmentName: "env",
				ProjectName:     "proj",
			}},
			TotalCount: 1,
			Took:       42,
		},
	}
	handler := newTestHandler(client, nil)
	componentUID := "33333333-3333-3333-3333-333333333333"
	projectUID := "11111111-1111-1111-1111-111111111111"
	envUID := "22222222-2222-2222-2222-222222222222"
	limit := 10
	sortOrder := gen.LogsQueryRequestSortOrder("desc")
	searchPhrase := "hello"
	logLevels := []gen.LogsQueryRequestLogLevels{"ERROR", "WARN"}
	scope := makeComponentSearchScopeBody(t, gen.ComponentSearchScope{
		Namespace:      "default",
		ComponentUid:   &componentUID,
		ProjectUid:     &projectUID,
		EnvironmentUid: &envUID,
	})
	body := &gen.LogsQueryRequest{
		StartTime:    now.Add(-time.Hour),
		EndTime:      now,
		SearchScope:  scope,
		Limit:        &limit,
		SortOrder:    &sortOrder,
		SearchPhrase: &searchPhrase,
		LogLevels:    &logLevels,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	ok, isOK := resp.(gen.QueryLogs200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.Total == nil || *ok.Total != 1 {
		t.Fatalf("unexpected total: %#v", ok.Total)
	}
}

func TestQueryLogsComponentReturnsServerErrorOnClientFailure(t *testing.T) {
	handler := newTestHandler(&queryStubClient{componentErr: errors.New("aws boom")}, nil)
	scope := makeComponentSearchScopeBody(t, gen.ComponentSearchScope{Namespace: "default"})
	body := &gen.LogsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if _, ok := resp.(gen.QueryLogs500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestQueryLogsWorkflowReturnsResults(t *testing.T) {
	now := time.Now().UTC()
	client := &queryStubClient{
		workflowResult: &cloudwatch.WorkflowLogsResult{
			Logs: []cloudwatch.WorkflowLogsEntry{{
				Timestamp: now,
				Log:       "running step",
			}},
			TotalCount: 1,
			Took:       12,
		},
	}
	handler := newTestHandler(client, nil)
	runName := "wf-run-123"
	limit := 5
	sortOrder := gen.LogsQueryRequestSortOrder("asc")
	searchPhrase := "step"
	logLevels := []gen.LogsQueryRequestLogLevels{"INFO"}
	scope := makeWorkflowSearchScopeBody(t, gen.WorkflowSearchScope{
		Namespace:       "default",
		WorkflowRunName: &runName,
	})
	body := &gen.LogsQueryRequest{
		StartTime:    now.Add(-time.Hour),
		EndTime:      now,
		SearchScope:  scope,
		Limit:        &limit,
		SortOrder:    &sortOrder,
		SearchPhrase: &searchPhrase,
		LogLevels:    &logLevels,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if _, ok := resp.(gen.QueryLogs200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestQueryLogsWorkflowReturnsServerErrorOnClientFailure(t *testing.T) {
	handler := newTestHandler(&queryStubClient{workflowErr: errors.New("aws boom")}, nil)
	runName := "wf-run-123"
	scope := makeWorkflowSearchScopeBody(t, gen.WorkflowSearchScope{
		Namespace:       "default",
		WorkflowRunName: &runName,
	})
	body := &gen.LogsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if _, ok := resp.(gen.QueryLogs500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestQueryLogsWorkflowRejectsEmptyNamespace(t *testing.T) {
	handler := newTestHandler(&queryStubClient{}, nil)
	runName := "wf-run-123"
	scope := makeWorkflowSearchScopeBody(t, gen.WorkflowSearchScope{
		Namespace:       "",
		WorkflowRunName: &runName,
	})
	body := &gen.LogsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

// --- CreateAlertRule edge cases ------------------------------------------

func TestCreateAlertRuleSucceedsWhenNoConflict(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatch.AlertDetail, error) {
			return nil, cloudwatch.ErrAlertNotFound
		},
		createAlertFn: func(context.Context, cloudwatch.LogAlertParams) (string, error) {
			return "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test", nil
		},
	}, nil)
	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{
		Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule201JSONResponse); !ok {
		t.Fatalf("expected 201, got %T", resp)
	}
}

func TestCreateAlertRuleRejectsNilBody(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{}, nil)
	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestCreateAlertRuleReturnsServerErrorOnUnexpectedFailure(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatch.AlertDetail, error) {
			return nil, cloudwatch.ErrAlertNotFound
		},
		createAlertFn: func(context.Context, cloudwatch.LogAlertParams) (string, error) {
			return "", errors.New("aws is having a bad day")
		},
	}, nil)
	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{
		Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestUpdateAlertRuleRejectsNilBody(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{}, nil)
	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "rule",
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestUpdateAlertRuleReturnsValidationError(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		updateAlertFn: func(context.Context, string, string, cloudwatch.LogAlertParams) (string, error) {
			return "", errors.New("invalid: window must be >= interval")
		},
	}, nil)

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "rule",
		Body:     alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestUpdateAlertRuleReturnsServerErrorOnUnexpectedFailure(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		updateAlertFn: func(context.Context, string, string, cloudwatch.LogAlertParams) (string, error) {
			return "", errors.New("aws boom")
		},
	}, nil)

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "rule",
		Body:     alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestDeleteAlertRuleReturnsServerErrorOnUnexpectedFailure(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		deleteAlertFn: func(context.Context, string, string) (string, error) {
			return "", errors.New("aws boom")
		},
	}, nil)
	resp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "rule"})
	if err != nil {
		t.Fatalf("DeleteAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestGetAlertRuleReturnsServerErrorOnUnexpectedFailure(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatch.AlertDetail, error) {
			return nil, errors.New("aws boom")
		},
	}, nil)
	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "rule"})
	if err != nil {
		t.Fatalf("GetAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.GetAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

// --- HandleAlertWebhook edge cases ----------------------------------------

func TestHandleAlertWebhookReturnsOKOnNilBody(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{}, nil)
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestHandleAlertWebhookForwardsRecoveryWhenEnabled(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	handler := NewLogsHandlerWithOptions(&stubLogsClient{}, HandlerOptions{
		ObserverClient:  observer,
		ForwardRecovery: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-logs-alert-123",
		"ruleName":       "rule-x",
		"ruleNamespace":  "ns-x",
		"state":          "OK",
		"alertValue":     0.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}
	if _, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "rule-x" {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected recovery to be forwarded")
	}
}

func TestHandleAlertWebhookEventBridgePayload(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	handler := NewLogsHandlerWithOptions(&stubLogsClient{}, HandlerOptions{
		ObserverClient: observer,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	names := cloudwatch.BuildAlertResourceNames("payments", "high-error-rate")

	body := gen.HandleAlertWebhookJSONRequestBody{
		"source": "aws.cloudwatch",
		"time":   "2026-04-23T10:00:00Z",
		"detail": map[string]any{
			"alarmName": names.AlarmName,
			"state": map[string]any{
				"value":      "ALARM",
				"reason":     "Threshold Crossed",
				"reasonData": `{"recentDatapoints":[7]}`,
				"timestamp":  "2026-04-23T10:00:05Z",
			},
		},
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "high-error-rate" || call.alertValue != 7 {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected EventBridge payload to be forwarded")
	}
}

func TestHandleAlertWebhookSNSSubscriptionConfirmIgnoredByDefault(t *testing.T) {
	handler := newTestHandler(&stubLogsClient{}, nil)
	body := gen.HandleAlertWebhookJSONRequestBody{
		"Type":           "SubscriptionConfirmation",
		"MessageId":      "msg-1",
		"Token":          "token-1",
		"TopicArn":       "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp":      "2026-04-23T10:00:00Z",
		"SubscribeURL":   "https://sns.eu-north-1.amazonaws.com/?Action=ConfirmSubscription",
		"Signature":      "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message":        "You have chosen to subscribe",
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestHandleAlertWebhookSNSNotificationFallsBackToTagsForRuleIdentity(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	client := &stubLogsClient{
		getAlarmTagsByName: func(_ context.Context, _ string) (map[string]string, error) {
			return map[string]string{
				cloudwatch.TagRuleName:      "tagged-rule",
				cloudwatch.TagRuleNamespace: "tagged-ns",
			}, nil
		},
	}
	handler := NewLogsHandlerWithOptions(client, HandlerOptions{ObserverClient: observer}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Use a non-managed alarm name so SNS parser cannot recover identity from the name.
	body := gen.HandleAlertWebhookJSONRequestBody{
		"Type":           "Notification",
		"MessageId":      "msg-1",
		"TopicArn":       "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp":      "2026-04-23T10:00:00Z",
		"Signature":      "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message":        `{"AlarmName":"unmanaged-name","NewStateValue":"ALARM","StateChangeTime":"2026-04-23T10:00:05Z"}`,
	}
	if _, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "tagged-rule" || call.ruleNamespace != "tagged-ns" {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected forwarded call once tags are hydrated")
	}
}

func TestHandleAlertWebhookSNSNotificationDropsWhenRuleNameUnknown(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	client := &stubLogsClient{
		getAlarmTagsByName: func(_ context.Context, _ string) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}
	handler := NewLogsHandlerWithOptions(client, HandlerOptions{ObserverClient: observer}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := gen.HandleAlertWebhookJSONRequestBody{
		"Type":           "Notification",
		"MessageId":      "msg-1",
		"TopicArn":       "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp":      "2026-04-23T10:00:00Z",
		"Signature":      "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message":        `{"AlarmName":"unmanaged-name","NewStateValue":"ALARM","StateChangeTime":"2026-04-23T10:00:05Z"}`,
	}
	if _, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		t.Fatalf("did not expect any forwarded event, got %#v", call)
	case <-time.After(150 * time.Millisecond):
	}
}

// --- Direct unit tests for helpers ---------------------------------------

func TestParseWebhookBodyDispatchesByEnvelope(t *testing.T) {
	t.Run("eventbridge", func(t *testing.T) {
		raw := []byte(`{"source":"aws.cloudwatch","detail":{"alarmName":"x","state":{"value":"ALARM"}}}`)
		evt, confirm, err := parseWebhookBody(raw)
		if err != nil {
			t.Fatalf("parseWebhookBody() error = %v", err)
		}
		if confirm != nil || evt == nil || evt.State != "ALARM" {
			t.Fatalf("unexpected parse: evt=%+v confirm=%+v", evt, confirm)
		}
	})

	t.Run("lambda", func(t *testing.T) {
		raw := []byte(`{"alarmName":"a","ruleName":"r","ruleNamespace":"n","state":"ALARM","alertValue":3.5,"alertTimestamp":"2026-04-23T10:00:05Z"}`)
		evt, confirm, err := parseWebhookBody(raw)
		if err != nil {
			t.Fatalf("parseWebhookBody() error = %v", err)
		}
		if confirm != nil || evt == nil || evt.RuleName != "r" {
			t.Fatalf("unexpected parse: %+v", evt)
		}
	})

	t.Run("sns subscription", func(t *testing.T) {
		raw := []byte(`{"Type":"SubscriptionConfirmation","Token":"t","TopicArn":"arn","SubscribeURL":"https://sns.eu-north-1.amazonaws.com/x"}`)
		_, confirm, err := parseWebhookBody(raw)
		if err != nil {
			t.Fatalf("parseWebhookBody() error = %v", err)
		}
		if confirm == nil || !confirm.IsSubscriptionConfirm {
			t.Fatalf("expected subscription confirm: %+v", confirm)
		}
	})
}

func TestIsValidationError(t *testing.T) {
	if isValidationError(nil) {
		t.Fatal("nil error should not be a validation error")
	}
	if !isValidationError(errors.New("invalid: too short")) {
		t.Fatal("invalid: prefix should mark as validation error")
	}
	if isValidationError(errors.New("aws boom")) {
		t.Fatal("non-invalid error should not be flagged")
	}
}

func TestParseUUIDInvalidReturnsFalse(t *testing.T) {
	if _, ok := parseUUID("not-a-uuid"); ok {
		t.Fatal("expected invalid uuid to return ok=false")
	}
}

func TestStrPtrEmptyReturnsNil(t *testing.T) {
	if strPtr("") != nil {
		t.Fatal("expected empty string to map to nil")
	}
	if got := strPtr("hello"); got == nil || *got != "hello" {
		t.Fatalf("strPtr() = %v", got)
	}
}

func TestNewLogsHandlerSetsDefaults(t *testing.T) {
	h := NewLogsHandler(&stubLogsClient{}, discardLogger())
	if h.client == nil || h.observerClient != nil {
		t.Fatalf("unexpected handler state: %#v", h)
	}
}

func TestToComponentLogsParamsCopiesOptionalFields(t *testing.T) {
	componentUID := "33333333-3333-3333-3333-333333333333"
	envUID := "22222222-2222-2222-2222-222222222222"
	projectUID := "11111111-1111-1111-1111-111111111111"
	limit := 50
	sortOrder := gen.LogsQueryRequestSortOrder("asc")
	searchPhrase := "needle"
	logLevels := []gen.LogsQueryRequestLogLevels{"ERROR"}
	scope := gen.ComponentSearchScope{
		Namespace:      "default",
		ComponentUid:   &componentUID,
		EnvironmentUid: &envUID,
		ProjectUid:     &projectUID,
	}
	req := &gen.LogsQueryRequest{
		StartTime:    time.Now(),
		EndTime:      time.Now().Add(time.Minute),
		Limit:        &limit,
		SortOrder:    &sortOrder,
		SearchPhrase: &searchPhrase,
		LogLevels:    &logLevels,
	}
	got := toComponentLogsParams(req, &scope)
	if got.Namespace != "default" || got.Limit != 50 || got.SortOrder != "asc" || got.SearchPhrase != "needle" {
		t.Fatalf("unexpected component params: %#v", got)
	}
	if got.ProjectID != projectUID || got.EnvironmentID != envUID {
		t.Fatalf("unexpected component params IDs: %#v", got)
	}
	if len(got.LogLevels) != 1 || got.LogLevels[0] != "ERROR" {
		t.Fatalf("unexpected log levels: %v", got.LogLevels)
	}
	if len(got.ComponentIDs) != 1 || got.ComponentIDs[0] != componentUID {
		t.Fatalf("unexpected component IDs: %v", got.ComponentIDs)
	}
}

func TestToWorkflowLogsParamsCopiesOptionalFields(t *testing.T) {
	runName := "wf-run-123"
	limit := 200
	sortOrder := gen.LogsQueryRequestSortOrder("desc")
	searchPhrase := "needle"
	logLevels := []gen.LogsQueryRequestLogLevels{"WARN"}
	scope := gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: &runName}
	req := &gen.LogsQueryRequest{
		StartTime:    time.Now(),
		EndTime:      time.Now().Add(time.Minute),
		Limit:        &limit,
		SortOrder:    &sortOrder,
		SearchPhrase: &searchPhrase,
		LogLevels:    &logLevels,
	}
	got := toWorkflowLogsParams(req, &scope)
	if got.WorkflowRunName != runName || got.Limit != 200 || got.SortOrder != "desc" || got.SearchPhrase != "needle" {
		t.Fatalf("unexpected workflow params: %#v", got)
	}
	if len(got.LogLevels) != 1 || got.LogLevels[0] != "WARN" {
		t.Fatalf("unexpected log levels: %v", got.LogLevels)
	}
}

func TestToComponentLogsQueryResponseSerialises(t *testing.T) {
	now := time.Now().UTC()
	resp := toComponentLogsQueryResponse(&cloudwatch.ComponentLogsResult{
		Logs: []cloudwatch.ComponentLogsEntry{{
			Timestamp:    now,
			Log:          "hi",
			LogLevel:     "INFO",
			Namespace:    "default",
			ComponentUID: "33333333-3333-3333-3333-333333333333",
		}},
		TotalCount: 1,
		Took:       7,
	})
	if resp.Total == nil || *resp.Total != 1 {
		t.Fatalf("unexpected total: %#v", resp.Total)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), "\"total\":1") {
		t.Fatalf("unexpected response payload: %s", string(raw))
	}
}

func TestToWorkflowLogsQueryResponseSerialises(t *testing.T) {
	now := time.Now().UTC()
	resp := toWorkflowLogsQueryResponse(&cloudwatch.WorkflowLogsResult{
		Logs:       []cloudwatch.WorkflowLogsEntry{{Timestamp: now, Log: "step"}},
		TotalCount: 1,
		Took:       3,
	})
	if resp.TookMs == nil || *resp.TookMs != 3 {
		t.Fatalf("unexpected tookMs: %#v", resp.TookMs)
	}
}

func TestToAlertRuleResponsePopulatesUUIDsWhenValid(t *testing.T) {
	got := toAlertRuleResponse(&cloudwatch.AlertDetail{
		Name:           "high-error-rate",
		Namespace:      "payments",
		ProjectUID:     "11111111-1111-1111-1111-111111111111",
		EnvironmentUID: "22222222-2222-2222-2222-222222222222",
		ComponentUID:   "33333333-3333-3333-3333-333333333333",
		SearchPattern:  "ERROR",
		Operator:       "gt",
		Window:         5 * time.Minute,
		Interval:       time.Minute,
		Enabled:        true,
	})
	if got.Metadata == nil || got.Metadata.ProjectUid == nil || got.Metadata.EnvironmentUid == nil || got.Metadata.ComponentUid == nil {
		t.Fatalf("expected all UIDs to be populated: %#v", got.Metadata)
	}
}

func TestToAlertRuleResponseDropsInvalidUUIDs(t *testing.T) {
	got := toAlertRuleResponse(&cloudwatch.AlertDetail{
		Name:           "rule",
		Namespace:      "ns",
		ProjectUID:     "not-a-uuid",
		EnvironmentUID: "still-not",
		ComponentUID:   "nope",
		Operator:       "lt",
		Window:         time.Minute,
		Interval:       time.Minute,
	})
	if got.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}
	if got.Metadata.ProjectUid != nil || got.Metadata.EnvironmentUid != nil || got.Metadata.ComponentUid != nil {
		t.Fatalf("expected invalid UIDs to be dropped: %#v", got.Metadata)
	}
}
