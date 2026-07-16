// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import "testing"

func TestBuildScopeFilter(t *testing.T) {
	tests := []struct {
		name string
		p    TracesParams
		want string
	}{
		{
			name: "namespace only",
			p:    TracesParams{Namespace: "default"},
			want: "+openchoreo.dev/namespace:default",
		},
		{
			name: "all scope fields",
			p: TracesParams{
				Namespace:      "default",
				ComponentUID:   "c1a2",
				ProjectUID:     "p3b4",
				EnvironmentUID: "e5c6",
			},
			want: "+openchoreo.dev/namespace:default " +
				"+openchoreo.dev/component-uid:c1a2 " +
				"+openchoreo.dev/project-uid:p3b4 " +
				"+openchoreo.dev/environment-uid:e5c6",
		},
		{
			name: "zero uuid is not a term",
			p: TracesParams{
				Namespace:    "default",
				ComponentUID: zeroUUID,
			},
			want: "+openchoreo.dev/namespace:default",
		},
		{
			name: "filter metacharacters are stripped from values",
			p:    TracesParams{Namespace: "default +span:evil ^root"},
			want: "+openchoreo.dev/namespace:defaultspanevilroot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildScopeFilter(tt.p); got != tt.want {
				t.Errorf("BuildScopeFilter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchesScope(t *testing.T) {
	labels := map[string]string{
		LabelNamespace:      "default",
		LabelComponentUID:   "c1",
		LabelProjectUID:     "p1",
		LabelEnvironmentUID: "e1",
	}

	tests := []struct {
		name string
		p    TracesParams
		want bool
	}{
		{"namespace match", TracesParams{Namespace: "default"}, true},
		{"namespace mismatch", TracesParams{Namespace: "other"}, false},
		{"full scope match", TracesParams{Namespace: "default", ComponentUID: "c1", ProjectUID: "p1", EnvironmentUID: "e1"}, true},
		{"component mismatch", TracesParams{Namespace: "default", ComponentUID: "c2"}, false},
		{"zero uuid ignored", TracesParams{Namespace: "default", ComponentUID: zeroUUID}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesScope(labels, tt.p); got != tt.want {
				t.Errorf("matchesScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidID(t *testing.T) {
	valid := []string{"abc123", "ABCDEF0123456789", "0f"}
	for _, s := range valid {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "xyz", "abc-123", "abc 123", "0x1f"}
	for _, s := range invalid {
		if ValidID(s) {
			t.Errorf("ValidID(%q) = true, want false", s)
		}
	}
}
