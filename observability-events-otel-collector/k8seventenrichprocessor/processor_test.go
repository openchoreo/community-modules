// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type capturingConsumer struct{ last plog.Logs }

func (c *capturingConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (c *capturingConsumer) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	c.last = ld
	return nil
}

func fakeObject(labels, annotations map[string]string, owners []metav1.OwnerReference) metav1.Object {
	return &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{
		Labels:          labels,
		Annotations:     annotations,
		OwnerReferences: owners,
	}}
}

func controllerRef(kind, name, uid string) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{Kind: kind, Name: name, UID: types.UID(uid), Controller: &controller}
}

// makeEvent builds a single-event plog.Logs the way k8seventsreceiver would:
// kind/name on the resource, namespace on the record.
func makeEvent(kind, name, namespace string) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	res := rl.Resource().Attributes()
	res.PutStr(attrObjectKind, kind)
	res.PutStr(attrObjectName, name)
	lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	if namespace != "" {
		lr.Attributes().PutStr(attrNamespaceName, namespace)
	}
	return ld
}

func newTestProcessor(t *testing.T, cfg *Config, obj metav1.Object) (*enrichProcessor, *capturingConsumer) {
	t.Helper()
	sink := &capturingConsumer{}
	ep := &enrichProcessor{
		logger:      zap.NewNop(),
		next:        sink,
		labels:      newFieldExtractor(cfg.Labels),
		annotations: newFieldExtractor(cfg.Annotations),
		owners:      newOwnerExtractor(cfg.OwnerReferences),
		getters: map[string]objectGetter{
			"pod": func(_, _ string) (metav1.Object, error) { return obj, nil },
		},
	}
	return ep, sink
}

func defaultConfig() *Config { return createDefaultConfig().(*Config) }

func resourceAttr(ld plog.Logs, key string) (string, bool) {
	v, ok := ld.ResourceLogs().At(0).Resource().Attributes().Get(key)
	if !ok {
		return "", false
	}
	return v.AsString(), true
}

func TestEnrichesLabels(t *testing.T) {
	ep, sink := newTestProcessor(t, defaultConfig(),
		fakeObject(map[string]string{"app": "checkout", "tier": "backend"}, nil, nil))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatal(err)
	}
	if got, _ := resourceAttr(sink.last, defaultLabelPrefix+"app"); got != "checkout" {
		t.Errorf("app label = %q, want checkout", got)
	}
	if got, _ := resourceAttr(sink.last, defaultLabelPrefix+"tier"); got != "backend" {
		t.Errorf("tier label = %q, want backend", got)
	}
}

func TestEnrichesAnnotationsAndExcludesLastApplied(t *testing.T) {
	// Annotations are disabled by default; enable them for this test.
	cfg := defaultConfig()
	cfg.Annotations.Enabled = true
	ep, sink := newTestProcessor(t, cfg,
		fakeObject(nil, map[string]string{
			"prometheus.io/scrape": "true",
			lastAppliedAnnotation:  "{huge json}",
		}, nil))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatal(err)
	}
	if got, _ := resourceAttr(sink.last, defaultAnnotationPrefix+"prometheus.io/scrape"); got != "true" {
		t.Errorf("scrape annotation = %q, want true", got)
	}
	if _, ok := resourceAttr(sink.last, defaultAnnotationPrefix+lastAppliedAnnotation); ok {
		t.Error("last-applied-configuration should be excluded by default")
	}
}

func TestEnrichesControllerOwner(t *testing.T) {
	owners := []metav1.OwnerReference{
		// A non-controller owner should be ignored.
		{Kind: "SomethingElse", Name: "x"},
		controllerRef("ReplicaSet", "checkout-6f55", "rs-uid-123"),
	}
	ep, sink := newTestProcessor(t, defaultConfig(), fakeObject(nil, nil, owners))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatal(err)
	}
	if got, _ := resourceAttr(sink.last, defaultOwnerPrefix+"kind"); got != "ReplicaSet" {
		t.Errorf("owner.kind = %q, want ReplicaSet", got)
	}
	if got, _ := resourceAttr(sink.last, defaultOwnerPrefix+"name"); got != "checkout-6f55" {
		t.Errorf("owner.name = %q, want checkout-6f55", got)
	}
	if got, _ := resourceAttr(sink.last, defaultOwnerPrefix+"uid"); got != "rs-uid-123" {
		t.Errorf("owner.uid = %q, want rs-uid-123", got)
	}
}

func TestNoControllerOwnerEmitsNothing(t *testing.T) {
	// Only a non-controller owner present.
	owners := []metav1.OwnerReference{{Kind: "SomethingElse", Name: "x"}}
	ep, sink := newTestProcessor(t, defaultConfig(), fakeObject(nil, nil, owners))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatal(err)
	}
	if _, ok := resourceAttr(sink.last, defaultOwnerPrefix+"kind"); ok {
		t.Error("no controller owner: owner attributes should not be set")
	}
}

func TestDisabledSourceSkipped(t *testing.T) {
	cfg := defaultConfig()
	cfg.Labels.Enabled = false
	ep, sink := newTestProcessor(t, cfg,
		fakeObject(map[string]string{"app": "checkout"}, nil, nil))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatal(err)
	}
	if _, ok := resourceAttr(sink.last, defaultLabelPrefix+"app"); ok {
		t.Error("labels disabled: should not enrich labels")
	}
}

func TestUnwatchedKindPassesThrough(t *testing.T) {
	ep, sink := newTestProcessor(t, defaultConfig(),
		fakeObject(map[string]string{"app": "checkout"}, nil, nil))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Node", "worker-1", "")); err != nil {
		t.Fatal(err)
	}
	if _, ok := resourceAttr(sink.last, defaultLabelPrefix+"app"); ok {
		t.Error("unwatched kind should not be enriched")
	}
}

func TestMissingNamespacePassesThrough(t *testing.T) {
	ep, sink := newTestProcessor(t, defaultConfig(),
		fakeObject(map[string]string{"app": "checkout"}, nil, nil))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "")); err != nil {
		t.Fatal(err)
	}
	if _, ok := resourceAttr(sink.last, defaultLabelPrefix+"app"); ok {
		t.Error("event without namespace should not be enriched")
	}
}

func TestIncludeExcludeFilters(t *testing.T) {
	cfg := defaultConfig()
	cfg.Labels.Include = []string{"app", "tier"}
	cfg.Labels.Exclude = []string{"tier"}
	ep, sink := newTestProcessor(t, cfg,
		fakeObject(map[string]string{"app": "checkout", "tier": "backend", "secret": "x"}, nil, nil))

	if err := ep.ConsumeLogs(context.Background(), makeEvent("Pod", "checkout-abc", "store")); err != nil {
		t.Fatal(err)
	}
	if _, ok := resourceAttr(sink.last, defaultLabelPrefix+"app"); !ok {
		t.Error("app should be included")
	}
	if _, ok := resourceAttr(sink.last, defaultLabelPrefix+"tier"); ok {
		t.Error("tier should be excluded even though it is in the include list")
	}
	if _, ok := resourceAttr(sink.last, defaultLabelPrefix+"secret"); ok {
		t.Error("secret should be dropped (not in include list)")
	}
}
