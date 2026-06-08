// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor // import "github.com/openchoreo/community-modules/observability-events-otel-collector/k8seventenrichprocessor"

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.uber.org/zap"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Attributes set by the k8seventsreceiver for the involved object.
const (
	attrObjectKind = "k8s.object.kind"
	attrObjectName = "k8s.object.name"
	// Namespace of the event, (same as it of the involved object) lives on the LOG RECORD.
	attrNamespaceName = "k8s.namespace.name"
)

// defaultCacheSyncTimeout bounds the initial WaitForCacheSync in Start,
// so a missing RBAC grant fails startup loudly instead of hanging forever.
const defaultCacheSyncTimeout = 2 * time.Minute

// enrichProcessor enriches Kubernetes events with metadata of the involved
// object, served from cluster-wide informer caches.
type enrichProcessor struct {
	logger *zap.Logger
	next   consumer.Logs

	labels       fieldExtractor
	annotations  fieldExtractor
	owners       ownerExtractor
	resyncPeriod time.Duration

	cacheSyncTimeout time.Duration

	// newClientset is the Kubernetes client source; overridable in tests.
	newClientset func() (kubernetes.Interface, error)

	// Established in Start.
	factory  informers.SharedInformerFactory
	getters  map[string]objectGetter
	stopCh   chan struct{}
	stopOnce sync.Once
}

func newProcessor(set processor.Settings, cfg *Config, next consumer.Logs) *enrichProcessor {
	return &enrichProcessor{
		logger:           set.Logger,
		next:             next,
		labels:           newFieldExtractor(cfg.Labels),
		annotations:      newFieldExtractor(cfg.Annotations),
		owners:           newOwnerExtractor(cfg.OwnerReferences),
		resyncPeriod:     cfg.ResyncPeriod,
		cacheSyncTimeout: cfg.CacheSyncTimeout,
		newClientset:     inClusterClientset,
	}
}

func (ep *enrichProcessor) enrichmentEnabled() bool {
	return ep.labels.enabled || ep.annotations.enabled || ep.owners.enabled
}

func inClusterClientset() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	return kubernetes.NewForConfig(cfg)
}

// Start builds the client, starts the informers, and blocks until caches are warm.
func (ep *enrichProcessor) Start(ctx context.Context, _ component.Host) error {
	if !ep.enrichmentEnabled() {
		ep.logger.Info("k8seventenrich: all enrichment sources disabled; running as pass-through (no caches started)")
		return nil
	}

	clientset, err := ep.newClientset()
	if err != nil {
		return fmt.Errorf("k8seventenrich: %w", err)
	}

	ep.stopCh = make(chan struct{})
	ep.factory = informers.NewSharedInformerFactory(clientset, ep.resyncPeriod)
	ep.getters = registerKindGetters(ep.factory)

	ep.factory.Start(ep.stopCh)
	if err := ep.waitForCacheSync(ctx); err != nil {
		ep.stopInformers()
		return err
	}

	ep.logger.Info("k8seventenrich caches synced; enrichment active",
		zap.Int("watched_kinds", len(ep.getters)),
		zap.Bool("labels", ep.labels.enabled),
		zap.Bool("annotations", ep.annotations.enabled),
		zap.Bool("owner_references", ep.owners.enabled),
	)
	return nil
}

// waitForCacheSync blocks until every informer cache has synced, or errors out if
// the start context is cancelled, the processor is shut down, or the timeout elapses.
func (ep *enrichProcessor) waitForCacheSync(ctx context.Context) error {
	syncCtx, cancel := context.WithTimeout(ctx, ep.cacheSyncTimeout)
	defer cancel()

	stopCh := ep.stopCh
	syncStopCh := make(chan struct{})
	go func() {
		select {
		case <-syncCtx.Done():
		case <-stopCh:
		}
		close(syncStopCh)
	}()

	for typ, ok := range ep.factory.WaitForCacheSync(syncStopCh) {
		if !ok {
			return fmt.Errorf("k8seventenrich: informer cache for %s did not sync within %s; "+
				"check the service account has get/list/watch on every watched kind", typ, ep.cacheSyncTimeout)
		}
	}
	return nil
}

func (ep *enrichProcessor) Shutdown(_ context.Context) error {
	ep.stopInformers()
	return nil
}

// stopInformers stops the watches and drains their goroutines.
func (ep *enrichProcessor) stopInformers() {
	ep.stopOnce.Do(func() {
		if ep.stopCh != nil {
			close(ep.stopCh)
		}
		if ep.factory != nil {
			ep.factory.Shutdown() // blocks until informer goroutines exit
		}
	})
}

func (ep *enrichProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeLogs enriches each event's resource attributes from the involved object, then forwards the batch.
// Best-effort: events whose kind is unwatched or whose object is not cached pass through unchanged.
func (ep *enrichProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	if !ep.enrichmentEnabled() {
		return ep.next.ConsumeLogs(ctx, ld)
	}

	resourceLogs := ld.ResourceLogs()
	for i := 0; i < resourceLogs.Len(); i++ {
		rl := resourceLogs.At(i)
		resAttrs := rl.Resource().Attributes()

		kind, ok := getStr(resAttrs, attrObjectKind)
		if !ok || kind == "" {
			continue
		}
		name, ok := getStr(resAttrs, attrObjectName)
		if !ok || name == "" {
			continue
		}
		get, watched := ep.getters[strings.ToLower(kind)]
		if !watched {
			continue // kind the processor doesn't watch (e.g. Node, Namespace, a CRD)
		}

		namespace := namespaceFromRecords(rl)
		if namespace == "" {
			continue // cluster-scoped or missing; namespaced listers need it
		}

		obj, err := get(namespace, name)
		if err != nil {
			// Not found / not yet synced. Leave the event unchanged.
			continue
		}

		ep.labels.inject(resAttrs, obj.GetLabels())
		ep.annotations.inject(resAttrs, obj.GetAnnotations())
		ep.owners.inject(resAttrs, obj.GetOwnerReferences())
	}

	return ep.next.ConsumeLogs(ctx, ld)
}

// namespaceFromRecords returns the first non-empty k8s.namespace.name on any record.
// k8seventsreceiver emits exactly one record per resource, hence the first match.
func namespaceFromRecords(rl plog.ResourceLogs) string {
	scopeLogs := rl.ScopeLogs()
	for i := 0; i < scopeLogs.Len(); i++ {
		records := scopeLogs.At(i).LogRecords()
		for j := 0; j < records.Len(); j++ {
			if v, ok := records.At(j).Attributes().Get(attrNamespaceName); ok {
				if s := v.Str(); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func getStr(m pcommon.Map, key string) (string, bool) {
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	return v.AsString(), true
}
