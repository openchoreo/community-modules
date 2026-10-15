// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// sanitizeWildcardValue escapes OpenSearch wildcard metacharacters from user-provided values
// to prevent wildcard injection attacks. Escaped characters: \, ", *, ?
func sanitizeWildcardValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `*`, `\*`)
	s = strings.ReplaceAll(s, `?`, `\?`)
	return s
}

// QueryBuilder provides methods to build OpenSearch queries.
type QueryBuilder struct {
	indexPrefix string
}

// NewQueryBuilder creates a new query builder with the given index prefix.
func NewQueryBuilder(indexPrefix string) *QueryBuilder {
	return &QueryBuilder{
		indexPrefix: indexPrefix,
	}
}

// formatDurationForOpenSearch normalizes durations so OpenSearch monitors accept them.
func formatDurationForOpenSearch(d string) (string, error) {
	parsed, err := time.ParseDuration(d)
	if err != nil {
		return "", err
	}

	if parsed <= 0 {
		return "", fmt.Errorf("duration must be a positive whole number of minutes or hours: %s", d)
	}

	switch {
	case parsed%time.Hour == 0:
		return fmt.Sprintf("%dh", parsed/time.Hour), nil
	case parsed%time.Minute == 0:
		return fmt.Sprintf("%dm", parsed/time.Minute), nil
	}
	return "", fmt.Errorf("duration must be a whole number of minutes or hours; seconds are not supported: %s", d)
}

// addTimeRangeFilter adds time range filter to must conditions.
func addTimeRangeFilter(mustConditions []map[string]interface{}, startTime, endTime string) []map[string]interface{} {
	if startTime != "" && endTime != "" {
		timeFilter := map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": map[string]interface{}{
					"gt": startTime,
					"lt": endTime,
				},
			},
		}
		mustConditions = append(mustConditions, timeFilter)
	}
	return mustConditions
}

// addSearchPhraseFilter adds wildcard search phrase filter to must conditions.
func addSearchPhraseFilter(mustConditions []map[string]interface{}, searchPhrase string) []map[string]interface{} {
	if searchPhrase != "" {
		searchFilter := map[string]interface{}{
			"wildcard": map[string]interface{}{
				"log": "*" + sanitizeWildcardValue(searchPhrase) + "*",
			},
		}
		mustConditions = append(mustConditions, searchFilter)
	}
	return mustConditions
}

// addLogLevelFilter adds log level filter to must conditions.
func addLogLevelFilter(mustConditions []map[string]interface{}, logLevels []string) []map[string]interface{} {
	if len(logLevels) > 0 {
		shouldConditions := make([]map[string]interface{}, 0, len(logLevels))

		for _, logLevel := range logLevels {
			shouldConditions = append(shouldConditions, map[string]interface{}{
				"wildcard": map[string]interface{}{
					"log": map[string]interface{}{
						"value":            "*" + sanitizeWildcardValue(strings.ToUpper(logLevel)) + "*",
						"case_insensitive": true,
					},
				},
			})
		}

		if len(shouldConditions) > 0 {
			logLevelFilter := map[string]interface{}{
				"bool": map[string]interface{}{
					"should":               shouldConditions,
					"minimum_should_match": 1,
				},
			}
			mustConditions = append(mustConditions, logLevelFilter)
		}
	}
	return mustConditions
}

