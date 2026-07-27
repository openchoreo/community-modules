// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import "testing"

func TestBuildScopeFilter(t *testing.T) {
	tests := []struct {
		name    string
		p       TracesParams
		want    string
		wantErr bool
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
				ComponentUID:   "a38a0603-bb5a-4b13-b326-3b831628c3fb",
				ProjectUID:     "9ebd1695-3a3d-4917-a869-9f375d7cf361",
				EnvironmentUID: "988c594d-a2f7-43ec-9a33-27b9c6e79b30",
			},
			want: "+openchoreo.dev/namespace:default " +
				"+openchoreo.dev/component-uid:a38a0603-bb5a-4b13-b326-3b831628c3fb " +
				"+openchoreo.dev/project-uid:9ebd1695-3a3d-4917-a869-9f375d7cf361 " +
				"+openchoreo.dev/environment-uid:988c594d-a2f7-43ec-9a33-27b9c6e79b30",
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
			name:    "filter metacharacters are rejected",
			p:       TracesParams{Namespace: "default +span:evil ^root"},
			wantErr: true,
		},
		{
			name:    "whitespace in value is rejected",
			p:       TracesParams{Namespace: "prod dev"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildScopeFilter(tt.p)
			if tt.wantErr {
				if err == nil {
					t.Errorf("BuildScopeFilter() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildScopeFilter() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("BuildScopeFilter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchesScope(t *testing.T) {
	const (
		componentUID   = "a38a0603-bb5a-4b13-b326-3b831628c3fb"
		projectUID     = "9ebd1695-3a3d-4917-a869-9f375d7cf361"
		environmentUID = "988c594d-a2f7-43ec-9a33-27b9c6e79b30"
		otherComponent = "11111111-2222-3333-4444-555555555555"
	)
	labels := map[string]string{
		LabelNamespace:      "default",
		LabelComponentUID:   componentUID,
		LabelProjectUID:     projectUID,
		LabelEnvironmentUID: environmentUID,
	}

	tests := []struct {
		name string
		p    TracesParams
		want bool
	}{
		{"namespace match", TracesParams{Namespace: "default"}, true},
		{"namespace mismatch", TracesParams{Namespace: "other"}, false},
		{"full scope match", TracesParams{Namespace: "default", ComponentUID: componentUID, ProjectUID: projectUID, EnvironmentUID: environmentUID}, true},
		{"component mismatch", TracesParams{Namespace: "default", ComponentUID: otherComponent}, false},
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

func TestValidTraceID(t *testing.T) {
	valid := []string{
		"0123456789abcdef0123456789abcdef",
		"ABCDEF01234567890000000000000001",
	}
	for _, s := range valid {
		if !ValidTraceID(s) {
			t.Errorf("ValidTraceID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                                  // empty
		"abc123",                            // too short
		"0123456789abcdef0123456789abcde",   // 31 chars
		"0123456789abcdef0123456789abcdef0", // 33 chars
		"00000000000000000000000000000000",  // all zero
		"0123456789abcdef0123456789abcdeg",  // non-hex
		"0123456789abcdef 123456789abcdef",  // whitespace
	}
	for _, s := range invalid {
		if ValidTraceID(s) {
			t.Errorf("ValidTraceID(%q) = true, want false", s)
		}
	}
}

func TestValidSpanID(t *testing.T) {
	valid := []string{"0000000000000abc", "abc123", "ABCDEF0123456789", "0f"}
	for _, s := range valid {
		if !ValidSpanID(s) {
			t.Errorf("ValidSpanID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                  // empty
		"0000000000000000",  // all zero
		"0123456789abcdef0", // 17 chars, too long
		"0x1f",              // non-hex
		"abc 123",           // whitespace
	}
	for _, s := range invalid {
		if ValidSpanID(s) {
			t.Errorf("ValidSpanID(%q) = true, want false", s)
		}
	}
}
