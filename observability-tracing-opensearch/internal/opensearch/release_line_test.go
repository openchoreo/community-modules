// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTraceQueries_ReleaseLine(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	want := `"resource.openchoreo.dev/release-line-uid":"` + lineUID + `"`
	builders := map[string]func(TracesRequestParams) map[string]interface{}{
		"spans":  BuildTracesQuery,
		"traces": BuildTracesAggregationQuery,
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			params := TracesRequestParams{
				StartTime: "2026-01-01T00:00:00Z", EndTime: "2026-01-01T01:00:00Z",
				Namespace: "default", ComponentUIDs: []string{"comp-uid"}, Limit: 10,
			}
			b, _ := json.Marshal(build(params))
			if got := string(b); !strings.Contains(got, `"resource.openchoreo.dev/release-line-component-uid":["comp-uid"]`) {
				t.Errorf("a component query must cover its release lines' spans: %s", got)
			}

			params.MainLineOnly = true
			b, _ = json.Marshal(build(params))
			if got := string(b); !strings.Contains(got, `"must_not":[{"exists":{"field":"resource.openchoreo.dev/release-line-uid"}}]`) ||
				strings.Contains(got, "release-line-component-uid") {
				t.Errorf("a main line query must exclude release line spans: %s", got)
			}

			params.ReleaseLineUID = lineUID
			b, _ = json.Marshal(build(params))
			if got := string(b); !strings.Contains(got, want) || strings.Contains(got, "component-uid") {
				t.Errorf("a release line query must match %s alone: %s", want, got)
			}
		})
	}
}

// A release line's spans are attributed to their component like the main line's.
func TestParseSpanEntry_ReleaseLineComponentUID(t *testing.T) {
	const componentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
	for name, resource := range map[string]map[string]interface{}{
		"main line":    {"openchoreo.dev/component-uid": componentUID},
		"release line": {"openchoreo.dev/release-line-component-uid": componentUID, "openchoreo.dev/release-line-uid": "93f65294-926f-475b-98f1-dc4f152f38da"},
	} {
		t.Run(name, func(t *testing.T) {
			span := ParseSpanEntry(Hit{Source: map[string]interface{}{"resource": resource}})
			if span.OpenChoreoComponentUID != componentUID {
				t.Errorf("span component UID = %q, want %q", span.OpenChoreoComponentUID, componentUID)
			}
		})
	}
}
