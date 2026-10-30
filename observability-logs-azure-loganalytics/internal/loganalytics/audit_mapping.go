// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// mapAuditRows returns the skipped rows so the caller can log them.
func mapAuditRows(t azlogs.Table) ([]AuditRecord, []error, error) {
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, nil, err
	}

	records := make([]AuditRecord, 0, len(t.Rows))
	var skipped []error

	for _, row := range t.Rows {
		rec, err := auditRecordFromRow(row, idx)
		if err != nil {
			skipped = append(skipped, err)
			continue
		}
		records = append(records, rec)
	}
	return records, skipped, nil
}

func auditRecordFromRow(row azlogs.Row, idx map[string]int) (AuditRecord, error) {
	rec := AuditRecord{
		SchemaVersion: rowString(row, idx, ColSchemaVersion),
		EventID:       rowString(row, idx, ColEventID),
		EventTime:     rowTime(row, idx, ColEventTime),
		Action:        rowString(row, idx, ColAction),
		Category:      rowString(row, idx, ColCategory),
		Result:        rowString(row, idx, ColResult),
		RequestID:     rowString(row, idx, ColRequestID),
		SourceIP:      rowString(row, idx, ColSourceIP),
		UserAgent:     rowString(row, idx, ColUserAgent),
		Producer:      rowString(row, idx, ColProducer),
		Surface:       rowString(row, idx, ColSurface),
		OperationID:   rowString(row, idx, ColOperationID),
		Actor: AuditActor{
			Type:      rowString(row, idx, ColActorType),
			ID:        rowString(row, idx, ColActorID),
			Issuer:    rowString(row, idx, ColActorIssuer),
			SessionID: rowString(row, idx, ColActorSessionID),
		},
	}

	// The query already excludes these; re-checked here for a caller that built
	// its own. Action and category are deliberately absent: a request rejected
	// before an operation resolved carries them empty and is still worth keeping.
	for _, required := range []struct{ field, value string }{
		{"schema_version", rec.SchemaVersion},
		{"event_id", rec.EventID},
		{"actor.type", rec.Actor.Type},
		{"actor.id", rec.Actor.ID},
		{"result", rec.Result},
	} {
		if required.value == "" {
			return AuditRecord{}, fmt.Errorf("audit record has no %s", required.field)
		}
	}
	if rec.EventTime.IsZero() {
		return AuditRecord{}, fmt.Errorf("audit record %s has no event_time", rec.EventID)
	}

	if m := unmarshalDynamic(rowString(row, idx, ColActorEntitlementsJSON)); len(m) > 0 {
		rec.Actor.Entitlements = toStringSlices(m)
	}
	rec.Metadata = unmarshalDynamic(rowString(row, idx, ColMetadataJSON))

	var request AuditRequest
	if unmarshalGroup(rowString(row, idx, ColRequestJSON), &request) {
		rec.Request = &request
	}
	// A returned group is kept even with a count of 0 and no items: that is a
	// read that found nothing, unlike an absent group.
	var returned AuditReturned
	if unmarshalGroup(rowString(row, idx, ColReturnedJSON), &returned) {
		rec.Returned = &returned
	}

	if method, path := rowString(row, idx, ColHTTPMethod), rowString(row, idx, ColHTTPPath); method != "" || path != "" {
		rec.HTTP = &AuditHTTPInfo{Method: method, Path: path}
	}

	res := AuditResource{
		Type:        rowString(row, idx, ColResourceType),
		Namespace:   rowString(row, idx, ColResourceNamespace),
		Environment: rowString(row, idx, ColResourceEnvironment),
		Project:     rowString(row, idx, ColResourceProject),
		Component:   rowString(row, idx, ColResourceComponent),
		Resource:    rowString(row, idx, ColResourceResource),
		UID:         rowString(row, idx, ColResourceUID),
		Name:        rowString(row, idx, ColResourceName),
		Metadata:    unmarshalDynamic(rowString(row, idx, ColResourceMetadataJSON)),
	}
	// Omitted rather than empty: an empty object would read as a resource that
	// exists but is unnamed.
	if !res.isEmpty() {
		rec.Resource = &res
	}

	ns := rowString(row, idx, ColCollectorNamespace)
	pod := rowString(row, idx, ColCollectorPod)
	container := rowString(row, idx, ColCollectorContainer)
	if ns != "" || pod != "" || container != "" {
		rec.Collector = &AuditCollectorInfo{NamespaceName: ns, PodName: pod, ContainerName: container}
	}

	return rec, nil
}

