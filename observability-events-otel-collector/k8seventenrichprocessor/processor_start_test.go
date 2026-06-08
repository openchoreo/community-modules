// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// testSettings builds the minimal processor.Settings (just the logger), avoiding
// the componenttest helpers so the test stays on packages already in go.mod.
func testSettings() processor.Settings {
	return processor.Settings{
		TelemetrySettings: component.TelemetrySettings{Logger: zap.NewNop()},
	}
}

// TestStartSyncsCachesAndEnriches drives the real informer path against a fake API
// server: Start warms the caches, then ConsumeLogs enriches from them.
func TestStartSyncsCachesAndEnriches(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "checkout-abc",
		Namespace: "store",
		Labels:    map[string]string{"app": "checkout"},
	}}
	client := fake.NewSimpleClientset(pod)

	sink := &capturingConsumer{}
	ep := newProcessor(testSettings(), defaultConfig(), sink)
	ep.cacheSyncTimeout = 10 * time.Second
	ep.newClientset = func() (kubernetes.Interface, error) { return client, nil }

	// host is ignored by Start, so a nil host is fine here.
	if err := ep.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = ep.Shutdown(context.Background()) })

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatalf("ConsumeLogs: %v", err)
	}
	if got, _ := resourceAttr(sink.last, defaultLabelPrefix+"app"); got != "checkout" {
		t.Errorf("app label = %q, want checkout (cache was not consulted)", got)
	}
}

// TestStartSkippedWhenEnrichmentDisabled verifies that with every source off, Start
// builds no client and warms no caches, and Shutdown is safe after a no-op Start.
func TestStartSkippedWhenEnrichmentDisabled(t *testing.T) {
	cfg := defaultConfig()
	cfg.Labels.Enabled = false
	cfg.Annotations.Enabled = false
	cfg.OwnerReferences.Enabled = false

	called := false
	ep := newProcessor(testSettings(), cfg, &capturingConsumer{})
	ep.newClientset = func() (kubernetes.Interface, error) {
		called = true
		return fake.NewSimpleClientset(), nil
	}

	if err := ep.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if called {
		t.Error("newClientset must not be called when all enrichment is disabled")
	}
	if err := ep.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after no-op Start: %v", err)
	}
}

// TestShutdownIsIdempotent guards the stopInformers contract: a second call must
// not panic on a double close.
func TestShutdownIsIdempotent(t *testing.T) {
	ep := newProcessor(testSettings(), defaultConfig(), &capturingConsumer{})
	ep.newClientset = func() (kubernetes.Interface, error) { return fake.NewSimpleClientset(), nil }

	if err := ep.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := ep.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := ep.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}
