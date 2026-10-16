// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"strings"
	"testing"
	"time"
)

const auditTestTable = "OpenChoreoAuditLog_CL"

func auditTestParams() AuditParams {
	return AuditParams{
		StartTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Limit:     50,
	}
}

func TestBuildAuditLogsKQL_WindowIsHalfOpen(t *testing.T) {
	kql := BuildAuditLogsKQL(auditTestParams(), auditTestTable)

	if !strings.Contains(kql, "TimeGenerated >= datetime(2026-09-01T00:00:00.0000000Z)") {
		t.Errorf("start bound missing or not inclusive:\n%s", kql)
	}
	if !strings.Contains(kql, "TimeGenerated < datetime(2026-09-02T00:00:00.0000000Z)") {
		t.Errorf("end bound missing or not exclusive:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_SortsWithEventIDTiebreaker(t *testing.T) {
	p := auditTestParams()
	p.SortOrder = SortAsc

	kql := BuildAuditLogsKQL(p, auditTestTable)

	// Sorted before taking, so the page is deterministic. top would be
	// equivalent but accepts only one sort expression.
	if !strings.Contains(kql, "| sort by TimeGenerated asc, EventId asc\n| take 50") {
		t.Errorf("expected a stable sort with an EventId tiebreaker:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_ClampsLimit(t *testing.T) {
	p := auditTestParams()
	p.Limit = 99999

	if kql := BuildAuditLogsKQL(p, auditTestTable); !strings.Contains(kql, "| take 1000") {
		t.Errorf("expected the limit clamped to %d:\n%s", MaxAuditLimit, kql)
	}
}

func TestBuildAuditLogsKQL_CountsTotalOverTheWindowNotThePage(t *testing.T) {
	kql := BuildAuditLogsKQL(auditTestParams(), auditTestTable)

	// Counting the page would make total == len(records) and stop paging early.
	if !strings.Contains(kql, "Base\n| summarize Total = count()") {
		t.Errorf("expected Total counted over Base:\n%s", kql)
	}
}

// Matching the events and platform builders, which both end without one.
func TestBuildAuditLogsKQL_HasNoTrailingSemicolon(t *testing.T) {
	for _, timeline := range []bool{false, true} {
		p := auditTestParams()
		p.IncludeTimeline = timeline

		if kql := BuildAuditLogsKQL(p, auditTestTable); strings.HasSuffix(strings.TrimSpace(kql), ";") {
			t.Errorf("includeTimeline=%v: query ends with a semicolon:\n%s", timeline, kql)
		}
	}
}

// A row dropped only at mapping time still counts towards Total and the
// timeline, so a page of 9 would report a total of 10.
func TestBuildAuditLogsKQL_ExcludesUnusableRowsFromEveryStatement(t *testing.T) {
	p := auditTestParams()
	p.IncludeTimeline = true
	kql := BuildAuditLogsKQL(p, auditTestTable)

	base, _, found := strings.Cut(kql, ");\n")
	if !found {
		t.Fatalf("could not isolate the Base statement:\n%s", kql)
	}

	for _, col := range auditIdentityColumns {
		if !strings.Contains(base, "| where isnotempty("+col+")") {
			t.Errorf("Base does not exclude rows with an empty %s:\n%s", col, base)
		}
	}
	if !strings.Contains(base, "| where isnotnull(EventTime)") {
		t.Errorf("Base does not exclude rows with no event time:\n%s", base)
	}
}

func TestBuildAuditLogsKQL_MaterialisesBase(t *testing.T) {
	p := auditTestParams()
	p.IncludeTimeline = true

	// A plain let would re-scan the window once per statement.
	if kql := BuildAuditLogsKQL(p, auditTestTable); !strings.Contains(kql, "let Base = materialize(") {
		t.Errorf("expected Base materialised:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_OmitsTimelineUnlessAsked(t *testing.T) {
	if kql := BuildAuditLogsKQL(auditTestParams(), auditTestTable); strings.Contains(kql, "bin_at(") {
		t.Errorf("did not expect a timeline statement:\n%s", kql)
	}

	p := auditTestParams()
	p.IncludeTimeline = true
	p.TimelineInterval = "1h"
	kql := BuildAuditLogsKQL(p, auditTestTable)
	if !strings.Contains(kql, "by BucketStart = bin_at(TimeGenerated, 1h, ") ||
		!strings.Contains(kql, "), Result") {
		t.Errorf("expected a timeline grouped by bucket and result:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_SearchPhraseScansTheRawRecord(t *testing.T) {
	p := auditTestParams()
	p.SearchPhrase = `say "hi"`

	kql := BuildAuditLogsKQL(p, auditTestTable)

	if !strings.Contains(kql, `| where RawRecord contains "say \"hi\""`) {
		t.Errorf("expected the phrase quoted and matched against the raw line:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_MapsEveryFilterToItsColumn(t *testing.T) {
	p := auditTestParams()
	p.Actions = []string{"create_project"}
	p.Categories = []string{"management"}
	p.Results = []string{"denied"}
	p.SourceIPs = []string{"10.0.0.1"}
	p.UserAgents = []string{"occ/1.3"}
	p.Producers = []string{"openchoreo-api"}
	p.Surfaces = []string{"rest"}
	p.OperationIDs = []string{"CreateProject"}
	p.EventIDs = []string{"01920000-0000-7000-8000-000000000000"}
	p.RequestIDs = []string{"req-1"}
	p.ActorTypes = []string{"user"}
	p.ActorIDs = []string{"alice"}
	p.ActorIssuers = []string{"https://idp.example"}
	p.ActorSessionIDs = []string{"sess-1"}
	p.ResourceTypes = []string{"Project"}
	p.ResourceNamespaces = []string{"acme"}
	p.ResourceEnvironments = []string{"acme/dev"}
	p.ResourceProjects = []string{"shop"}
	p.ResourceComponents = []string{"api"}
	p.ResourceResources = []string{"res"}
	p.ResourceNames = []string{"name"}

	kql := BuildAuditLogsKQL(p, auditTestTable)

	for _, want := range []string{
		`| where Action in ("create_project")`,
		`| where Category in ("management")`,
		`| where Result in ("denied")`,
		`| where SourceIP in ("10.0.0.1")`,
		`| where UserAgent in ("occ/1.3")`,
		`| where Producer in ("openchoreo-api")`,
		`| where Surface in ("rest")`,
		`| where OperationId in ("CreateProject")`,
		`| where EventId in ("01920000-0000-7000-8000-000000000000")`,
		`| where RequestId in ("req-1")`,
		`| where ActorType in ("user")`,
		`| where ActorId in ("alice")`,
		`| where ActorIssuer in ("https://idp.example")`,
		`| where ActorSessionId in ("sess-1")`,
		`| where ResourceType in ("Project")`,
		`| where ResourceNamespace in ("acme")`,
		`| where ResourceEnvironment in ("acme/dev")`,
		`| where ResourceProject in ("shop")`,
		`| where ResourceComponent in ("api")`,
		`| where ResourceResource in ("res")`,
		`| where ResourceName in ("name")`,
	} {
		if !strings.Contains(kql, want) {
			t.Errorf("missing filter %q in:\n%s", want, kql)
		}
	}
}

func TestBuildAuditLogsKQL_EmptyFiltersDoNotNarrow(t *testing.T) {
	p := auditTestParams()
	p.Actions = []string{}
	p.ActorIDs = nil

	kql := BuildAuditLogsKQL(p, auditTestTable)

	// `in ()` would match nothing, silently returning no records.
	if strings.Contains(kql, "in ()") {
		t.Errorf("an empty filter must not narrow the query:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_MatchesEntitlementsAcrossEveryClaim(t *testing.T) {
	p := auditTestParams()
	p.ActorEntitlements = []string{"admin"}

	kql := BuildAuditLogsKQL(p, auditTestTable)

	// Walked by key: naming one claim would miss subjects entitled under another.
	if !strings.Contains(kql, "mv-apply _entKey = bag_keys(ActorEntitlements)") {
		t.Errorf("expected the entitlements bag to be walked by key:\n%s", kql)
	}
	// Whole-element, so "admin" cannot match a record entitled to "admin-readonly".
	if !strings.Contains(kql, `set_has_element(ActorEntitlements[_entKey], "admin")`) {
		t.Errorf("expected a whole-element entitlement match:\n%s", kql)
	}
	// bag_keys returns null rather than [] for a null bag.
	if !strings.Contains(kql, "| where isnotnull(ActorEntitlements)") {
		t.Errorf("expected null bags to be dropped before the walk:\n%s", kql)
	}
}

// Without the summarize, mv-apply emits one row per claim and a record entitled
// under two claims would be paged twice and counted twice in Total.
func TestBuildAuditLogsKQL_EntitlementsFilterKeepsOneRowPerRecord(t *testing.T) {
	p := auditTestParams()
	p.ActorEntitlements = []string{"admin"}

	kql := BuildAuditLogsKQL(p, auditTestTable)

	if !strings.Contains(kql, "summarize _entMatch = max(iff(") {
		t.Errorf("expected the mv-apply subquery to collapse to one row:\n%s", kql)
	}
	if !strings.Contains(kql, "| where _entMatch == 1") {
		t.Errorf("expected the collapsed match to be filtered on:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_OrsEveryEntitlementValue(t *testing.T) {
	p := auditTestParams()
	p.ActorEntitlements = []string{"admin", "dev"}

	kql := BuildAuditLogsKQL(p, auditTestTable)

	want := `set_has_element(ActorEntitlements[_entKey], "admin") or ` +
		`set_has_element(ActorEntitlements[_entKey], "dev")`
	if !strings.Contains(kql, want) {
		t.Errorf("expected the values to be OR-ed within one walk:\n%s", kql)
	}
	// A second mv-apply would filter records carrying both, not either.
	if strings.Count(kql, "mv-apply _entKey") != 1 {
		t.Errorf("expected exactly one bag walk:\n%s", kql)
	}
}

func TestBuildAuditLogsKQL_KeepsRawRecordOutOfTheMaterialisedBase(t *testing.T) {
	p := auditTestParams()
	p.SearchPhrase = "denied"

	kql := BuildAuditLogsKQL(p, auditTestTable)
	base, _, _ := strings.Cut(kql, ");\n")

	// The filter reads RawRecord before the projection drops it.
	if !strings.Contains(base, "| where RawRecord contains") {
		t.Errorf("searchPhrase should still filter on RawRecord:\n%s", base)
	}
	if !strings.Contains(base, "| project ") {
		t.Fatalf("Base should project the columns it caches:\n%s", base)
	}
	projection := base[strings.LastIndex(base, "| project "):]
	if strings.Contains(projection, ColRawRecord) {
		t.Errorf("RawRecord should not be cached:\n%s", projection)
	}
}

func TestBuildAuditFilterValuesKQL_CachesOnlyTheCountedColumn(t *testing.T) {
	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:  auditTestParams(),
		Filter: AuditFilterActorID,
	}, auditTestTable)

	base, _, _ := strings.Cut(kql, ");\n")
	if !strings.Contains(base, "| project "+ColActorID) {
		t.Errorf("filter values should cache only the column it counts:\n%s", base)
	}
}

// bin's origin is undocumented, so the anchor is explicit.
func TestBuildAuditLogsKQL_AnchorsTimelineAtTheWindowStart(t *testing.T) {
	p := auditTestParams()
	p.IncludeTimeline = true
	p.TimelineInterval = "1h"

	kql := BuildAuditLogsKQL(p, auditTestTable)

	if !strings.Contains(kql, "bin_at(TimeGenerated, 1h, datetime(2026-09-01T00:00:00.0000000Z))") {
		t.Errorf("expected bin_at anchored at StartTime:\n%s", kql)
	}
}

func TestBuildAuditFilterValuesKQL_IgnoresTheNamedFiltersOwnSelections(t *testing.T) {
	q := auditTestParams()
	q.Actions = []string{"create_project"}
	q.Producers = []string{"openchoreo-api"}

	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:  q,
		Filter: AuditFilterAction,
	}, auditTestTable)

	// Otherwise the picker would only ever offer what is already selected.
	if strings.Contains(kql, `| where Action in (`) {
		t.Errorf("the named filter's own selections must be cleared:\n%s", kql)
	}
	if !strings.Contains(kql, `| where Producer in ("openchoreo-api")`) {
		t.Errorf("other filters must still narrow the values:\n%s", kql)
	}
}

func TestBuildAuditFilterValuesKQL_OrdersByCountThenValue(t *testing.T) {
	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:     auditTestParams(),
		Filter:    AuditFilterActorID,
		MaxValues: 25,
	}, auditTestTable)

	if !strings.Contains(kql, "| sort by _count desc, _value asc") {
		t.Errorf("expected busiest values first, ties broken by value:\n%s", kql)
	}
	if !strings.Contains(kql, "| take 25") {
		t.Errorf("expected maxValues honoured:\n%s", kql)
	}
	if !strings.Contains(kql, "| summarize TotalValues = dcount(_value)") {
		t.Errorf("expected the distinct-value total:\n%s", kql)
	}
}

func TestBuildAuditFilterValuesKQL_DropsEmptyValues(t *testing.T) {
	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:  auditTestParams(),
		Filter: AuditFilterProducer,
	}, auditTestTable)

	// No filter value would select an empty string, so offering one is noise.
	if !strings.Contains(kql, "| where isnotempty(_value)") {
		t.Errorf("expected empty values excluded:\n%s", kql)
	}
}

func TestBuildAuditFilterValuesKQL_ExpandsEntitlementsBeforeCounting(t *testing.T) {
	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:  auditTestParams(),
		Filter: AuditFilterActorEntitlements,
	}, auditTestTable)

	// Offering the bag whole, or one value per claim, would give the picker
	// entries no filter could select.
	if !strings.Contains(kql, "mv-expand _entKey = bag_keys(ActorEntitlements)") {
		t.Errorf("expected the entitlements bag to be walked by key:\n%s", kql)
	}
	if !strings.Contains(kql, "mv-expand _value = ActorEntitlements[_entKey]") {
		t.Errorf("expected each claim's values expanded individually:\n%s", kql)
	}
}

// The values a filter offers come from Base, so leaving the filter's own
// selection in place would offer only what is already picked.
func TestBuildAuditFilterValuesKQL_ClearsTheEntitlementsSelection(t *testing.T) {
	q := auditTestParams()
	q.ActorEntitlements = []string{"admin"}

	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:  q,
		Filter: AuditFilterActorEntitlements,
	}, auditTestTable)

	if strings.Contains(kql, "mv-apply _entKey") {
		t.Errorf("the entitlements filter must not narrow its own value list:\n%s", kql)
	}
}

func TestBuildAuditFilterValuesKQL_ClampsMaxValues(t *testing.T) {
	kql := BuildAuditFilterValuesKQL(AuditFilterValuesParams{
		Query:     auditTestParams(),
		Filter:    AuditFilterActorID,
		MaxValues: 99999,
	}, auditTestTable)

	if !strings.Contains(kql, "| take 1000") {
		t.Errorf("expected maxValues clamped to %d:\n%s", MaxAuditMaxValues, kql)
	}
}

func TestIsListableAuditFilter(t *testing.T) {
	if !IsListableAuditFilter(AuditFilterActorID) {
		t.Error("actor.id should be listable")
	}
	if IsListableAuditFilter(AuditFilter("event_id")) {
		t.Error("event_id is near-unique per record and must not be listable")
	}
}

func TestResolveTimelineInterval(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		end          time.Time
		requested    string
		wantInterval string
		wantKQL      string
	}{
		{"honours a width that fits", start.Add(24 * time.Hour), "1h", "1h", "1h"},
		{"coarsens minutes to hours", start.Add(7 * 24 * time.Hour), "1m", "1h", "1h"},
		{"keeps coarsening past hours when a week of them is still too many", start.Add(30 * 24 * time.Hour), "1m", "1d", "1d"},
		{"coarsens to days on a year", start.Add(365 * 24 * time.Hour), "1m", "1d", "1d"},
		{"renders weeks as 7d for KQL", start.Add(4 * 365 * 24 * time.Hour), "1m", "1w", "7d"},
		{"falls back on an unparseable width", start.Add(24 * time.Hour), "banana", "1h", "1h"},
		{"falls back on a zero count", start.Add(24 * time.Hour), "0h", "1h", "1h"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			interval, kql, width := ResolveTimelineInterval(start, tc.end, tc.requested)
			if interval != tc.wantInterval {
				t.Errorf("interval = %q, want %q", interval, tc.wantInterval)
			}
			if kql != tc.wantKQL {
				t.Errorf("kql = %q, want %q", kql, tc.wantKQL)
			}
			if width <= 0 {
				t.Errorf("width = %s, want a positive duration", width)
			}
		})
	}
}

func TestResolveTimelineInterval_StaysUnderTheBucketCap(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * 365 * 24 * time.Hour)

	_, _, width := ResolveTimelineInterval(start, end, "1m")

	if got := buckets(end.Sub(start), width); got > maxTimelineBuckets {
		t.Errorf("got %d buckets at width %s, want at most %d", got, width, maxTimelineBuckets)
	}
}

func TestBuckets_RoundsThePartialBucketUp(t *testing.T) {
	// The final partial bucket holds real records and must be drawn.
	if got := buckets(90*time.Minute, time.Hour); got != 2 {
		t.Errorf("buckets(90m, 1h) = %d, want 2", got)
	}
}
