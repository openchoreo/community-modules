// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/dynatrace"
)

// QueryPlatformLogs implements POST /api/v1alpha1/platform-logs/query.
func (h *LogsHandler) QueryPlatformLogs(
	ctx context.Context, request gen.QueryPlatformLogsRequestObject,
) (gen.QueryPlatformLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryPlatformLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	if !request.Body.EndTime.After(request.Body.StartTime) {
		return gen.QueryPlatformLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("endTime must be after startTime"),
		}, nil
	}

	if key, ok := firstInvalidLabelKey(request.Body); !ok {
		return gen.QueryPlatformLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr(fmt.Sprintf("label key %q is not a valid Kubernetes label key", key)),
		}, nil
	}

	result, err := h.backend.GetPlatformLogs(ctx, platformQueryParams(request.Body))
	if err != nil {
		h.logger.Error("Failed to query platform logs",
			slog.String("function", "QueryPlatformLogs"),
			slog.Any("error", err),
		)
		return gen.QueryPlatformLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	logs := make([]gen.PlatformLog, 0, len(result.Logs))
	for _, entry := range result.Logs {
		log := gen.PlatformLog{
			Timestamp:       entry.Timestamp,
			Log:             entry.Log,
			Level:           strPtr(entry.LogLevel),
			ClusterInstance: strPtr(entry.ClusterInstance),
			NamespaceName:   strPtr(entry.NamespaceName),
			PodName:         strPtr(entry.PodName),
			ContainerName:   strPtr(entry.ContainerName),
			PodIp:           strPtr(entry.PodIP),
			NodeName:        strPtr(entry.NodeName),
			ContainerImage:  strPtr(entry.ContainerImage),
		}
		if len(entry.Labels) > 0 {
			labels := entry.Labels
			log.Labels = &labels
		}
		logs = append(logs, log)
	}

	return gen.QueryPlatformLogs200JSONResponse{
		Logs:   logs,
		Total:  result.TotalCount,
		TookMs: result.Took,
	}, nil
}

// QueryPlatformLogFilterValues implements POST /api/v1alpha1/platform-logs/filter-values.
//
// Answers "what can I pick next" for one filter: the distinct values it takes among the
// records the rest of the query already selects. The listed filter's own selections are
// dropped first - counting them would offer back only what is already picked, so a choice
// could never be widened once made.
func (h *LogsHandler) QueryPlatformLogFilterValues(
	ctx context.Context, request gen.QueryPlatformLogFilterValuesRequestObject,
) (gen.QueryPlatformLogFilterValuesResponseObject, error) {
	if request.Body == nil {
		return gen.QueryPlatformLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}
	body := request.Body

	if !body.Query.EndTime.After(body.Query.StartTime) {
		return gen.QueryPlatformLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("query.endTime must be after query.startTime"),
		}, nil
	}

	if key, ok := firstInvalidLabelKey(&body.Query); !ok {
		return gen.QueryPlatformLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr(fmt.Sprintf("label key %q is not a valid Kubernetes label key", key)),
		}, nil
	}

	filter := string(body.Filter)
	valueField, ok := dynatrace.PlatformFilterField(filter)
	if !ok {
		return gen.QueryPlatformLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr(fmt.Sprintf("filter %q cannot be listed", filter)),
		}, nil
	}

	maxValues := dynatrace.DefaultMaxFilterValues
	if body.MaxValues != nil {
		maxValues = *body.MaxValues
	}
	if maxValues < 1 {
		maxValues = 1
	}
	if maxValues > dynatrace.MaxMaxFilterValues {
		maxValues = dynatrace.MaxMaxFilterValues
	}

	valueSearch := ""
	if body.ValueSearch != nil {
		valueSearch = *body.ValueSearch
	}

	// limit and sortOrder page and order records; this returns none, so they are ignored
	// rather than rejected, as the contract requires.
	params := dynatrace.ClearFilterSelections(platformQueryParams(&body.Query), filter)

	result, err := h.backend.GetPlatformLogFilterValues(ctx, params, valueField, valueSearch, maxValues)
	if err != nil {
		h.logger.Error("Failed to query platform log filter values",
			slog.String("function", "QueryPlatformLogFilterValues"),
			slog.String("filter", filter),
			slog.Any("error", err),
		)
		return gen.QueryPlatformLogFilterValues500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	values := make([]gen.PlatformLogFilterValue, 0, len(result.Values))
	for _, v := range result.Values {
		values = append(values, gen.PlatformLogFilterValue{Value: v.Value, Count: v.Count})
	}

	return gen.QueryPlatformLogFilterValues200JSONResponse{
		Filter: filter,
		Values: values,
		// The contract treats this as a sense of scale rather than a guaranteed total;
		// countDistinctExact makes it the exact count.
		TotalValues: result.TotalValues,
		TookMs:      result.Took,
	}, nil
}

// platformQueryParams maps a platform logs query request onto the client's parameters. Both
// the record query and the filter-values query take the same request shape, so the mapping
// lives here rather than in each handler.
func platformQueryParams(body *gen.PlatformLogsQueryRequest) dynatrace.PlatformLogsParams {
	params := dynatrace.PlatformLogsParams{
		StartTime:        body.StartTime,
		EndTime:          body.EndTime,
		ClusterInstances: derefSlice(body.ClusterInstance),
		Namespaces:       derefSlice(body.Namespace),
		PodNames:         derefSlice(body.PodName),
		ContainerNames:   derefSlice(body.ContainerName),
	}
	if body.Labels != nil {
		params.Labels = *body.Labels
	}
	if body.Limit != nil {
		params.Limit = *body.Limit
	}
	if body.SortOrder != nil {
		params.SortOrder = string(*body.SortOrder)
	}
	if body.SearchPhrase != nil {
		params.SearchPhrase = *body.SearchPhrase
	}
	if body.LogLevels != nil {
		levels := make([]string, len(*body.LogLevels))
		for i, l := range *body.LogLevels {
			levels[i] = string(l)
		}
		params.LogLevels = levels
	}
	return params
}

// derefSlice returns the pointed-to slice, or nil. An absent multi-value filter and an
// empty one mean the same thing here: not a filter.
func derefSlice(v *[]string) []string {
	if v == nil {
		return nil
	}
	return *v
}

// firstInvalidLabelKey reports the first label key that is not a well-formed Kubernetes
// label key, if any.
//
// Labels are matched as the literal "key=value", so a key holding "=" would shift where the
// value begins. A malformed key is refused here rather than silently dropped: dropping it
// would widen the query, returning records the caller did not ask for.
func firstInvalidLabelKey(body *gen.PlatformLogsQueryRequest) (string, bool) {
	if body.Labels == nil {
		return "", true
	}
	// Sorted so the key reported is the same one on every call for a given request.
	keys := make([]string, 0, len(*body.Labels))
	for k := range *body.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !dynatrace.IsValidLabelKey(k) {
			return k, false
		}
	}
	return "", true
}
