// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/observer"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHealth(t *testing.T) {
	handler := NewMetricsHandler(nil, testLogger())
	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	healthResp, ok := resp.(gen.Health200JSONResponse)
	if !ok {
		t.Fatalf("unexpected response type: %T", resp)
	}
	if healthResp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", healthResp.Status)
	}
}

func TestHandleAlertmanagerWebhook_NilBody(t *testing.T) {
	handler := NewMetricsHandler(nil, testLogger())
	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertmanagerWebhook_NoAlerts(t *testing.T) {
	handler := NewMetricsHandler(nil, testLogger())
	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertmanagerWebhook_MultipleAlerts(t *testing.T) {
	handler := NewMetricsHandler(nil, testLogger())
	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{
				{Status: "firing", Annotations: gen.AlertmanagerAlert_Annotations{RuleName: "r1", RuleNamespace: "ns1", AlertValue: "1.0"}},
				{Status: "firing", Annotations: gen.AlertmanagerAlert_Annotations{RuleName: "r2", RuleNamespace: "ns2", AlertValue: "2.0"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertmanagerWebhook_NonFiringStatus(t *testing.T) {
	handler := NewMetricsHandler(nil, testLogger())
	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{
				{
					Status:      "resolved",
					Annotations: gen.AlertmanagerAlert_Annotations{RuleName: "r1", RuleNamespace: "ns1", AlertValue: "1.0"},
					StartsAt:    time.Now(),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertmanagerWebhook_MissingAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations gen.AlertmanagerAlert_Annotations
	}{
		{"missing rule_name", gen.AlertmanagerAlert_Annotations{RuleName: "", RuleNamespace: "ns1", AlertValue: "1.0"}},
		{"missing rule_namespace", gen.AlertmanagerAlert_Annotations{RuleName: "r1", RuleNamespace: "", AlertValue: "1.0"}},
		{"missing alert_value", gen.AlertmanagerAlert_Annotations{RuleName: "r1", RuleNamespace: "ns1", AlertValue: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewMetricsHandler(nil, testLogger())
			resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
				Body: &gen.AlertmanagerWebhookPayload{
					Alerts: []gen.AlertmanagerAlert{
						{
							Status:      "firing",
							Annotations: tt.annotations,
							StartsAt:    time.Now(),
						},
					},
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := resp.(gen.HandleAlertmanagerWebhook400JSONResponse); !ok {
				t.Fatalf("expected 400 response, got %T", resp)
			}
		})
	}
}

func TestHandleAlertmanagerWebhook_InvalidAlertValue(t *testing.T) {
	handler := NewMetricsHandler(nil, testLogger())
	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{
				{
					Status: "firing",
					Annotations: gen.AlertmanagerAlert_Annotations{
						RuleName:      "r1",
						RuleNamespace: "ns1",
						AlertValue:    "not-a-number",
					},
					StartsAt: time.Now(),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertmanagerWebhook_Success(t *testing.T) {
	alertTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := observer.NewClient(server.URL)
	handler := NewMetricsHandler(client, testLogger())

	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{
				{
					Status: "firing",
					Annotations: gen.AlertmanagerAlert_Annotations{
						RuleName:      "high-cpu",
						RuleNamespace: "production",
						AlertValue:    "95.5",
					},
					StartsAt: alertTime,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	successResp, ok := resp.(gen.HandleAlertmanagerWebhook200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if successResp.Status != gen.Success {
		t.Errorf("expected status 'success', got %q", successResp.Status)
	}
}

func TestHandleAlertmanagerWebhook_FiringCaseInsensitive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := observer.NewClient(server.URL)
	handler := NewMetricsHandler(client, testLogger())

	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{
				{
					Status: "FIRING",
					Annotations: gen.AlertmanagerAlert_Annotations{
						RuleName:      "rule1",
						RuleNamespace: "ns1",
						AlertValue:    "1.0",
					},
					StartsAt: time.Now(),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestExtractAlertFields_EdgeCases(t *testing.T) {
	tests := []struct {
		name              string
		ruleName          string
		ruleNamespace     string
		alertValue        string
		expectedRuleName  string
		expectedNamespace string
		expectedValue     float32
	}{
		{
			name:              "whitespace trimming",
			ruleName:          "  high-cpu  ",
			ruleNamespace:     "  production  ",
			alertValue:        "  95.5  ",
			expectedRuleName:  "high-cpu",
			expectedNamespace: "production",
			expectedValue:     95.5,
		},
		{
			name:              "negative number",
			ruleName:          "low-temp",
			ruleNamespace:     "monitoring",
			alertValue:        "-42.5",
			expectedRuleName:  "low-temp",
			expectedNamespace: "monitoring",
			expectedValue:     -42.5,
		},
		{
			name:              "zero value",
			ruleName:          "zero-check",
			ruleNamespace:     "default",
			alertValue:        "0",
			expectedRuleName:  "zero-check",
			expectedNamespace: "default",
			expectedValue:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := gen.AlertmanagerAlert{
				Status: "firing",
				Annotations: gen.AlertmanagerAlert_Annotations{
					RuleName:      tt.ruleName,
					RuleNamespace: tt.ruleNamespace,
					AlertValue:    tt.alertValue,
				},
			}

			ruleName, ruleNamespace, alertValue, err := extractAlertFields(alert)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ruleName != tt.expectedRuleName {
				t.Errorf("expected ruleName %q, got %q", tt.expectedRuleName, ruleName)
			}
			if ruleNamespace != tt.expectedNamespace {
				t.Errorf("expected ruleNamespace %q, got %q", tt.expectedNamespace, ruleNamespace)
			}
			if alertValue != tt.expectedValue {
				t.Errorf("expected alertValue %v, got %v", tt.expectedValue, alertValue)
			}
		})
	}
}

func TestHandleAlertmanagerWebhook_ForwardError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := observer.NewClient(server.URL)
	handler := NewMetricsHandler(client, testLogger())

	resp, err := handler.HandleAlertmanagerWebhook(context.Background(), gen.HandleAlertmanagerWebhookRequestObject{
		Body: &gen.AlertmanagerWebhookPayload{
			Alerts: []gen.AlertmanagerAlert{
				{
					Status: "firing",
					Annotations: gen.AlertmanagerAlert_Annotations{
						RuleName:      "rule1",
						RuleNamespace: "ns1",
						AlertValue:    "1.0",
					},
					StartsAt: time.Now(),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertmanagerWebhook500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}