// BuildComponentLogsQueryV1 builds a query for the API component logs endpoint.
func (qb *QueryBuilder) BuildComponentLogsQueryV1(params ComponentLogsQueryParamsV1) (map[string]interface{}, error) {
	if params.StartTime == "" || params.EndTime == "" || params.NamespaceName == "" {
		return nil, fmt.Errorf("start time, end time, and namespace name are required")
	}
	mustConditions := []map[string]interface{}{}

	mustConditions = addTimeRangeFilter(mustConditions, params.StartTime, params.EndTime)

	namespaceFilter := map[string]interface{}{
		"term": map[string]interface{}{
			OSNamespaceName: params.NamespaceName,
		},
	}
	mustConditions = append(mustConditions, namespaceFilter)

	if params.ProjectID != "" {
		projectFilter := map[string]interface{}{
			"term": map[string]interface{}{
				OSProjectID: params.ProjectID,
			},
		}
		mustConditions = append(mustConditions, projectFilter)
	}

	if params.ComponentID != "" {
		componentFilter := map[string]interface{}{
			"term": map[string]interface{}{
				OSComponentID: params.ComponentID,
			},
		}
		mustConditions = append(mustConditions, componentFilter)
	}

	if params.EnvironmentID != "" {
		environmentFilter := map[string]interface{}{
			"term": map[string]interface{}{
				OSEnvironmentID: params.EnvironmentID,
			},
		}
		mustConditions = append(mustConditions, environmentFilter)
	}

	if params.ReleaseLineID != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"term": map[string]interface{}{
				OSReleaseLineID: params.ReleaseLineID,
			},
		})
	}

	mustConditions = addSearchPhraseFilter(mustConditions, params.SearchPhrase)
	mustConditions = addLogLevelFilter(mustConditions, params.LogLevels)

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	query := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustConditions,
			},
		},
		"sort": []map[string]interface{}{
			{
				"@timestamp": map[string]interface{}{
					"order": sortOrder,
				},
			},
		},
	}

	return query, nil
}

// BuildWorkflowRunLogsQuery builds a query for workflow run logs with wildcard search.
func (qb *QueryBuilder) BuildWorkflowRunLogsQuery(params WorkflowRunQueryParams) map[string]interface{} {
	sanitizedWorkflowRunID := sanitizeWildcardValue(params.WorkflowRunID)
	podNamePattern := sanitizedWorkflowRunID + "*"

	mustConditions := []map[string]interface{}{
		{
			"wildcard": map[string]interface{}{
				KubernetesPodName: podNamePattern,
			},
		},
	}
	if params.StepName != "" {
		const kubeAnnotationsPrefix = "kubernetes.annotations."
		const argoNodeNameAnnotation = "workflows_argoproj_io/node-name"
		stepNameFilter := map[string]interface{}{
			"wildcard": map[string]interface{}{
				kubeAnnotationsPrefix + argoNodeNameAnnotation: "*" + sanitizeWildcardValue(params.StepName) + "*",
			},
		}
		mustConditions = append(mustConditions, stepNameFilter)
	}
	mustConditions = addTimeRangeFilter(mustConditions, params.QueryParams.StartTime, params.QueryParams.EndTime)

	if params.QueryParams.NamespaceName != "" {
		k8sNamespace := fmt.Sprintf("workflows-%s", params.QueryParams.NamespaceName)
		namespaceFilter := map[string]interface{}{
			"term": map[string]interface{}{
				KubernetesNamespaceName: k8sNamespace,
			},
		}
		mustConditions = append(mustConditions, namespaceFilter)
	}

	mustNotConditions := []map[string]interface{}{
		{
			"term": map[string]interface{}{
				KubernetesContainerName: "init",
			},
		},
		{
			"term": map[string]interface{}{
				KubernetesContainerName: "wait",
			},
		},
	}

	query := map[string]interface{}{
		"size": params.QueryParams.Limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":     mustConditions,
				"must_not": mustNotConditions,
			},
		},
		"sort": []map[string]interface{}{
			{
				"@timestamp": map[string]interface{}{
					"order": params.QueryParams.SortOrder,
				},
			},
		},
	}
	return query
}

// reasonsFilter builds a terms filter restricting results to the given event
// reasons (e.g. DeploymentSucceeded), or nil if reasons is empty.
func reasonsFilter(reasons []string) map[string]interface{} {
	if len(reasons) == 0 {
		return nil
	}
	return map[string]interface{}{
		"terms": map[string]interface{}{
			EvReason: reasons,
		},
	}
}

