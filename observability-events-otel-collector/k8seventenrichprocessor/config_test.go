// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8seventenrichprocessor

import (
	"testing"
	"time"
)

// TestConfigValidate exercises every branch of (*Config).Validate. Each case starts
// from a valid baseline (defaultConfig) and mutates a single field, so a failure
// points at exactly one validation rule.
func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid default config",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name:    "negative resync_period",
			mutate:  func(c *Config) { c.ResyncPeriod = -1 * time.Minute },
			wantErr: true,
		},
		{
			name:    "zero resync_period is allowed (disables resync)",
			mutate:  func(c *Config) { c.ResyncPeriod = 0 },
			wantErr: false,
		},
		{
			name:    "zero cache_sync_timeout",
			mutate:  func(c *Config) { c.CacheSyncTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "negative cache_sync_timeout",
			mutate:  func(c *Config) { c.CacheSyncTimeout = -1 * time.Second },
			wantErr: true,
		},
		{
			name:    "labels enabled with empty prefix",
			mutate:  func(c *Config) { c.Labels.Enabled = true; c.Labels.Prefix = "" },
			wantErr: true,
		},
		{
			name:    "labels disabled with empty prefix is allowed",
			mutate:  func(c *Config) { c.Labels.Enabled = false; c.Labels.Prefix = "" },
			wantErr: false,
		},
		{
			name:    "annotations enabled with empty prefix",
			mutate:  func(c *Config) { c.Annotations.Enabled = true; c.Annotations.Prefix = "" },
			wantErr: true,
		},
		{
			name:    "annotations disabled with empty prefix is allowed",
			mutate:  func(c *Config) { c.Annotations.Enabled = false; c.Annotations.Prefix = "" },
			wantErr: false,
		},
		{
			name:    "owner_references enabled with empty prefix",
			mutate:  func(c *Config) { c.OwnerReferences.Enabled = true; c.OwnerReferences.Prefix = "" },
			wantErr: true,
		},
		{
			name:    "owner_references disabled with empty prefix is allowed",
			mutate:  func(c *Config) { c.OwnerReferences.Enabled = false; c.OwnerReferences.Prefix = "" },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
