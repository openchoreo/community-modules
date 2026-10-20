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
	defaultLimit = 100
	maxLimit     = 1000

	// workflowNamespacePrefix is how OpenChoreo names the Kubernetes namespace workflow
	// runs of an OpenChoreo namespace execute in.
	workflowNamespacePrefix = "workflows-"
)

// ComponentLogsParams holds parameters for component log queries.
type ComponentLogsParams struct {
	Namespace     string
	ProjectID     string
	EnvironmentID string
	ComponentID   string
	StartTime     time.Time
	EndTime       time.Time
	SearchPhrase  string
	LogLevels     []string
	Limit         int
	SortOrder     string
}

// WorkflowLogsParams holds parameters for workflow log queries.
type WorkflowLogsParams struct {
	Namespace       string
	WorkflowRunName string
	StartTime       time.Time
	EndTime         time.Time
	SearchPhrase    string
	LogLevels       []string
	Limit           int
	SortOrder       string
}

// ComponentLogEntry is one parsed component log record.
type ComponentLogEntry struct {
	Timestamp       time.Time
	Log             string
	LogLevel        string
	ComponentUID    string
	ComponentName   string
	EnvironmentUID  string
	EnvironmentName string
	ProjectUID      string
	ProjectName     string
	Namespace       string
	PodName         string
	PodNamespace    string
	ContainerName   string
}

// ComponentLogsResult is a page of component logs plus the true total.
type ComponentLogsResult struct {
	Logs       []ComponentLogEntry
	TotalCount int
	Took       int
}

// WorkflowLogEntry is one parsed workflow log record.
type WorkflowLogEntry struct {
	Timestamp time.Time
	Log       string
}

// WorkflowLogsResult is a page of workflow logs plus the true total.
type WorkflowLogsResult struct {
	Logs       []WorkflowLogEntry
	TotalCount int
	Took       int
}

// clampLimit applies the contract's default and ceiling to a page size.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// containerLogsFilter scopes a query to the container log records this module ships, so
// logs other sources send to the same environment never leak into an OpenChoreo view.
func (c *Client) containerLogsFilter() string {
	return eq(fLogSource, c.logsSource)
}

func componentLogsConditions(p ComponentLogsParams) string {
	conditions := []string{eq(fOCNamespace, p.Namespace)}
	if p.ProjectID != "" {
		conditions = append(conditions, eq(fOCProjectUID, p.ProjectID))
	}
	if p.EnvironmentID != "" {
		conditions = append(conditions, eq(fOCEnvironmentUID, p.EnvironmentID))
	}
	if p.ComponentID != "" {
		conditions = append(conditions, eq(fOCComponentUID, p.ComponentID))
	}
	if p.SearchPhrase != "" {
		conditions = append(conditions, containsText(fContent, p.SearchPhrase, false))
	}
	conditions = append(conditions, levelsCondition(p.LogLevels))
	return and(conditions...)
}

func workflowLogsConditions(p WorkflowLogsParams) string {
	conditions := []string{eq(fNamespaceName, workflowNamespacePrefix+p.Namespace)}
	if p.WorkflowRunName != "" {
		conditions = append(conditions, eq(fOCWorkflowRun, p.WorkflowRunName))
	}
	if p.SearchPhrase != "" {
		conditions = append(conditions, containsText(fContent, p.SearchPhrase, false))
	}
	conditions = append(conditions, levelsCondition(p.LogLevels))
	return and(conditions...)
}

// componentLogsFields are the fields a component log page needs; projecting them keeps
// the response small when records carry many labels.
var componentLogsFields = []string{
	fTimestamp, fContent, fNamespaceName, fPodName, fContainerName,
	fOCNamespace, fOCProject, fOCProjectUID, fOCComponent, fOCComponentUID,
	fOCEnvironment, fOCEnvironmentUID,
}

func fieldList(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += field(n)
	}
	return out
}

func buildRecordsQuery(scope, conditions, order string, limit int, fields []string) string {
	return fetchLogs().
		filter(scope).
		filter(conditions).
		then("sort " + field(fTimestamp) + " " + sortDirection(order)).
		then("limit " + strconv.Itoa(limit)).
		then("fields " + fieldList(fields)).
		String()
}

func buildCountQuery(scope, conditions string) string {
	return fetchLogs().
		filter(scope).
		filter(conditions).
		then("summarize total = count()").
		String()
}

