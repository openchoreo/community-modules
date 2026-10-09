// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// DefaultMaxFilterValues is the number of values returned when the caller does not ask
	// for a count. It matches the contract's default.
	DefaultMaxFilterValues = 100
	// MaxMaxFilterValues is the ceiling the contract puts on maxValues.
	MaxMaxFilterValues = 1000
)

// PlatformLogsParams holds parameters for a platform log query.
//
// Every filter is optional except the time range. Multi-value fields OR within themselves
// and AND with each other, and an absent or empty field is not a filter.
type PlatformLogsParams struct {
	ClusterInstances []string
	Namespaces       []string
	PodNames         []string
	ContainerNames   []string
	Labels           map[string]string
	StartTime        time.Time
	EndTime          time.Time
	SearchPhrase     string
	LogLevels        []string
	Limit            int
	SortOrder        string
}

// PlatformLogEntry is one parsed platform log record.
type PlatformLogEntry struct {
	Timestamp       time.Time
	Log             string
	LogLevel        string
	ClusterInstance string
	NamespaceName   string
	PodName         string
	ContainerName   string
	PodIP           string
	NodeName        string
	ContainerImage  string
	Labels          map[string]string
}

// PlatformLogsResult is a page of platform logs plus the true total.
type PlatformLogsResult struct {
	Logs       []PlatformLogEntry
	TotalCount int
	Took       int
}

// FilterValue is one value a filter takes, with how many records carry it.
type FilterValue struct {
	Value string
	Count int64
}

// FilterValues is the parsed result of a filter-values query.
type FilterValues struct {
	Values      []FilterValue
	TotalValues int64
	Took        int
}

// platformFilterFields maps a filter, named as the request field that accepts it, onto the
// field it selects on. Pod labels are deliberately absent: the set of label keys is open,
// so "which values does this filter take" has no single answer for them.
var platformFilterFields = map[string]string{
	"clusterInstance": fClusterName,
	"namespace":       fNamespaceName,
	"podName":         fPodName,
	"containerName":   fContainerName,
}

// PlatformFilterField returns the field a filter selects on, and whether the filter is one
// this adapter can list.
func PlatformFilterField(filter string) (string, bool) {
	f, ok := platformFilterFields[filter]
	return f, ok
}

// ClearFilterSelections returns params with the named filter's own selections removed, so
// a picker keeps offering the alternatives to what is already selected.
func ClearFilterSelections(p PlatformLogsParams, filter string) PlatformLogsParams {
	switch filter {
	case "clusterInstance":
		p.ClusterInstances = nil
	case "namespace":
		p.Namespaces = nil
	case "podName":
		p.PodNames = nil
	case "containerName":
		p.ContainerNames = nil
	}
	return p
}

// platformConditions builds the filter shared by the record, count and filter-values
// queries. Keeping them in one place is what guarantees the values offered for a filter are
// drawn from the records the caller would get back.
func platformConditions(p PlatformLogsParams) string {
	conditions := []string{
		anyOf(fClusterName, p.ClusterInstances),
		anyOf(fNamespaceName, p.Namespaces),
		anyOf(fPodName, p.PodNames),
		anyOf(fContainerName, p.ContainerNames),
	}
	for _, k := range sortedKeys(p.Labels) {
		conditions = append(conditions, arrayHasAny(fPodLabels, []string{k + "=" + p.Labels[k]}))
	}
	if p.SearchPhrase != "" {
		conditions = append(conditions, containsText(fContent, p.SearchPhrase, false))
	}
	conditions = append(conditions, levelsCondition(p.LogLevels))
	return and(conditions...)
}

var platformLogsFields = []string{
	fTimestamp, fContent, fClusterName, fNamespaceName, fPodName, fContainerName,
	fPodIP, fNodeName, fContainerImage, fPodLabels,
}

// GeneratePlatformLogsQueries returns the page query and the count query for platform logs.
func (c *Client) GeneratePlatformLogsQueries(p PlatformLogsParams) (records, count string) {
	conditions := platformConditions(p)
	scope := c.containerLogsFilter()
	return buildRecordsQuery(scope, conditions, p.SortOrder, clampLimit(p.Limit), platformLogsFields),
		buildCountQuery(scope, conditions)
}