// eventsSort returns the sort clause shared by every events query.
//
// Sorting on timestamp alone is not stable when multiple events share one: the
// order among them can differ between two identical searches, so a caller that
// resumes from the last returned timestamp could see a different subset the
// second time. _seq_no breaks the tie deterministically (a numeric,
// always-doc-valued metadata field — unlike _id, it needs no fielddata and is
// safe to sort on).
//
// The clause is applied before "size", so OpenSearch truncates a window by
// dropping the events furthest from the sort direction, never an arbitrary
// subset. That is what makes resume-by-timestamp safe for the caller.
func eventsSort(sortOrder string) []map[string]interface{} {
	return []map[string]interface{}{
		{EvTimestamp: map[string]interface{}{"order": sortOrder}},
		{"_seq_no": map[string]interface{}{"order": sortOrder}},
	}
}

// addEventsTimeRangeFilter bounds an events query as [startTime, endTime).
//
// The start is inclusive, unlike addTimeRangeFilter which the logs queries use.
// A caller reading a window in parts resumes at the last timestamp it was
// handed, so an exclusive start would drop every event bearing it -- including
// any the previous read did not return.
func addEventsTimeRangeFilter(mustConditions []map[string]interface{}, startTime, endTime string) []map[string]interface{} {
	if startTime != "" && endTime != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"range": map[string]interface{}{
				EvTimestamp: map[string]interface{}{
					"gte": startTime,
					"lt":  endTime,
				},
			},
		})
	}
	return mustConditions
}

// BuildEventsAtTimestampQuery builds the follow-up that completes a timestamp
// group: every event bearing exactly ts, so a page cut at limit can be extended
// to a group boundary rather than splitting one.
//
// size and from page that follow-up. A group larger than one page would itself be
// truncated otherwise, which is the same split this exists to prevent, only moved
// one query along.
func (qb *QueryBuilder) BuildEventsAtTimestampQuery(
	base map[string]interface{}, ts string, size, from int,
) (map[string]interface{}, error) {
	encoded, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("failed to clone events query: %w", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("failed to clone events query: %w", err)
	}

	queryClause, ok := out["query"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("events query has no query clause to rebound")
	}
	boolQuery, ok := queryClause["bool"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("events query has no bool clause to rebound")
	}
	must, ok := boolQuery["must"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("events query has no must clause to rebound")
	}
	rebounded := false
	for _, cond := range must {
		m, isMap := cond.(map[string]interface{})
		if !isMap {
			continue
		}
		rng, isRange := m["range"].(map[string]interface{})
		if !isRange {
			continue
		}
		if _, onTimestamp := rng[EvTimestamp]; !onTimestamp {
			continue
		}
		rng[EvTimestamp] = map[string]interface{}{"gte": ts, "lte": ts}
		rebounded = true
	}
	if !rebounded {
		return nil, fmt.Errorf("events query has no %s range to rebound", EvTimestamp)
	}

	out["size"] = size
	if from > 0 {
		out["from"] = from
	}
	// The caller only needs the group itself, not a count of it.
	delete(out, "track_total_hits")
	return out, nil
}

// BuildEventsCountQuery builds a count-only form of an events query, asked to
// count as far as trackTotalHits.
//
// It exists because the page a caller is handed can be extended past limit to a
// timestamp boundary, and hits.total from the original search was only counted
// as far as that limit -- so it can come back smaller than the page it is meant
// to describe. Re-counting against the page actually returned is what keeps
// "total equals the events returned" meaning the window was read.
func (qb *QueryBuilder) BuildEventsCountQuery(
	base map[string]interface{}, trackTotalHits int,
) (map[string]interface{}, error) {
	encoded, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("failed to clone events query: %w", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("failed to clone events query: %w", err)
	}
	out["size"] = 0
	delete(out, "from")
	delete(out, "sort")
	out["track_total_hits"] = trackTotalHits
	return out, nil
}

// eventsTrackTotalHits is how far OpenSearch is asked to count matches for an
// events query.
//
// hits.total saturates at 10000 by default and reports relation "gte" once it
// does, so it is a floor rather than a count. Callers read completeness from
// total against the events returned, and that only needs to distinguish
// "exactly a full page" from "more than a page" -- counting to limit+1 answers
// that exactly, without the full-match-set scan track_total_hits: true costs.
func eventsTrackTotalHits(limit int) int {
	return limit + 1
}