// GenerateComponentLogsQueries returns the page query and the count query for component
// logs. Exported for tests and for debugging a query against a tenant by hand.
func (c *Client) GenerateComponentLogsQueries(p ComponentLogsParams) (records, count string, err error) {
	if p.Namespace == "" {
		return "", "", fmt.Errorf("namespace is required for component log queries")
	}
	conditions := componentLogsConditions(p)
	scope := c.containerLogsFilter()
	return buildRecordsQuery(scope, conditions, p.SortOrder, clampLimit(p.Limit), componentLogsFields),
		buildCountQuery(scope, conditions), nil
}

// GenerateWorkflowLogsQueries returns the page query and the count query for workflow logs.
func (c *Client) GenerateWorkflowLogsQueries(p WorkflowLogsParams) (records, count string, err error) {
	if p.Namespace == "" {
		return "", "", fmt.Errorf("namespace is required for workflow log queries")
	}
	conditions := workflowLogsConditions(p)
	scope := c.containerLogsFilter()
	return buildRecordsQuery(scope, conditions, p.SortOrder, clampLimit(p.Limit), []string{fTimestamp, fContent}),
		buildCountQuery(scope, conditions), nil
}

// runPageAndCount runs a page query and its count query concurrently. They are
// independent reads of the same window, so there is nothing to gain by serialising them.
func (c *Client) runPageAndCount(
	ctx context.Context, recordsQuery, countQuery string, start, end time.Time, limit int,
) (*QueryResult, int64, error) {
	var page *QueryResult
	var total int64

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		res, err := c.Query(gctx, QueryRequest{Query: recordsQuery, Start: start, End: end, MaxRecords: limit})
		if err != nil {
			return fmt.Errorf("records query failed: %w", err)
		}
		page = res
		return nil
	})
	g.Go(func() error {
		res, err := c.Query(gctx, QueryRequest{Query: countQuery, Start: start, End: end, MaxRecords: 1})
		if err != nil {
			return fmt.Errorf("count query failed: %w", err)
		}
		total = extractTotal(res)
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, 0, err
	}
	return page, total, nil
}

// extractTotal reads the single "total" value a count query returns.
func extractTotal(res *QueryResult) int64 {
	if res == nil || len(res.Records) == 0 {
		return 0
	}
	n, _ := int64Field(res.Records[0], "total")
	return n
}

// GetComponentLogs returns a page of component logs and the number of matching records.
func (c *Client) GetComponentLogs(ctx context.Context, p ComponentLogsParams) (*ComponentLogsResult, error) {
	recordsQuery, countQuery, err := c.GenerateComponentLogsQueries(p)
	if err != nil {
		return nil, err
	}

	page, total, err := c.runPageAndCount(ctx, recordsQuery, countQuery, p.StartTime, p.EndTime, clampLimit(p.Limit))
	if err != nil {
		return nil, err
	}

	logs := make([]ComponentLogEntry, 0, len(page.Records))
	for _, r := range page.Records {
		content := stringField(r, fContent)
		logs = append(logs, ComponentLogEntry{
			Timestamp:       timeField(r, fTimestamp),
			Log:             content,
			LogLevel:        ExtractLogLevel(content),
			ComponentUID:    stringField(r, fOCComponentUID),
			ComponentName:   stringField(r, fOCComponent),
			EnvironmentUID:  stringField(r, fOCEnvironmentUID),
			EnvironmentName: stringField(r, fOCEnvironment),
			ProjectUID:      stringField(r, fOCProjectUID),
			ProjectName:     stringField(r, fOCProject),
			Namespace:       stringField(r, fOCNamespace),
			PodName:         stringField(r, fPodName),
			PodNamespace:    stringField(r, fNamespaceName),
			ContainerName:   stringField(r, fContainerName),
		})
	}

	return &ComponentLogsResult{Logs: logs, TotalCount: int(total), Took: int(page.Took.Milliseconds())}, nil
}

// GetWorkflowLogs returns a page of workflow logs and the number of matching records.
func (c *Client) GetWorkflowLogs(ctx context.Context, p WorkflowLogsParams) (*WorkflowLogsResult, error) {
	recordsQuery, countQuery, err := c.GenerateWorkflowLogsQueries(p)
	if err != nil {
		return nil, err
	}

	page, total, err := c.runPageAndCount(ctx, recordsQuery, countQuery, p.StartTime, p.EndTime, clampLimit(p.Limit))
	if err != nil {
		return nil, err
	}

	logs := make([]WorkflowLogEntry, 0, len(page.Records))
	for _, r := range page.Records {
		logs = append(logs, WorkflowLogEntry{
			Timestamp: timeField(r, fTimestamp),
			Log:       stringField(r, fContent),
		})
	}

	return &WorkflowLogsResult{Logs: logs, TotalCount: int(total), Took: int(page.Took.Milliseconds())}, nil
}