// GetPlatformLogs queries logs by raw Kubernetes coordinates.
func (c *Client) GetPlatformLogs(ctx context.Context, p PlatformLogsParams) (*PlatformLogsResult, error) {
	recordsQuery, countQuery := c.GeneratePlatformLogsQueries(p)

	page, total, err := c.runPageAndCount(ctx, recordsQuery, countQuery, p.StartTime, p.EndTime, clampLimit(p.Limit))
	if err != nil {
		return nil, err
	}

	logs := make([]PlatformLogEntry, 0, len(page.Records))
	for _, r := range page.Records {
		content := stringField(r, fContent)
		logs = append(logs, PlatformLogEntry{
			Timestamp:       timeField(r, fTimestamp),
			Log:             content,
			LogLevel:        ExtractLogLevel(content),
			ClusterInstance: stringField(r, fClusterName),
			NamespaceName:   stringField(r, fNamespaceName),
			PodName:         stringField(r, fPodName),
			ContainerName:   stringField(r, fContainerName),
			PodIP:           stringField(r, fPodIP),
			NodeName:        stringField(r, fNodeName),
			ContainerImage:  stringField(r, fContainerImage),
			Labels:          splitLabels(stringSlice(r, fPodLabels)),
		})
	}

	return &PlatformLogsResult{Logs: logs, TotalCount: int(total), Took: int(page.Took.Milliseconds())}, nil
}

// buildValuesQueries returns the grouped-values query and the distinct-count query for one
// field. Both apply the same filter, so the total describes the list it accompanies. Rows
// where the field is absent or empty are dropped: no filter value would select one.
func buildValuesQueries(fetch func() *pipeline, scope, conditions, valueField, valueSearch string, maxValues int) (values, total string) {
	filters := and(conditions, present(valueField))
	if valueSearch != "" {
		filters = and(filters, containsText(valueField, valueSearch, false))
	}

	values = fetch().
		filter(scope).
		filter(filters).
		then("summarize record_count = count(), by: {value = " + field(valueField) + "}").
		// Busiest first, ties broken by value so the order is stable across calls.
		then("sort record_count desc, value asc").
		then("limit " + strconv.Itoa(maxValues)).
		String()

	total = fetch().
		filter(scope).
		filter(filters).
		then("summarize total = countDistinctExact(" + field(valueField) + ")").
		String()
	return values, total
}

// GeneratePlatformFilterValuesQueries returns the values query and its total query.
func (c *Client) GeneratePlatformFilterValuesQueries(
	p PlatformLogsParams, valueField, valueSearch string, maxValues int,
) (values, total string) {
	return buildValuesQueries(fetchLogs, c.containerLogsFilter(), platformConditions(p), valueField, valueSearch, maxValues)
}

// GetPlatformLogFilterValues lists the distinct values one filter takes under a query. The
// caller is expected to have cleared the listed filter's own selections already.
func (c *Client) GetPlatformLogFilterValues(
	ctx context.Context, p PlatformLogsParams, valueField, valueSearch string, maxValues int,
) (*FilterValues, error) {
	valuesQuery, totalQuery := c.GeneratePlatformFilterValuesQueries(p, valueField, valueSearch, maxValues)
	return c.runValuesQueries(ctx, valuesQuery, totalQuery, p.StartTime, p.EndTime, maxValues)
}

func (c *Client) runValuesQueries(
	ctx context.Context, valuesQuery, totalQuery string, start, end time.Time, maxValues int,
) (*FilterValues, error) {
	var valuesRes *QueryResult
	var total int64

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		res, err := c.Query(gctx, QueryRequest{Query: valuesQuery, Start: start, End: end, MaxRecords: maxValues})
		if err != nil {
			return fmt.Errorf("filter values query failed: %w", err)
		}
		valuesRes = res
		return nil
	})
	g.Go(func() error {
		res, err := c.Query(gctx, QueryRequest{Query: totalQuery, Start: start, End: end, MaxRecords: 1})
		if err != nil {
			return fmt.Errorf("filter values total query failed: %w", err)
		}
		total = extractTotal(res)
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	values := make([]FilterValue, 0, len(valuesRes.Records))
	for _, r := range valuesRes.Records {
		v := stringField(r, "value")
		if v == "" {
			continue
		}
		n, _ := int64Field(r, "record_count")
		values = append(values, FilterValue{Value: v, Count: n})
	}

	return &FilterValues{Values: values, TotalValues: total, Took: int(valuesRes.Took.Milliseconds())}, nil
}
