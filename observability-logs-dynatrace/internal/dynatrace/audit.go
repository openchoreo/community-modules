// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// AuditEntitlementsFilter lists values across every entitlement claim.
	AuditEntitlementsFilter = "actor.entitlements"

	maxTimelineBuckets = 500
	// Keeps count*week from overflowing a Duration.
	maxTimelineIntervalCount = 10000
	// One row per bucket and result value.
	maxTimelineRows = maxTimelineBuckets * 20
)

// AuditLogsParams holds the filters for an audit log query.
type AuditLogsParams struct {
	StartTime time.Time
	EndTime   time.Time

	ActorIDs          []string
	ActorTypes        []string
	ActorIssuers      []string
	ActorSessionIDs   []string
	ActorEntitlements []string

	ResourceTypes        []string
	ResourceNamespaces   []string
	ResourceEnvironments []string
	ResourceProjects     []string
	ResourceComponents   []string
	ResourceResources    []string
	ResourceNames        []string

	Actions      []string
	Categories   []string
	Results      []string
	Producers    []string
	Surfaces     []string
	OperationIDs []string
	RequestIDs   []string
	EventIDs     []string
	SourceIPs    []string
	UserAgents   []string

	SearchPhrase string
	Limit        int
	SortOrder    string
}

// AuditRecord is one audit event, decoded from the producer's log line.
type AuditRecord struct {
	SchemaVersion string         `json:"schema_version"`
	EventID       string         `json:"event_id"`
	EventTime     time.Time      `json:"event_time"`
	Actor         AuditActor     `json:"actor"`
	Action        string         `json:"action"`
	Category      string         `json:"category"`
	Result        string         `json:"result"`
	RequestID     string         `json:"request_id"`
	SourceIP      string         `json:"source_ip"`
	UserAgent     string         `json:"user_agent"`
	Producer      string         `json:"producer"`
	Surface       string         `json:"surface"`
	OperationID   string         `json:"operation_id"`
	HTTP          *AuditHTTPInfo `json:"http"`
	Resource      *AuditResource `json:"resource"`
	Metadata      map[string]any `json:"metadata"`

	// Read from the collector's fields, never from the producer's line.
	Collector AuditCollectorInfo `json:"-"`
}

// AuditActor is who performed the action. ID is unique only within Issuer.
type AuditActor struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Issuer       string              `json:"issuer"`
	SessionID    string              `json:"session_id"`
	Entitlements map[string][]string `json:"entitlements"`
}

// AuditHTTPInfo is the request line of an event that arrived over HTTP.
type AuditHTTPInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// AuditResource is the target resource and where authorization was decided.
type AuditResource struct {
	Type        string         `json:"type"`
	Namespace   string         `json:"namespace"`
	Environment string         `json:"environment"`
	Project     string         `json:"project"`
	Component   string         `json:"component"`
	Resource    string         `json:"resource"`
	UID         string         `json:"uid"`
	Name        string         `json:"name"`
	Metadata    map[string]any `json:"metadata"`
}

// AuditCollectorInfo is where the collector read the record from.
type AuditCollectorInfo struct {
	NamespaceName string
	PodName       string
	ContainerName string
}

// AuditLogsResult is a page of audit records with the total and optional timeline.
type AuditLogsResult struct {
	Records  []AuditRecord
	Total    int64
	Took     int
	Timeline *AuditTimeline
}

// AuditTimeline is per-interval counts across the queried window.
type AuditTimeline struct {
	Interval string
	Buckets  []AuditTimelineBucket
}

// AuditTimelineBucket is one interval of the timeline.
type AuditTimelineBucket struct {
	StartTime time.Time
	Total     int64
	Counts    map[string]int64
}

// auditFilterFields maps contract filters to the fields the Lua filter lifts them into.
var auditFilterFields = map[string]string{
	"actor.id":             auActorID,
	"actor.type":           auActorType,
	"actor.issuer":         auActorIssuer,
	"actor.session_id":     auActorSessionID,
	"actor.entitlements":   auActorEntitlements,
	"resource.type":        auResourceType,
	"resource.namespace":   auResourceNamespace,
	"resource.environment": auResourceEnvironment,
	"resource.project":     auResourceProject,
	"resource.component":   auResourceComponent,
	"resource.resource":    auResourceResource,
	"resource.name":        auResourceName,
	"action":               auAction,
	"category":             auCategory,
	"result":               auResult,
	"producer":             auProducer,
	"surface":              auSurface,
	"operation_id":         auOperationID,
	"source_ip":            auSourceIP,
	"user_agent":           auUserAgent,
}

