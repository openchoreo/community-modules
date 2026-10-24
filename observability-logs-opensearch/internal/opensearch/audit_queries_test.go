// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// auditFilterClauses pulls the bool/filter array out of a built query.
func auditFilterClauses(t *testing.T, query map[string]interface{}) []map[string]interface{} {
	t.Helper()
	boolQuery, ok := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
	if !ok {
		t.Fatalf("query has no bool clause: %v", query)
	}
	clauses, ok := boolQuery["filter"].([]map[string]interface{})
	if !ok {
		t.Fatalf("bool clause has no filter array: %v", boolQuery)
	}
	return clauses
}

func TestBuildAuditLogsQuery_MapsEveryFilterOntoItsField(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")

	query := qb.BuildAuditLogsQuery(AuditLogsQueryParams{
		StartTime:            "2026-09-01T00:00:00Z",
		EndTime:              "2026-09-02T00:00:00Z",
		ActorIDs:             []string{"user-1", "user-2"},
		ActorTypes:           []string{"user"},
		ActorIssuers:         []string{"https://idp.example"},
		ActorSessionIDs:      []string{"sid-1"},
		ActorEntitlements:    []string{"admins"},
		ResourceTypes:        []string{"Project"},
		ResourceNamespaces:   []string{"default"},
		ResourceEnvironments: []string{"default/production"},
		ResourceProjects:     []string{"checkout"},
		ResourceComponents:   []string{"api"},
		ResourceReleaseLines: []string{"next"},
		ResourceResources:    []string{"orders-db"},
		ResourceNames:        []string{"checkout-api"},
		ReturnedItemNames:    []string{"db-creds", "api-key"},
		Actions:              []string{"create_project"},
		Categories:           []string{"management"},
		Results:              []string{"success"},
		Producers:            []string{"openchoreo-api"},
		Surfaces:             []string{"rest"},
		OperationIDs:         []string{"CreateProject"},
		RequestIDs:           []string{"req-1"},
		EventIDs:             []string{"evt-1"},
		SourceIPs:            []string{"10.0.0.1"},
		UserAgents:           []string{"occ/1.2.3"},
		Limit:                50,
		SortOrder:            "asc",
	})

	if query["size"] != 50 {
		t.Errorf("size = %v, want 50", query["size"])
	}
	// The contract gives no field in which to mark a count truncated.
	if query["track_total_hits"] != true {
		t.Errorf("track_total_hits = %v, want true", query["track_total_hits"])
	}

	clauses := auditFilterClauses(t, query)

	for _, tc := range []struct {
		field string
		want  []string
	}{
		{"actor.id", []string{"user-1", "user-2"}},
		{"actor.type", []string{"user"}},
		{"actor.issuer", []string{"https://idp.example"}},
		{"actor.session_id", []string{"sid-1"}},
		{AuditEntitlementValuesField, []string{"admins"}},
		{"resource.type", []string{"Project"}},
		{"resource.namespace", []string{"default"}},
		{"resource.environment", []string{"default/production"}},
		{"resource.project", []string{"checkout"}},
		{"resource.component", []string{"api"}},
		{"resource.release_line", []string{"next"}},
		{"resource.resource", []string{"orders-db"}},
		{"resource.name", []string{"checkout-api"}},
		{"returned.items.name", []string{"db-creds", "api-key"}},
		{"action", []string{"create_project"}},
		{"category", []string{"management"}},
		{"result", []string{"success"}},
		{"producer", []string{"openchoreo-api"}},
		{"surface", []string{"rest"}},
		{"operation_id", []string{"CreateProject"}},
		{"request_id", []string{"req-1"}},
		{AuditEventIDField, []string{"evt-1"}},
		{"source_ip", []string{"10.0.0.1"}},
		{"user_agent", []string{"occ/1.2.3"}},
	} {
		got := findClause(clauses, "terms", tc.field)
		if got == nil {
			t.Errorf("no terms clause for %s", tc.field)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.field, got, tc.want)
		}
	}
}

// @timestamp would select by when the collector read the line, which drifts whenever
// collection is backed up.
func TestBuildAuditLogsQuery_RangesOnEventTime(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")

	query := qb.BuildAuditLogsQuery(AuditLogsQueryParams{
		StartTime: "2026-09-01T00:00:00Z",
		EndTime:   "2026-09-02T00:00:00Z",
	})

	clauses := auditFilterClauses(t, query)
	if findClause(clauses, "range", "@timestamp") != nil {
		t.Error("query ranges on @timestamp; it must range on event_time")
	}

	rangeClause, ok := findClause(clauses, "range", AuditEventTimeField).(map[string]interface{})
	if !ok {
		t.Fatalf("no range clause on %s: %v", AuditEventTimeField, clauses)
	}
	// startTime is inclusive and endTime exclusive, as the contract states.
	if rangeClause["gte"] != "2026-09-01T00:00:00Z" {
		t.Errorf("gte = %v, want inclusive start", rangeClause["gte"])
	}
	if rangeClause["lt"] != "2026-09-02T00:00:00Z" {
		t.Errorf("lt = %v, want exclusive end", rangeClause["lt"])
	}
}

