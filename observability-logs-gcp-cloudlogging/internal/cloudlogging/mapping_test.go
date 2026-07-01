// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"testing"
	"time"

	"cloud.google.com/go/logging"
	mrpb "google.golang.org/genproto/googleapis/api/monitoredres"
)

func TestMapComponentEntry(t *testing.T) {
	ts := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	e := &logging.Entry{
		Timestamp: ts,
		Severity:  logging.Error,
		Payload:   "something failed",
		Labels: map[string]string{
			podLabelKey(LabelComponentUID):   "c-uid",
			podLabelKey(LabelProjectUID):     "p-uid",
			podLabelKey(LabelEnvironmentUID): "e-uid",
			podLabelKey(LabelNamespace):      "oc-ns",
		},
		Resource: &mrpb.MonitoredResource{
			Type: "k8s_container",
			Labels: map[string]string{
				"namespace_name": "dp-ns",
				"pod_name":       "pod-1",
				"container_name": "main",
			},
		},
	}

	got := mapComponentEntry(e)
	if got.LogMessage != "something failed" {
		t.Errorf("LogMessage = %q", got.LogMessage)
	}
	if got.LogLevel != "ERROR" {
		t.Errorf("LogLevel = %q, want ERROR", got.LogLevel)
	}
	if got.PodName != "pod-1" || got.ContainerName != "main" || got.PodNamespace != "dp-ns" {
		t.Errorf("resource labels not mapped: %+v", got)
	}
	if got.ComponentUID != "c-uid" || got.ProjectUID != "p-uid" || got.EnvironmentUID != "e-uid" || got.OpenChoreoNamespace != "oc-ns" {
		t.Errorf("openchoreo labels not mapped: %+v", got)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, ts)
	}
}

func TestMapComponentEntry_NilResource(t *testing.T) {
	got := mapComponentEntry(&logging.Entry{Payload: "x"})
	if got.PodName != "" || got.PodNamespace != "" {
		t.Errorf("nil resource should yield empty resource fields: %+v", got)
	}
}

func TestPayloadString(t *testing.T) {
	if got := payloadString("plain"); got != "plain" {
		t.Errorf("string payload = %q", got)
	}
	if got := payloadString(nil); got != "" {
		t.Errorf("nil payload = %q", got)
	}
	// Structured payload prefers a conventional message field.
	if got := payloadString(map[string]interface{}{"message": "hi", "k": "v"}); got != "hi" {
		t.Errorf("structured payload message = %q, want hi", got)
	}
	// Structured payload without a message field falls back to JSON.
	got := payloadString(map[string]interface{}{"k": "v"})
	if got != `{"k":"v"}` {
		t.Errorf("structured payload JSON = %q", got)
	}
}

func TestResolveLogLevel(t *testing.T) {
	// 1. Structured GCP severity wins.
	if got := resolveLogLevel(logging.Warning, "anything"); got != "WARN" {
		t.Errorf("GCP Warning should map to WARN; got %q", got)
	}
	if got := resolveLogLevel(logging.Critical, "x"); got != "ERROR" {
		t.Errorf("GCP Critical should collapse to ERROR; got %q", got)
	}
	if got := resolveLogLevel(logging.Notice, "x"); got != "INFO" {
		t.Errorf("GCP Notice should collapse to INFO; got %q", got)
	}
	// 2. Unset severity + JSON envelope.
	if got := resolveLogLevel(logging.Default, `{"level":"warning","msg":"x"}`); got != "WARN" {
		t.Errorf("JSON envelope warning should map to WARN; got %q", got)
	}
	// 3. Unset severity + keyword scan.
	if got := resolveLogLevel(logging.Default, "this is an ERROR line"); got != "ERROR" {
		t.Errorf("keyword scan should find ERROR; got %q", got)
	}
	// 4. Default to INFO.
	if got := resolveLogLevel(logging.Default, "nothing notable"); got != "INFO" {
		t.Errorf("default should be INFO; got %q", got)
	}
}

func TestFromGCPSeverity(t *testing.T) {
	cases := map[logging.Severity]string{
		logging.Debug:     "DEBUG",
		logging.Info:      "INFO",
		logging.Notice:    "INFO",
		logging.Warning:   "WARN",
		logging.Error:     "ERROR",
		logging.Critical:  "ERROR",
		logging.Alert:     "ERROR",
		logging.Emergency: "ERROR",
		logging.Default:   "",
	}
	for sev, want := range cases {
		if got := fromGCPSeverity(sev); got != want {
			t.Errorf("fromGCPSeverity(%v) = %q, want %q", sev, got, want)
		}
	}
}