// IsAuditFilter reports whether a filter's values can be listed.
func IsAuditFilter(filter string) bool {
	_, ok := auditFilterFields[filter]
	return ok
}

// auditScope limits a query to audit records.
func (c *Client) auditScope() string {
	return eq(fLogSource, c.auditSource)
}

// auditFetch starts an audit query, reading only the audit bucket when one is configured.
func (c *Client) auditFetch() *pipeline {
	return fetchLogsFrom(c.auditBucket)
}

func auditConditions(p AuditLogsParams) string {
	conditions := []string{
		anyOf(auActorID, p.ActorIDs),
		anyOf(auActorType, p.ActorTypes),
		anyOf(auActorIssuer, p.ActorIssuers),
		anyOf(auActorSessionID, p.ActorSessionIDs),
		arrayHasAny(auActorEntitlements, p.ActorEntitlements),
		anyOf(auResourceType, p.ResourceTypes),
		anyOf(auResourceNamespace, p.ResourceNamespaces),
		anyOf(auResourceEnvironment, p.ResourceEnvironments),
		anyOf(auResourceProject, p.ResourceProjects),
		anyOf(auResourceComponent, p.ResourceComponents),
		anyOf(auResourceResource, p.ResourceResources),
		anyOf(auResourceName, p.ResourceNames),
		anyOf(auAction, p.Actions),
		anyOf(auCategory, p.Categories),
		anyOf(auResult, p.Results),
		anyOf(auProducer, p.Producers),
		anyOf(auSurface, p.Surfaces),
		anyOf(auOperationID, p.OperationIDs),
		anyOf(auRequestID, p.RequestIDs),
		anyOf(auEventID, p.EventIDs),
		anyOf(auSourceIP, p.SourceIPs),
		anyOf(auUserAgent, p.UserAgents),
	}
	if p.SearchPhrase != "" {
		// Case-sensitive: an audit search is for an exact identifier or phrase, and the
		// contract's records are frozen text a SIEM compares byte for byte.
		conditions = append(conditions, containsText(fContent, p.SearchPhrase, true))
	}
	return and(conditions...)
}

var auditFields = []string{fTimestamp, fContent, fNamespaceName, fPodName, fContainerName}

// GenerateAuditLogsQueries returns the page query and the count query for audit logs.
func (c *Client) GenerateAuditLogsQueries(p AuditLogsParams) (records, count string) {
	conditions := auditConditions(p)
	scope := c.auditScope()
	dir := sortDirection(p.SortOrder)

	// event_id breaks ties, so paging by the last record's time is stable.
	records = c.auditFetch().
		filter(scope).
		filter(conditions).
		then("sort " + field(fTimestamp) + " " + dir + ", " + field(auEventID) + " " + dir).
		then("limit " + strconv.Itoa(clampLimit(p.Limit))).
		then("fields " + fieldList(auditFields)).
		String()
	count = c.auditFetch().
		filter(scope).
		filter(conditions).
		then("summarize total = count()").
		String()
	return records, count
}

// GenerateAuditTimelineQuery counts records per bucket and result. Buckets are numbered
// from the window start rather than taken from bin(), which aligns to the epoch and would
// leave the first and last buckets straddling the window's edges.
func (c *Client) GenerateAuditTimelineQuery(p AuditLogsParams, width time.Duration) string {
	bucket := "toLong(floor((unixMillisFromTimestamp(" + field(fTimestamp) + ") - " +
		strconv.FormatInt(p.StartTime.UnixMilli(), 10) + ") / " +
		strconv.FormatInt(width.Milliseconds(), 10) + "))"
	return c.auditFetch().
		filter(c.auditScope()).
		filter(auditConditions(p)).
		then("summarize record_count = count(), by: {bucket = " + bucket + ", result = " + field(auResult) + "}").
		String()
}

