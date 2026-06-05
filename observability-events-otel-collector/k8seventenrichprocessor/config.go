// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor // import "github.com/openchoreo/community-modules/observability-events-otel-collector/k8seventenrichprocessor"

import (
	"fmt"
	"time"
)

type Config struct {
	// ResyncPeriod is how often the informer caches are fully re-listed.
	// Zero disables periodic resync. Defaults to 10m.
	ResyncPeriod time.Duration `mapstructure:"resync_period"`

	// CacheSyncTimeout bounds how long Start waits for caches to warm up. Defaults to 2m.
	CacheSyncTimeout time.Duration `mapstructure:"cache_sync_timeout"`

	Labels          FieldConfig `mapstructure:"labels"`
	Annotations     FieldConfig `mapstructure:"annotations"`
	OwnerReferences OwnerConfig `mapstructure:"owner_references"`
}

// FieldConfig controls enrichment from a key/value metadata map (labels or annotations).
type FieldConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`

	// Include, when non-empty, restricts enrichment to this allow-list (empty = all).
	Include []string `mapstructure:"include"`

	// Exclude is a deny-list that takes precedence over Include. Setting this in config
	// replaces the default exclusion (kubectl.kubernetes.io/last-applied-configuration) unless re-listed.
	Exclude []string `mapstructure:"exclude"`
}

// OwnerConfig controls enrichment from the controlling owner reference (controller=true).
type OwnerConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

func (cfg *Config) Validate() error {
	if cfg.ResyncPeriod < 0 {
		return fmt.Errorf("resync_period must not be negative, got %s", cfg.ResyncPeriod)
	}
	if cfg.CacheSyncTimeout <= 0 {
		return fmt.Errorf("cache_sync_timeout must be positive, got %s", cfg.CacheSyncTimeout)
	}
	if cfg.Labels.Enabled && cfg.Labels.Prefix == "" {
		return fmt.Errorf("labels.prefix must not be empty when labels are enabled")
	}
	if cfg.Annotations.Enabled && cfg.Annotations.Prefix == "" {
		return fmt.Errorf("annotations.prefix must not be empty when annotations are enabled")
	}
	if cfg.OwnerReferences.Enabled && cfg.OwnerReferences.Prefix == "" {
		return fmt.Errorf("owner_references.prefix must not be empty when owner_references are enabled")
	}
	return nil
}
