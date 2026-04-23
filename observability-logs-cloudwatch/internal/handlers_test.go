package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
