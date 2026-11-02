// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openchoreo/community-modules/observability-metrics-aws-cloudwatch/internal/cloudwatchmetrics"
)

func TestKubernetesScopeResolverReleaseLine(t *testing.T) {
	mainPod := map[string]string{
		cloudwatchmetrics.LabelComponentUID:   testComponentUID,
		cloudwatchmetrics.LabelEnvironmentUID: testEnvironmentUID,
		cloudwatchmetrics.LabelNamespace:      "payments",
	}
	linePod := map[string]string{
		cloudwatchmetrics.LabelReleaseLineUID:          testReleaseLineUID,
		cloudwatchmetrics.LabelReleaseLineComponentUID: testComponentUID,
		cloudwatchmetrics.LabelEnvironmentUID:          testEnvironmentUID,
		cloudwatchmetrics.LabelNamespace:               "payments",
	}
	envSelector := cloudwatchmetrics.LabelEnvironmentUID + "=" + testEnvironmentUID
	tests := map[string]struct {
		params       ScopeResolutionParams
		podLabels    map[string]string
		wantSelector string
	}{
		"main line": {
			params:       ScopeResolutionParams{ComponentUID: testComponentUID, EnvironmentUID: testEnvironmentUID},
			podLabels:    mainPod,
			wantSelector: cloudwatchmetrics.LabelComponentUID + "=" + testComponentUID + "," + envSelector,
		},
		"release line": {
			params:       ScopeResolutionParams{ComponentUID: testComponentUID, EnvironmentUID: testEnvironmentUID, ReleaseLineUID: testReleaseLineUID},
			podLabels:    linePod,
			wantSelector: cloudwatchmetrics.LabelReleaseLineUID + "=" + testReleaseLineUID + "," + envSelector,
		},
		// A line pod's component UID comes from its release line component UID label.
		"release line without component": {
			params:       ScopeResolutionParams{EnvironmentUID: testEnvironmentUID, ReleaseLineUID: testReleaseLineUID},
			podLabels:    linePod,
			wantSelector: cloudwatchmetrics.LabelReleaseLineUID + "=" + testReleaseLineUID + "," + envSelector,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var gotSelector string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSelector = r.URL.Query().Get("labelSelector")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"items": []any{map[string]any{"metadata": map[string]any{"labels": tt.podLabels}}},
				})
			}))
			defer srv.Close()

			tokenPath := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenPath, []byte("test-token"), 0o600); err != nil {
				t.Fatalf("write token: %v", err)
			}
			r := &KubernetesScopeResolver{baseURL: srv.URL, tokenPath: tokenPath, httpClient: srv.Client(), logger: discardLogger()}

			got, ok, err := r.Resolve(context.Background(), tt.params)
			if err != nil || !ok {
				t.Fatalf("Resolve() = %v, %v, want ok", ok, err)
			}
			if gotSelector != tt.wantSelector {
				t.Errorf("labelSelector = %q, want %q", gotSelector, tt.wantSelector)
			}
			if got.ComponentUID != testComponentUID || got.Namespace != "payments" {
				t.Errorf("ComponentUID, Namespace = %q, %q, want %q, %q", got.ComponentUID, got.Namespace, testComponentUID, "payments")
			}
		})
	}
}