func TestBuildAuditLogsQuery_OmitsEmptyFilters(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")

	query := qb.BuildAuditLogsQuery(AuditLogsQueryParams{
		StartTime: "2026-09-01T00:00:00Z",
		EndTime:   "2026-09-02T00:00:00Z",
		Actions:   []string{},
		Producers: nil,
	})

	clauses := auditFilterClauses(t, query)
	if len(clauses) != 1 {
		t.Errorf("got %d clauses, want only the time range: %v", len(clauses), clauses)
	}
}

// Without a tiebreaker, two identical queries can disagree on which record the limit
// cuts off.
func TestBuildAuditLogsQuery_SortsWithATiebreaker(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")

	query := qb.BuildAuditLogsQuery(AuditLogsQueryParams{
		StartTime: "2026-09-01T00:00:00Z",
		EndTime:   "2026-09-02T00:00:00Z",
		SortOrder: "desc",
	})

	sort, ok := query["sort"].([]map[string]interface{})
	if !ok || len(sort) != 2 {
		t.Fatalf("sort = %v, want two keys", query["sort"])
	}
	if _, ok := sort[0][AuditEventTimeField]; !ok {
		t.Errorf("first sort key = %v, want %s", sort[0], AuditEventTimeField)
	}
	if _, ok := sort[1][AuditEventIDField]; !ok {
		t.Errorf("second sort key = %v, want %s", sort[1], AuditEventIDField)
	}
}

// A year day-walked is ~8KB of request line against OpenSearch's 4KB default, which
// fails as a malformed request rather than a length error.
func TestAuditIndexPattern_IsAWildcardNotADayWalk(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")

	if got := qb.AuditIndexPattern(); got != "audit-logs-*" {
		t.Errorf("AuditIndexPattern() = %q, want audit-logs-*", got)
	}
}