// unmarshalDynamic decodes a dynamic column projected with tostring(). Absent,
// empty or not an object yields nil; the record is still valid.
func unmarshalDynamic(s string) map[string]any {
	if s == "" || s == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// unmarshalGroup decodes a dynamic column projected with tostring() into out,
// reporting whether the group was present. Absent or malformed leaves the
// record without it; the record is still valid.
func unmarshalGroup(s string, out any) bool {
	if s == "" || s == "null" {
		return false
	}
	return json.Unmarshal([]byte(s), out) == nil
}

// toStringSlices drops a claim that is neither a string nor an array of them,
// rather than coercing it.
//
// A scalar claim is read here but would not match the entitlements filter,
// which uses set_has_element against an array. The producers emit
// map[string][]string, so this only matters for a record written by something
// else.
func toStringSlices(m map[string]any) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = []string{t}
		case []any:
			vals := make([]string, 0, len(t))
			for _, e := range t {
				if s, ok := e.(string); ok {
					vals = append(vals, s)
				}
			}
			if len(vals) > 0 {
				out[k] = vals
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mapAuditFilterValues(t azlogs.Table) ([]AuditFilterValue, error) {
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, err
	}
	values := make([]AuditFilterValue, 0, len(t.Rows))
	for _, row := range t.Rows {
		v := rowString(row, idx, ColValue)
		if v == "" {
			continue
		}
		values = append(values, AuditFilterValue{Value: v, Count: rowInt64(row, idx, ColCount)})
	}
	return values, nil
}

// mapAuditTimeline fills the gaps KQL leaves: it returns only the bins that had
// records, and a missing bucket would read as absent data, not a quiet period.
func mapAuditTimeline(t azlogs.Table, start, end time.Time, interval string, width time.Duration) (*AuditTimeline, error) {
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, err
	}
	if width <= 0 {
		return nil, fmt.Errorf("loganalytics: timeline width must be positive, got %s", width)
	}

	type key struct {
		start  time.Time
		result string
	}
	counts := map[key]int64{}
	for _, row := range t.Rows {
		bucketStart := rowTime(row, idx, ColBucketStart)
		if bucketStart.IsZero() {
			continue
		}
		counts[key{bucketStart.UTC(), rowString(row, idx, ColResult)}] += rowInt64(row, idx, ColBucketTotal)
	}

	// bin_at is anchored at StartTime, so the walk starts there too.
	first := start.UTC()
	out := &AuditTimeline{Interval: interval}
	for at := first; at.Before(end.UTC()); at = at.Add(width) {
		b := AuditTimelineBucket{StartTime: at}
		for k, n := range counts {
			if !k.start.Equal(at) {
				continue
			}
			b.Total += n
			if k.result != "" {
				if b.Counts == nil {
					b.Counts = map[string]int64{}
				}
				b.Counts[k.result] += n
			}
		}
		out.Buckets = append(out.Buckets, b)
	}

	// A window shorter than one bucket still has somewhere to put its records.
	if len(out.Buckets) == 0 {
		out.Buckets = []AuditTimelineBucket{{StartTime: first}}
	}

	sort.Slice(out.Buckets, func(i, j int) bool {
		return out.Buckets[i].StartTime.Before(out.Buckets[j].StartTime)
	})
	return out, nil
}