// BuildComponentEventsQuery builds a query for the API component events endpoint.
func (qb *QueryBuilder) BuildComponentEventsQuery(params EventsQueryParams) (map[string]interface{}, error) {
	if params.StartTime == "" || params.EndTime == "" || params.NamespaceName == "" {
		return nil, fmt.Errorf("start time, end time, and namespace name are required")
	}
	mustConditions := []map[string]interface{}{}

	mustConditions = addEventsTimeRangeFilter(mustConditions, params.StartTime, params.EndTime)

	namespaceFilter := map[string]interface{}{
		"term": map[string]interface{}{
			EvNamespaceName: params.NamespaceName,
		},
	}
	mustConditions = append(mustConditions, namespaceFilter)

	if params.ProjectID != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"term": map[string]interface{}{
				EvProjectID: params.ProjectID,
			},
		})
	}

	if params.ComponentID != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"term": map[string]interface{}{
				EvComponentID: params.ComponentID,
			},
		})
	}

	if params.EnvironmentID != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"term": map[string]interface{}{
				EvEnvironmentID: params.EnvironmentID,
			},
		})
	}

	if params.ReleaseLineID != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"term": map[string]interface{}{
				EvReleaseLineID: params.ReleaseLineID,
			},
		})
	}

	if filter := reasonsFilter(params.Reasons); filter != nil {
		mustConditions = append(mustConditions, filter)
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	sort := eventsSort(sortOrder)
	query := map[string]interface{}{
		"size":             limit,
		"track_total_hits": eventsTrackTotalHits(limit),
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustConditions,
			},
		},
		"sort": sort,
	}

	return query, nil
}

// BuildReasonFilteredEventsQuery builds an unscoped events query restricted
// only by time range and reason: no namespace/component/environment filter.
// Used by machine consumers (e.g. the Delivery Insights aggregator) sweeping
// controller-emitted events across every namespace in one query.
func (qb *QueryBuilder) BuildReasonFilteredEventsQuery(params ReasonFilteredEventsQueryParams) (map[string]interface{}, error) {
	if params.StartTime == "" || params.EndTime == "" {
		return nil, fmt.Errorf("start time and end time are required")
	}
	if len(params.Reasons) == 0 {
		return nil, fmt.Errorf("at least one reason is required for an unscoped events sweep")
	}

	mustConditions := []map[string]interface{}{}
	mustConditions = addEventsTimeRangeFilter(mustConditions, params.StartTime, params.EndTime)
	mustConditions = append(mustConditions, reasonsFilter(params.Reasons))

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	sort := eventsSort(sortOrder)
	query := map[string]interface{}{
		"size":             limit,
		"track_total_hits": eventsTrackTotalHits(limit),
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustConditions,
			},
		},
		"sort": sort,
	}

	return query, nil
}

// BuildWorkflowEventsQuery builds a query for workflow run events.
//
// It mirrors the workflow logs convention: events are matched by the involved object
// name (the workflow pod/job names are prefixed with the workflow run ID) within the
// "workflows-<namespace>" Kubernetes namespace.
func (qb *QueryBuilder) BuildWorkflowEventsQuery(params WorkflowEventsQueryParams) (map[string]interface{}, error) {
	if params.StartTime == "" || params.EndTime == "" || params.NamespaceName == "" || params.WorkflowRunID == "" {
		return nil, fmt.Errorf("start time, end time, namespace name, and workflow run ID are required")
	}

	objectNamePattern := sanitizeWildcardValue(params.WorkflowRunID) + "*"
	mustConditions := []map[string]interface{}{
		{
			"wildcard": map[string]interface{}{
				EvObjectName: objectNamePattern,
			},
		},
	}

	// Narrow to a specific workflow task when requested. Event resource attributes do not carry
	// the Argo node-name annotation (annotation enrichment is disabled by default), so we match on
	// the involved object name, whose pod/job names embed the task name.
	if params.TaskName != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"wildcard": map[string]interface{}{
				EvObjectName: "*" + sanitizeWildcardValue(params.TaskName) + "*",
			},
		})
	}

	mustConditions = addEventsTimeRangeFilter(mustConditions, params.StartTime, params.EndTime)

	k8sNamespace := fmt.Sprintf("workflows-%s", params.NamespaceName)
	mustConditions = append(mustConditions, map[string]interface{}{
		"term": map[string]interface{}{
			EvObjectNamespace: k8sNamespace,
		},
	})

	if filter := reasonsFilter(params.Reasons); filter != nil {
		mustConditions = append(mustConditions, filter)
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	sort := eventsSort(sortOrder)
	query := map[string]interface{}{
		"size":             limit,
		"track_total_hits": eventsTrackTotalHits(limit),
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustConditions,
			},
		},
		"sort": sort,
	}

	return query, nil
}

