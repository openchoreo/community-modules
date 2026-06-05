// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor // import "github.com/openchoreo/community-modules/observability-events-otel-collector/k8seventenrichprocessor"

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

// typeStr is the config key for this processor in a pipeline (eg. processors: [k8seventenrich]).
var typeStr = component.MustNewType("k8seventenrich")

const (
	defaultResyncPeriod     = 10 * time.Minute
	defaultLabelPrefix      = "k8s.object.label."
	defaultAnnotationPrefix = "k8s.object.annotation."
	defaultOwnerPrefix      = "k8s.object.owner."

	// lastAppliedAnnotation duplicates the full object spec and is excluded from annotation enrichment by default.
	lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
)

func NewFactory() processor.Factory {
	return processor.NewFactory(
		typeStr,
		createDefaultConfig,
		processor.WithLogs(createLogsProcessor, component.StabilityLevelAlpha),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ResyncPeriod:     defaultResyncPeriod,
		CacheSyncTimeout: defaultCacheSyncTimeout,
		Labels: FieldConfig{
			Enabled: true,
			Prefix:  defaultLabelPrefix,
		},
		// Annotations are disabled by default since they may be noisy and may carry sensitive values.
		Annotations: FieldConfig{
			Enabled: false,
			Prefix:  defaultAnnotationPrefix,
			Exclude: []string{lastAppliedAnnotation},
		},
		OwnerReferences: OwnerConfig{
			Enabled: true,
			Prefix:  defaultOwnerPrefix,
		},
	}
}

func createLogsProcessor(
	_ context.Context,
	set processor.Settings,
	cfg component.Config,
	next consumer.Logs,
) (processor.Logs, error) {
	return newProcessor(set, cfg.(*Config), next), nil
}
