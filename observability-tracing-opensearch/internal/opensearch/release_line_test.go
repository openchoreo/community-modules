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
			if strings.Contains(string(b), "release-line-uid") {
				t.Errorf("query without a release line filters on one: %s", b)
			}

			params.ReleaseLineUID = lineUID
			b, _ = json.Marshal(build(params))
			if !strings.Contains(string(b), want) {
				t.Errorf("missing release line term %s in %s", want, b)
			}
		})
	}
}
