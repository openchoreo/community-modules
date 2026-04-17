// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import "strings"

const sortOrderDesc = "desc"

// sanitizeWildcardValue escapes OpenSearch wildcard metacharacters from user-provided values
// to prevent wildcard injection attacks. Escaped characters: \, ", *, ?
func sanitizeWildcardValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `*`, `\*`)
	s = strings.ReplaceAll(s, `?`, `\?`)
	return s
}

// BuildTracesQuery builds a query for retrieving spans matching the given parameters.
func BuildTracesQuery(params TracesRequestParams) map[string]interface{} {
	filterConditions := []map[string]interface{}{
		{
			"range": map[string]interface{}{
				"startTime": map[string]interface{}{
					"gte": params.StartTime,
				},
			},
		},
		{
			"range": map[string]interface{}{
				"endTime": map[string]interface{}{
					"lte": params.EndTime,
				},
			},
		},
	}

	// Add TraceID filter if present
	if params.TraceID != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"wildcard": map[string]interface{}{
				"traceId": sanitizeWildcardValue(params.TraceID),
			},
		})
	}

	// Add ComponentUIDs filter if present
	if len(params.ComponentUIDs) > 0 {
		shouldConditions := make([]map[string]interface{}, 0, len(params.ComponentUIDs))
		for _, componentUID := range params.ComponentUIDs {
			shouldConditions = append(shouldConditions, map[string]interface{}{
				"term": map[string]interface{}{
					"resource.openchoreo.dev/component-uid": componentUID,
				},
			})
		}
		filterConditions = append(filterConditions, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": shouldConditions,
			},
		})
	}

	// Add EnvironmentUID filter if present
	if params.EnvironmentUID != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				"resource.openchoreo.dev/environment-uid": params.EnvironmentUID,
			},
		})
	}

	if params.ProjectUID != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				"resource.openchoreo.dev/project-uid": params.ProjectUID,
			},
		})
	}

	if params.Namespace != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				"resource.openchoreo.dev/namespace": params.Namespace,
			},
		})
	}

	query := map[string]interface{}{
		"size": params.Limit,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": filterConditions,
			},
		},
		"sort": []map[string]interface{}{
			{
				"startTime": map[string]interface{}{
					"order": params.SortOrder,
				},
			},
		},
	}

	return query
}

// BuildTracesAggregationQuery builds an aggregation query that groups spans by traceId,
// so that the limit parameter controls the number of distinct traces returned.
func BuildTracesAggregationQuery(params TracesRequestParams) map[string]interface{} {
	filterConditions := []map[string]interface{}{
		{
			"range": map[string]interface{}{
				"startTime": map[string]interface{}{
					"gte": params.StartTime,
				},
			},
		},
		{
			"range": map[string]interface{}{
				"endTime": map[string]interface{}{
					"lte": params.EndTime,
				},
			},
		},
	}

	// Add ComponentUIDs filter if present
	if len(params.ComponentUIDs) > 0 {
		shouldConditions := make([]map[string]interface{}, 0, len(params.ComponentUIDs))
		for _, componentUID := range params.ComponentUIDs {
			shouldConditions = append(shouldConditions, map[string]interface{}{
				"term": map[string]interface{}{
					"resource.openchoreo.dev/component-uid": componentUID,
				},
			})
		}
		filterConditions = append(filterConditions, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": shouldConditions,
			},
		})
	}

	// Add EnvironmentUID filter if present
	if params.EnvironmentUID != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				"resource.openchoreo.dev/environment-uid": params.EnvironmentUID,
			},
		})
	}

	if params.ProjectUID != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				"resource.openchoreo.dev/project-uid": params.ProjectUID,
			},
		})
	}

	if params.Namespace != "" {
		filterConditions = append(filterConditions, map[string]interface{}{
			"term": map[string]interface{}{
				"resource.openchoreo.dev/namespace": params.Namespace,
			},
		})
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = sortOrderDesc
	}

	query := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": filterConditions,
			},
		},
		"aggs": map[string]interface{}{
			"trace_count": map[string]interface{}{
				"cardinality": map[string]interface{}{
					"field": "traceId",
				},
			},
			"traces": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "traceId",
					"size":  params.Limit,
					"order": map[string]interface{}{
						"min_start_time": sortOrder,
					},
				},
				"aggs": map[string]interface{}{
					"earliest_span": map[string]interface{}{
						"top_hits": map[string]interface{}{
							"size": 1,
							"sort": []map[string]interface{}{
								{
									"startTime": map[string]interface{}{
										"order": "asc",
									},
								},
							},
							"_source": []string{"spanId", "name", "parentSpanId", "startTime", "kind"},
						},
					},
					"root_span": map[string]interface{}{
						"filter": map[string]interface{}{
							"term": map[string]interface{}{
								"parentSpanId": "",
							},
						},
						"aggs": map[string]interface{}{
							"hit": map[string]interface{}{
								"top_hits": map[string]interface{}{
									"size":    1,
									"_source": []string{"spanId", "name", "startTime", "kind"},
								},
							},
						},
					},
					"latest_span": map[string]interface{}{
						"top_hits": map[string]interface{}{
							"size": 1,
							"sort": []map[string]interface{}{
								{
									"endTime": map[string]interface{}{
										"order": sortOrderDesc,
									},
								},
							},
							"_source": []string{"endTime"},
						},
					},
					"error_span_count": map[string]interface{}{
						"filter": map[string]interface{}{
							"term": map[string]interface{}{
								"status.code": map[string]interface{}{
									"value":            "error",
									"case_insensitive": true,
								},
							},
						},
					},
					"min_start_time": map[string]interface{}{
						"min": map[string]interface{}{
							"field": "startTime",
						},
					},
				},
			},
		},
	}

	return query
}

// BuildSpanDetailsQuery builds a query for retrieving a specific span by traceId and spanId.
func BuildSpanDetailsQuery(traceID string, spanID string) map[string]interface{} {
	filterConditions := []map[string]interface{}{
		{
			"term": map[string]interface{}{
				"traceId": traceID,
			},
		},
		{
			"term": map[string]interface{}{
				"spanId": spanID,
			},
		},
	}

	query := map[string]interface{}{
		"size": 1,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": filterConditions,
			},
		},
	}

	return query
}
