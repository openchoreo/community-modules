// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import (
	"testing"
	"time"
)

func TestBuildFilter(t *testing.T) {
	tests := []struct {
		name         string
		namespace    string
		envUID       string
		projectUID   string
		componentUID string
		want         string
	}{
		{
			name:      "namespace and environment only",
			namespace: "default",
			envUID:    "3d9d7f27-f0ab-4310-ae0b-4980f4ccd302",
			want:      `label[openchoreo_dev_namespace]:"default" + label[openchoreo_dev_environment_uid]:"3d9d7f27-f0ab-4310-ae0b-4980f4ccd302"`,
		},
		{
			name:       "with project",
			namespace:  "default",
			envUID:     "3d9d7f27-f0ab-4310-ae0b-4980f4ccd302",
			projectUID: "74bd2a7e-4277-4982-9d16-8a41b979a55c",
			want:       `label[openchoreo_dev_namespace]:"default" + label[openchoreo_dev_environment_uid]:"3d9d7f27-f0ab-4310-ae0b-4980f4ccd302" + label[openchoreo_dev_project_uid]:"74bd2a7e-4277-4982-9d16-8a41b979a55c"`,
		},
		{
			name:         "with project and component",
			namespace:    "default",
			envUID:       "3d9d7f27-f0ab-4310-ae0b-4980f4ccd302",
			projectUID:   "74bd2a7e-4277-4982-9d16-8a41b979a55c",
			componentUID: "14e6fe2a-820a-481e-9a96-018e86a241fa",
			want:         `label[openchoreo_dev_namespace]:"default" + label[openchoreo_dev_environment_uid]:"3d9d7f27-f0ab-4310-ae0b-4980f4ccd302" + label[openchoreo_dev_project_uid]:"74bd2a7e-4277-4982-9d16-8a41b979a55c" + label[openchoreo_dev_component_uid]:"14e6fe2a-820a-481e-9a96-018e86a241fa"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildFilter(tt.namespace, tt.envUID, tt.projectUID, tt.componentUID)
			if got != tt.want {
				t.Errorf("BuildFilter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildWindow(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)
	got := BuildWindow(start, end)
	want := "2026-07-01T00:00:00Z,2026-07-02T12:30:00Z"
	if got != want {
		t.Errorf("BuildWindow() = %q, want %q", got, want)
	}
}

func TestNormalizeGranularity(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: "1h", want: "1h"},
		{in: "6h", want: "6h"},
		{in: "2d", want: "2d"},
		{in: "3w", want: "21d"},
		{in: "1w", want: "7d"},
		{in: "0d", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "5x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeGranularity(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeGranularity(%q) expected error, got %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeGranularity(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeGranularity(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
