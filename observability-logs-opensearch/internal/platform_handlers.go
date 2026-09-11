// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

// QueryPlatformLogs implements POST /api/v1alpha1/platform-logs/query.
//
// Platform logs share the container-logs-* index with workload logs; what distinguishes
// them is the filter, not the store. There is no scope to resolve here - the observer has
// already decided who may ask - so this maps the request onto a query and back.
func (h *LogsHandler) QueryPlatformLogs(
	ctx context.Context, request gen.QueryPlatformLogsRequestObject,
) (gen.QueryPlatformLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryPlatformLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}
	body := request.Body

	startTime := body.StartTime.Format(time.RFC3339)
	endTime := body.EndTime.Format(time.RFC3339)

	params := opensearch.PlatformLogsQueryParams{
		StartTime:        startTime,
		EndTime:          endTime,
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

	query := h.queryBuilder.BuildPlatformLogsQuery(params)

	indices, err := h.queryBuilder.GenerateIndices(startTime, endTime)
	if err != nil {
		h.logger.Error("Failed to generate indices",
			slog.String("function", "QueryPlatformLogs"),
			slog.Any("error", err),
		)
		return gen.QueryPlatformLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	result, err := h.osClient.Search(ctx, indices, query)
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

	logs := make([]gen.PlatformLog, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		// timestamp and log are required by the contract, and this module owns those
		// guarantees. A document reaching here should satisfy both - the query
		// range-filters on @timestamp, and the index holds container logs - so a parse
		// failure means the document is malformed. Emitting it would put
		// 0001-01-01T00:00:00Z on the wire as a real reading, or a blank line that was
		// never logged, so skip it and say which document and why.
		entry, err := opensearch.ParsePlatformLogEntry(hit)
		if err != nil {
			h.logger.Warn("skipping malformed log document", "docId", hit.ID, "error", err)
			continue
		}

		log := gen.PlatformLog{
			Timestamp:       entry.Timestamp,
			Log:             entry.Log,
			Level:           optional(entry.LogLevel),
			ClusterInstance: optional(entry.ClusterInstance),
			NamespaceName:   optional(entry.NamespaceName),
			PodName:         optional(entry.PodName),
			ContainerName:   optional(entry.ContainerName),
			PodIp:           optional(entry.PodIP),
			NodeName:        optional(entry.NodeName),
			ContainerImage:  optional(entry.ContainerImage),
		}
		if len(entry.Labels) > 0 {
			labels := entry.Labels
			log.Labels = &labels
		}
		logs = append(logs, log)
	}

	return gen.QueryPlatformLogs200JSONResponse{
		Logs:   logs,
		Total:  result.Hits.Total.Value,
		TookMs: result.Took,
	}, nil
}

// derefSlice returns the pointed-to slice, or nil. An absent multi-value filter and an
// empty one mean the same thing here: not a filter.
func derefSlice(v *[]string) []string {
	if v == nil {
		return nil
	}
	return *v
}

// optional returns a pointer to s, or nil when s is empty, so an unknown field is omitted
// from the response rather than serialised as "".
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
