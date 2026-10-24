// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const testReleaseLineID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestReleaseLineFilter(t *testing.T) {
	params := TracesQueryParams{
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		TraceID:   "trace-1",
		Scope:     Scope{Namespace: "default", ComponentID: "comp-uid"},
	}
	want := "service_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "'"

	for name, gen := range map[string]func(TracesQueryParams, string, *slog.Logger) ([]byte, error){
		"traces list":  generateTracesListQuery,
		"traces count": generateTracesCountQuery,
		"spans list":   generateSpansListQuery,
		"spans count":  generateSpansCountQuery,
	} {
		t.Run(name, func(t *testing.T) {
			sqlFor := func(p TracesQueryParams) string {
				raw, err := gen(p, "default", testLogger())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				var query struct {
					Query struct {
						SQL string `json:"sql"`
					} `json:"query"`
				}
				if err := json.Unmarshal(raw, &query); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				return query.Query.SQL
			}

			if sql := sqlFor(params); strings.Contains(sql, "release_line_uid") {
				t.Errorf("query without a release line filters on one: %s", sql)
			}
			p := params
			p.Scope.ReleaseLineID = testReleaseLineID
			if sql := sqlFor(p); !strings.Contains(sql, want) {
				t.Errorf("missing release line condition %q in %s", want, sql)
			}
		})
	}
}