// GetAuditLogs returns a page of audit records, their total and, when timelineInterval is
// set, the timeline.
func (c *Client) GetAuditLogs(ctx context.Context, p AuditLogsParams, timelineInterval string) (*AuditLogsResult, error) {
	recordsQuery, countQuery := c.GenerateAuditLogsQueries(p)

	var page *QueryResult
	var total int64
	var timeline *AuditTimeline

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		res, t, err := c.runPageAndCount(gctx, recordsQuery, countQuery, p.StartTime, p.EndTime, clampLimit(p.Limit))
		page, total = res, t
		return err
	})
	if timelineInterval != "" {
		g.Go(func() error {
			// The contract allows omitting a failed timeline rather than failing the query.
			t, err := c.getAuditTimeline(gctx, p, timelineInterval)
			if err != nil {
				c.logger.Warn("Failed to compute audit timeline", slog.Any("error", err))
				return nil
			}
			timeline = t
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	records := make([]AuditRecord, 0, len(page.Records))
	for _, r := range page.Records {
		record, err := ParseAuditRecord(r)
		if err != nil {
			c.logger.Warn("Skipping malformed audit record", slog.Any("error", err))
			continue
		}
		records = append(records, record)
	}

	return &AuditLogsResult{
		Records:  records,
		Total:    total,
		Took:     int(page.Took.Milliseconds()),
		Timeline: timeline,
	}, nil
}

func (c *Client) getAuditTimeline(ctx context.Context, p AuditLogsParams, interval string) (*AuditTimeline, error) {
	width, err := parseTimelineInterval(interval)
	if err != nil {
		return nil, err
	}
	res, err := c.Query(ctx, QueryRequest{
		Query: c.GenerateAuditTimelineQuery(p, width), Start: p.StartTime, End: p.EndTime,
		MaxRecords: maxTimelineRows,
	})
	if err != nil {
		return nil, err
	}
	return buildAuditTimeline(interval, p.StartTime, p.EndTime, res.Records)
}

// GenerateAuditFilterValuesQueries returns the values query and its total query.
func (c *Client) GenerateAuditFilterValuesQueries(
	p AuditLogsParams, filter, valueSearch string, maxValues int,
) (values, total string, err error) {
	valueField, ok := auditFilterFields[filter]
	if !ok {
		return "", "", fmt.Errorf("unknown audit filter %q", filter)
	}

	if filter != AuditEntitlementsFilter {
		values, total = buildValuesQueries(c.auditFetch, c.auditScope(), auditConditions(p), valueField, valueSearch, maxValues)
		return values, total, nil
	}

	// Entitlements are an array per record: expand it to one row per value, deduplicated
	// first so a record carrying a value under two claims still counts once.
	valueFilter := `isNotNull(value) and value != ""`
	if valueSearch != "" {
		valueFilter = and(valueFilter, "contains(value, "+str(valueSearch)+", caseSensitive: false)")
	}
	base := func() *pipeline {
		return c.auditFetch().
			filter(c.auditScope()).
			filter(auditConditions(p)).
			then("fieldsAdd value = arrayDistinct(" + field(valueField) + ")").
			then("expand value").
			filter(valueFilter)
	}
	values = base().
		then("summarize record_count = count(), by: {value}").
		then("sort record_count desc, value asc").
		then("limit " + strconv.Itoa(maxValues)).
		String()
	total = base().
		then("summarize total = countDistinctExact(value)").
		String()
	return values, total, nil
}

// GetAuditLogFilterValues lists the distinct values a filter takes. params must already
// exclude the filter's own selections.
func (c *Client) GetAuditLogFilterValues(
	ctx context.Context, p AuditLogsParams, filter, valueSearch string, maxValues int,
) (*FilterValues, error) {
	valuesQuery, totalQuery, err := c.GenerateAuditFilterValuesQueries(p, filter, valueSearch, maxValues)
	if err != nil {
		return nil, err
	}
	return c.runValuesQueries(ctx, valuesQuery, totalQuery, p.StartTime, p.EndTime, maxValues)
}

// ParseAuditRecord decodes a record's original log line rather than its lifted fields, so
// nested metadata survives exactly as the producer wrote it. A record missing identity
// fields is an error rather than a zero-valued record.
func ParseAuditRecord(row map[string]any) (AuditRecord, error) {
	line := stringField(row, fContent)
	if line == "" {
		return AuditRecord{}, fmt.Errorf("record has no content")
	}

	var record AuditRecord
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return AuditRecord{}, fmt.Errorf("failed to parse audit line: %w", err)
	}

	// action and category are not required: a record with no resolved operation carries
	// them empty.
	for _, required := range []struct{ field, value string }{
		{"schema_version", record.SchemaVersion},
		{"event_id", record.EventID},
		{"actor.type", record.Actor.Type},
		{"actor.id", record.Actor.ID},
		{"result", record.Result},
	} {
		if required.value == "" {
			return AuditRecord{}, fmt.Errorf("record has no %s", required.field)
		}
	}
	if record.EventTime.IsZero() {
		return AuditRecord{}, fmt.Errorf("record has no event_time")
	}

	record.Collector = AuditCollectorInfo{
		NamespaceName: stringField(row, fNamespaceName),
		PodName:       stringField(row, fPodName),
		ContainerName: stringField(row, fContainerName),
	}
	return record, nil
}

// buildAuditTimeline zero-fills the buckets the query returns no rows for.
func buildAuditTimeline(interval string, start, end time.Time, rows []map[string]any) (*AuditTimeline, error) {
	width, err := parseTimelineInterval(interval)
	if err != nil {
		return nil, err
	}
	window := end.Sub(start)
	if width <= 0 || window <= 0 {
		return nil, fmt.Errorf("invalid timeline window")
	}

	count := int((window + width - 1) / width)
	buckets := make([]AuditTimelineBucket, count)
	for i := range buckets {
		buckets[i] = AuditTimelineBucket{
			StartTime: start.Add(time.Duration(i) * width),
			Counts:    map[string]int64{},
		}
	}

	for _, row := range rows {
		index, ok := int64Field(row, "bucket")
		if !ok || index < 0 || index >= int64(count) {
			continue
		}
		n, _ := int64Field(row, "record_count")
		b := &buckets[index]
		b.Total += n
		if result := stringField(row, "result"); result != "" {
			b.Counts[result] += n
		}
	}

	return &AuditTimeline{Interval: interval, Buckets: buckets}, nil
}

// ResolveTimelineInterval picks the bucket width, coarsening rather than rejecting one
// that exceeds the bucket limit.
func ResolveTimelineInterval(requested string, start, end time.Time) (string, error) {
	window := end.Sub(start)
	if window <= 0 {
		return "", fmt.Errorf("end time must be after start time")
	}

	width, err := parseTimelineInterval(requested)
	if err != nil {
		return "", err
	}
	if width <= 0 {
		width = (window / 60).Truncate(time.Minute)
		if width < time.Minute {
			width = time.Minute
		}
	}

	for (window+width-1)/width > maxTimelineBuckets {
		next := coarsenInterval(width)
		if next <= width {
			width += 7 * 24 * time.Hour
			continue
		}
		width = next
	}

	return formatTimelineInterval(width), nil
}

// parseTimelineInterval reads <count><unit> notation; empty means the adapter chooses.
func parseTimelineInterval(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid timeline interval: %s", s)
	}

	count, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || count <= 0 || count > maxTimelineIntervalCount {
		return 0, fmt.Errorf("invalid timeline interval: %s", s)
	}

	var unit time.Duration
	switch s[len(s)-1] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid timeline interval unit: %s", s)
	}

	return time.Duration(count) * unit, nil
}

func coarsenInterval(d time.Duration) time.Duration {
	switch {
	case d < time.Hour:
		return time.Hour
	case d < 24*time.Hour:
		return 24 * time.Hour
	case d < 7*24*time.Hour:
		return 7 * 24 * time.Hour
	}
	return d
}

func formatTimelineInterval(d time.Duration) string {
	switch {
	case d%(7*24*time.Hour) == 0:
		return fmt.Sprintf("%dw", d/(7*24*time.Hour))
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	default:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
}