// GenerateIndices generates the list of indices to search based on time range.
func (qb *QueryBuilder) GenerateIndices(startTime, endTime string) ([]string, error) {
	if startTime == "" || endTime == "" {
		return []string{qb.indexPrefix + "*"}, nil
	}

	start, err := time.Parse(time.RFC3339, startTime)
	if err != nil {
		return nil, fmt.Errorf("invalid start time format: %w", err)
	}

	end, err := time.Parse(time.RFC3339, endTime)
	if err != nil {
		return nil, fmt.Errorf("invalid end time format: %w", err)
	}

	indices := []string{}
	current := start

	for current.Before(end) || current.Equal(end) {
		indexName := qb.indexPrefix + current.Format("2006-01-02")
		indices = append(indices, indexName)
		current = current.AddDate(0, 0, 1)
	}

	endIndexName := qb.indexPrefix + end.Format("2006-01-02")
	if !contains(indices, endIndexName) {
		indices = append(indices, endIndexName)
	}

	return indices, nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// BuildLogAlertingRuleQuery builds the query for a log alerting rule monitor.
func (qb *QueryBuilder) BuildLogAlertingRuleQuery(params AlertingRuleRequest) (map[string]interface{}, error) {
	window, err := formatDurationForOpenSearch(params.Condition.Window)
	if err != nil {
		return nil, fmt.Errorf("failed to format window duration: %w", err)
	}
	filterConditions := []map[string]interface{}{
		{
			"range": map[string]interface{}{
				"@timestamp": map[string]interface{}{
					"from":          "{{period_end}}||-" + window,
					"to":            "{{period_end}}",
					"format":        "epoch_millis",
					"include_lower": true,
					"include_upper": true,
					"boost":         1,
				},
			},
		},
		{
			"term": map[string]interface{}{
				OSComponentID: map[string]interface{}{
					"value": params.Metadata.ComponentUID,
					"boost": 1,
				},
			},
		},
		{
			"term": map[string]interface{}{
				OSEnvironmentID: map[string]interface{}{
					"value": params.Metadata.EnvironmentUID,
					"boost": 1,
				},
			},
		},
		{
			"term": map[string]interface{}{
				OSProjectID: map[string]interface{}{
					"value": params.Metadata.ProjectUID,
					"boost": 1,
				},
			},
		},
		{
			"wildcard": map[string]interface{}{
				"log": map[string]interface{}{
					"wildcard": "*" + sanitizeWildcardValue(params.Source.Query) + "*",
					"boost":    1,
				},
			},
		},
	}
	if params.Metadata.ReleaseLineUID != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				OSReleaseLineID: map[string]interface{}{
					"value": params.Metadata.ReleaseLineUID,
					"boost": 1,
				},
			},
		})
	}

	query := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter":               filterConditions,
				"adjust_pure_negative": true,
				"boost":                1,
			},
		},
	}
	return query, nil
}

