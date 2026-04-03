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