// Rejecting would leave a caller who asked for 1m over a year with no timeline at all.
func TestResolveTimelineInterval_CoarsensRatherThanRejects(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name      string
		requested string
		end       time.Time
		want      string
	}{
		{"honours a width that fits", "15m", start.Add(24 * time.Hour), "15m"},
		{"coarsens a year at 1m", "1m", start.AddDate(1, 0, 0), "1d"},
		// Ten days is 240 hours, under the cap, so 1m coarsens exactly one step.
		{"coarsens one step when that is enough", "1m", start.Add(10 * 24 * time.Hour), "1h"},
		// A month is 744 hours, over the cap, so hours are not enough.
		{"coarsens past hours when they do not fit", "1m", start.AddDate(0, 1, 0), "1d"},
		{"chooses one when none is asked for", "", start.Add(24 * time.Hour), "24m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveTimelineInterval(tc.requested, start, tc.end)
			if err != nil {
				t.Fatalf("ResolveTimelineInterval() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveTimelineInterval(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

func TestResolveTimelineInterval_StaysUnderTheBucketCap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(5, 0, 0)

	got, err := ResolveTimelineInterval("1m", start, end)
	if err != nil {
		t.Fatalf("ResolveTimelineInterval() error = %v", err)
	}

	width, err := parseTimelineInterval(got)
	if err != nil {
		t.Fatalf("resolved interval %q does not parse: %v", got, err)
	}
	if buckets := end.Sub(start) / width; buckets > maxTimelineBuckets {
		t.Errorf("interval %q yields %d buckets, want at most %d", got, buckets, maxTimelineBuckets)
	}
}

// A window one instant longer than maxTimelineBuckets whole intervals spills into one
// more bucket, which a floor division does not see.
func TestResolveTimelineInterval_CountsThePartialBucket(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(maxTimelineBuckets*time.Minute + time.Nanosecond)

	got, err := ResolveTimelineInterval("1m", start, end)
	if err != nil {
		t.Fatalf("ResolveTimelineInterval() error = %v", err)
	}
	if got == "1m" {
		t.Errorf("interval = %q, which yields %d buckets, want a coarser one",
			got, maxTimelineBuckets+1)
	}
}

func TestResolveTimelineInterval_RejectsAnUnusableWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, err := ResolveTimelineInterval("15m", start, start); err == nil {
		t.Error("expected an error for a zero-length window, got nil")
	}
	if _, err := ResolveTimelineInterval("bogus", start, start.Add(time.Hour)); err == nil {
		t.Error("expected an error for an unparseable interval, got nil")
	}
}

// A sparse array would chart straight across a gap in activity, reading quiet as busy.
func TestBuildAuditTimelineAgg_ZeroFillsTheWindow(t *testing.T) {
	agg := BuildAuditTimelineAgg("15m", "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z")

	histogram, ok := agg["timeline"].(map[string]interface{})["date_histogram"].(map[string]interface{})
	if !ok {
		t.Fatalf("aggregation has no date_histogram: %v", agg)
	}
	if histogram["min_doc_count"] != 0 {
		t.Errorf("min_doc_count = %v, want 0 so empty buckets are present", histogram["min_doc_count"])
	}
	bounds, ok := histogram["extended_bounds"].(map[string]interface{})
	if !ok {
		t.Fatalf("date_histogram has no extended_bounds: %v", histogram)
	}
	if bounds["min"] != "2026-09-01T00:00:00Z" || bounds["max"] != "2026-09-02T00:00:00Z" {
		t.Errorf("extended_bounds = %v, want the query window", bounds)
	}
	if histogram["field"] != AuditEventTimeField {
		t.Errorf("field = %v, want %s", histogram["field"], AuditEventTimeField)
	}
}

// Lucene regex has no case-insensitivity flag. A lowercase normalizer sub-field would
// return lowercased values, which the contract does not allow.
func TestValueSearchRegex_IsCaseInsensitiveSubstring(t *testing.T) {
	got, ok := valueSearchRegex("cli")
	if !ok {
		t.Fatal("valueSearchRegex() declined a short search")
	}
	if got != ".*[cC][lL][iI].*" {
		t.Errorf("valueSearchRegex(cli) = %q", got)
	}
}

func TestValueSearchRegex_EscapesRegexSyntax(t *testing.T) {
	got, ok := valueSearchRegex("a.b")
	if !ok {
		t.Fatal("valueSearchRegex() declined a short search")
	}
	if !strings.Contains(got, `\.`) {
		t.Errorf("valueSearchRegex(a.b) = %q, want the dot escaped", got)
	}
}

// The engine rejects a regex over index.max_regex_length outright, so it is applied in
// Go instead.
func TestValueSearchRegex_DeclinesWhatTheEngineWouldReject(t *testing.T) {
	if _, ok := valueSearchRegex(strings.Repeat("a", 256)); ok {
		t.Error("valueSearchRegex() accepted a search that exceeds the regex length limit")
	}
	if _, ok := valueSearchRegex(""); ok {
		t.Error("valueSearchRegex() built a regex for an empty search")
	}
}

func TestFilterValuesBySearch_MatchesCaseInsensitively(t *testing.T) {
	values := []AuditFilterValue{
		{Value: "occ/1.2.3", Count: 10},
		{Value: "Mozilla/5.0", Count: 4},
	}

	got := FilterValuesBySearch(values, "OCC")
	if len(got) != 1 || got[0].Value != "occ/1.2.3" {
		t.Errorf("FilterValuesBySearch() = %v, want only occ/1.2.3", got)
	}

	if len(FilterValuesBySearch(values, "")) != 2 {
		t.Error("an empty search must not filter anything out")
	}
}

func TestBuildAuditFilterValuesQuery_OrdersByCountThenValue(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")

	query := qb.BuildAuditFilterValuesQuery(AuditLogsQueryParams{
		StartTime: "2026-09-01T00:00:00Z",
		EndTime:   "2026-09-02T00:00:00Z",
	}, "actor.id", "", 25)

	if query["size"] != 0 {
		t.Errorf("size = %v, want 0; only the aggregation is wanted", query["size"])
	}

	terms, ok := query["aggs"].(map[string]interface{})["values"].(map[string]interface{})["terms"].(map[string]interface{})
	if !ok {
		t.Fatalf("query has no values terms aggregation: %v", query)
	}
	if terms["field"] != "actor.id" || terms["size"] != 25 {
		t.Errorf("terms = %v, want actor.id sized 25", terms)
	}

	// Busiest first, then by value, so a truncated list is the useful end of it.
	order, ok := terms["order"].([]map[string]string)
	if !ok || len(order) != 2 {
		t.Fatalf("terms order = %v, want two keys", terms["order"])
	}
	if order[0]["_count"] != "desc" || order[1]["_key"] != "asc" {
		t.Errorf("terms order = %v, want _count desc then _key asc", order)
	}
}

// Counting the whole field would report thousands behind a search that narrowed to three.
func TestBuildAuditFilterValuesQuery_CountsOnlyMatchingValues(t *testing.T) {
	qb := NewQueryBuilder("audit-logs-")
	params := AuditLogsQueryParams{
		StartTime: "2026-09-01T00:00:00Z",
		EndTime:   "2026-09-02T00:00:00Z",
	}

	withSearch := qb.BuildAuditFilterValuesQuery(params, "actor.id", "admin", 25)
	total, ok := withSearch["aggs"].(map[string]interface{})["total_values"].(map[string]interface{})
	if !ok {
		t.Fatalf("query has no total_values aggregation: %v", withSearch)
	}
	scope, ok := total["filter"].(map[string]interface{})["regexp"].(map[string]interface{})
	if !ok {
		t.Fatalf("total_values is not scoped to the search: %v", total)
	}
	terms := withSearch["aggs"].(map[string]interface{})["values"].(map[string]interface{})["terms"].(map[string]interface{})
	if scope["actor.id"] != terms["include"] {
		t.Errorf("total_values scope = %v, want the buckets' include %v", scope["actor.id"], terms["include"])
	}

	// Without a search there is nothing to narrow by, and every value counts.
	noSearch := qb.BuildAuditFilterValuesQuery(params, "actor.id", "", 25)
	total = noSearch["aggs"].(map[string]interface{})["total_values"].(map[string]interface{})
	if _, ok := total["filter"].(map[string]interface{})["match_all"]; !ok {
		t.Errorf("total_values filter = %v, want match_all", total["filter"])
	}
}

func TestParseAuditFilterValues_DropsValuesNoFilterWouldSelect(t *testing.T) {
	aggs := json.RawMessage(`{
		"values": {
			"buckets": [
				{"key": "user-1", "doc_count": 10},
				{"key": "", "doc_count": 3}
			],
			"sum_other_doc_count": 0
		},
		"total_values": {"doc_count": 13, "matching": {"value": 2}}
	}`)

	values, total, err := ParseAuditFilterValues(aggs)
	if err != nil {
		t.Fatalf("ParseAuditFilterValues() error = %v", err)
	}
	// No filter value would select an absent field, so it is not offered as a choice.
	if len(values) != 1 || values[0].Value != "user-1" {
		t.Errorf("values = %v, want only user-1", values)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

func TestParseAuditFilterValues_TotalIsNotThePageSize(t *testing.T) {
	aggs := json.RawMessage(`{
		"values": {
			"buckets": [{"key": "user-1", "doc_count": 10}]
		},
		"total_values": {"doc_count": 50000, "matching": {"value": 9000}}
	}`)

	values, total, err := ParseAuditFilterValues(aggs)
	if err != nil {
		t.Fatalf("ParseAuditFilterValues() error = %v", err)
	}
	if len(values) != 1 {
		t.Errorf("values = %v, want the single returned bucket", values)
	}
	if total != 9000 {
		t.Errorf("totalValues = %d, want 9000", total)
	}
}

func TestParseAuditTimeline_KeepsEmptyBuckets(t *testing.T) {
	aggs := json.RawMessage(`{
		"timeline": {
			"buckets": [
				{
					"key_as_string": "2026-09-01T00:00:00.000Z",
					"doc_count": 2,
					"results": {"buckets": [{"key": "success", "doc_count": 2}]}
				},
				{
					"key_as_string": "2026-09-01T00:15:00.000Z",
					"doc_count": 0,
					"results": {"buckets": []}
				}
			]
		}
	}`)

	timeline, err := ParseAuditTimeline(aggs, "15m")
	if err != nil {
		t.Fatalf("ParseAuditTimeline() error = %v", err)
	}
	if timeline == nil {
		t.Fatal("ParseAuditTimeline() = nil, want a timeline")
	}
	if len(timeline.Buckets) != 2 {
		t.Fatalf("got %d buckets, want 2 including the empty one", len(timeline.Buckets))
	}
	if timeline.Buckets[1].Total != 0 {
		t.Errorf("empty bucket total = %d, want 0", timeline.Buckets[1].Total)
	}
	if timeline.Interval != "15m" {
		t.Errorf("interval = %q, want the width actually used", timeline.Interval)
	}
}

// An omitted timeline differs from one reporting no activity, so nil must not become an
// empty struct.
func TestParseAuditTimeline_ReturnsNilWhenAbsent(t *testing.T) {
	timeline, err := ParseAuditTimeline(nil, "15m")
	if err != nil {
		t.Fatalf("ParseAuditTimeline() error = %v", err)
	}
	if timeline != nil {
		t.Errorf("ParseAuditTimeline(nil) = %v, want nil", timeline)
	}

	timeline, err = ParseAuditTimeline(json.RawMessage(`{"other": {}}`), "15m")
	if err != nil {
		t.Fatalf("ParseAuditTimeline() error = %v", err)
	}
	if timeline != nil {
		t.Errorf("ParseAuditTimeline() = %v, want nil when no timeline aggregation is present", timeline)
	}
}