// BuildLogAlertingRuleMonitorBody builds the full monitor body for an alerting rule.
func (qb *QueryBuilder) BuildLogAlertingRuleMonitorBody(params AlertingRuleRequest) (map[string]interface{}, error) {
	intervalDuration, err := time.ParseDuration(params.Condition.Interval)
	if err != nil {
		return nil, fmt.Errorf("invalid interval format: %w", err)
	}
	if intervalDuration <= 0 || intervalDuration%time.Minute != 0 {
		return nil, fmt.Errorf("invalid interval: must be a positive whole number of minutes, got %q", params.Condition.Interval)
	}

	query, err := qb.BuildLogAlertingRuleQuery(params)
	if err != nil {
		return nil, fmt.Errorf("failed to build log alerting rule query: %w", err)
	}

	operatorSymbol, err := GetOperatorSymbol(params.Condition.Operator)
	if err != nil {
		return nil, fmt.Errorf("invalid condition operator: %w", err)
	}

	monitorBody := MonitorBody{
		Type:        "monitor",
		MonitorType: "query_level_monitor",
		Name:        params.Metadata.Name,
		Enabled:     params.Condition.Enabled,
		Schedule: MonitorSchedule{
			Period: MonitorSchedulePeriod{
				Interval: int(intervalDuration.Minutes()),
				Unit:     "MINUTES",
			},
		},
		Inputs: []MonitorInput{
			{
				Search: MonitorInputSearch{
					Indices: []string{qb.indexPrefix + "*"},
					Query:   query,
				},
			},
		},
		Triggers: []MonitorTrigger{
			{
				QueryLevelTrigger: &MonitorTriggerQueryLevelTrigger{
					Name:     "trigger-" + params.Metadata.Name,
					Severity: "1",
					Condition: MonitorTriggerCondition{
						Script: MonitorTriggerConditionScript{
							Source: fmt.Sprintf("ctx.results[0].hits.total.value %s %s", operatorSymbol, strconv.FormatFloat(params.Condition.Threshold, 'f', -1, 64)),
							Lang:   "painless",
						},
					},
					Actions: []MonitorTriggerAction{
						{
							Name:          "action-" + params.Metadata.Name,
							DestinationID: "openchoreo-observer-alerting-webhook",
							MessageTemplate: MonitorMessageTemplate{
								Source: buildWebhookMessageTemplate(params),
								Lang:   "mustache",
							},
							ThrottleEnabled: true,
							Throttle: MonitorTriggerActionThrottle{
								Value: 60,
								Unit:  "MINUTES",
							},
							SubjectTemplate: MonitorMessageTemplate{
								Source: "TheSubject",
								Lang:   "mustache",
							},
							ActionExecutionPolicy: MonitorTriggerActionExecutionPolicy{
								ActionExecutionScope: MonitorTriggerActionExecutionScope{
									PerAlert: MonitorActionExecutionScopePerAlert{
										ActionableAlerts: []string{"DEDUPED", "NEW"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(monitorBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal monitor body: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal monitor body: %w", err)
	}

	return result, nil
}

// GetOperatorSymbol converts an operator string to its symbol.
func GetOperatorSymbol(operator string) (string, error) {
	switch operator {
	case "gt":
		return ">", nil
	case "gte":
		return ">=", nil
	case "lt":
		return "<", nil
	case "lte":
		return "<=", nil
	}
	return "", fmt.Errorf("unknown operator: %q", operator)
}

// ReverseMapOperator converts an operator symbol back to its string name.
func ReverseMapOperator(operator string) string {
	switch operator {
	case ">":
		return "gt"
	case ">=":
		return "gte"
	case "<":
		return "lt"
	case "<=":
		return "lte"
	}
	return ""
}

// buildWebhookMessageTemplate builds a JSON message template for webhook notifications.
func buildWebhookMessageTemplate(params AlertingRuleRequest) string {
	ruleName, _ := json.Marshal(params.Metadata.Name)
	ruleNamespace, _ := json.Marshal(params.Metadata.Namespace)
	componentUID, _ := json.Marshal(params.Metadata.ComponentUID)
	projectUID, _ := json.Marshal(params.Metadata.ProjectUID)
	environmentUID, _ := json.Marshal(params.Metadata.EnvironmentUID)

	return fmt.Sprintf(
		`{"ruleName":%s,"ruleNamespace":%s,"componentUid":%s,"projectUid":%s,"environmentUid":%s,"alertValue":{{ctx.results.0.hits.total.value}},"alertTimestamp":"{{ctx.periodStart}}"}`,
		string(ruleName),
		string(ruleNamespace),
		string(componentUID),
		string(projectUID),
		string(environmentUID),
	)
}
