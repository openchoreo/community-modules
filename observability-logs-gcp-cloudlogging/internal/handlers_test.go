// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"strings"
	"testing"

	gen "github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/api/gen"
)

// validAlertRequest returns an AlertRuleRequest with all required fields set,
// which callers mutate to exercise one missing field at a time.
func validAlertRequest() gen.AlertRuleRequest {
	var req gen.AlertRuleRequest
	req.Metadata.Name = "too-many-errors"
	req.Metadata.Namespace = "default"
	req.Source.Query = "panic"
	req.Condition.Operator = "gt"
	req.Condition.Threshold = 3
	req.Condition.Window = "PT5M"
	req.Condition.Enabled = true
	return req
}

func TestQueryEvents_NotImplemented(t *testing.T) {
	h := &LogsHandler{}
	resp, err := h.QueryEvents(context.Background(), gen.QueryEventsRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(gen.QueryEvents500JSONResponse)
	if !ok {
		t.Fatalf("response type = %T, want QueryEvents500JSONResponse", resp)
	}
	if got.ErrorCode == nil || *got.ErrorCode != errCodeNotImplemented {
		t.Errorf("errorCode = %v, want %q", got.ErrorCode, errCodeNotImplemented)
	}
}

func TestRuleInputFromRequest_RequiresFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*gen.AlertRuleRequest)
		wantErr string
	}{
		{"valid", func(*gen.AlertRuleRequest) {}, ""},
		{"missing name", func(r *gen.AlertRuleRequest) { r.Metadata.Name = "" }, "metadata.name"},
		{"missing namespace", func(r *gen.AlertRuleRequest) { r.Metadata.Namespace = "" }, "metadata.namespace"},
		{"missing operator", func(r *gen.AlertRuleRequest) { r.Condition.Operator = "" }, "condition.operator"},
		{"missing query", func(r *gen.AlertRuleRequest) { r.Source.Query = "" }, "source.query"},
		{"blank query", func(r *gen.AlertRuleRequest) { r.Source.Query = "   " }, "source.query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validAlertRequest()
			tt.mutate(&req)
			_, err := ruleInputFromRequest(req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
