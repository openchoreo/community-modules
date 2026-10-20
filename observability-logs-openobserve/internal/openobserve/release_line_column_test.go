// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func unknownFieldServer(message string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":20004,"message":"` + message + `"}`))
	}))
}

func TestGetComponentLogs_NoReleaseLineColumnYet(t *testing.T) {
	server := unknownFieldServer("unknown field 'kubernetes_labels_openchoreo_dev_release_line_uid'")
	defer server.Close()

	result, err := newTestClient(server.URL).GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "test-ns", ReleaseLineID: testReleaseLineID,
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("expected no error before any record carries a line, got: %v", err)
	}
	if len(result.Logs) != 0 || result.TotalCount != 0 {
		t.Errorf("expected empty result, got logs=%d total=%d", len(result.Logs), result.TotalCount)
	}
}

func TestGetComponentEvents_NoReleaseLineColumnYet(t *testing.T) {
	server := unknownFieldServer("unknown field 'k8s_object_label_openchoreo_dev_release_line_uid'")
	defer server.Close()

	result, err := newTestClient(server.URL).GetComponentEvents(context.Background(), EventsQueryParams{
		Namespace: "test-ns", ReleaseLineID: testReleaseLineID,
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("expected no error before any event carries a line, got: %v", err)
	}
	if len(result.Events) != 0 || result.TotalCount != 0 {
		t.Errorf("expected empty result, got events=%d total=%d", len(result.Events), result.TotalCount)
	}
}

func TestGetComponentLogs_OtherUnknownFieldStillFails(t *testing.T) {
	server := unknownFieldServer("unknown field 'kubernetes_labels_openchoreo_dev_component_uid'")
	defer server.Close()

	_, err := newTestClient(server.URL).GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "test-ns", StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
	})
	if err == nil {
		t.Fatal("expected an unknown field other than the release line column to fail")
	}
}
