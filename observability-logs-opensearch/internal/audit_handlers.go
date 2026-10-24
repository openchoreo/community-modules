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

// Contract ceilings. The generated server does not enforce them, so an over-large
// value is clamped rather than passed to OpenSearch.
const (
	maxAuditLimit            = 1000
	defaultAuditFilterValues = 100
	maxAuditFilterValues     = 1000
)

// auditFilterFields maps a contract filter name onto the field it is stored at.
var auditFilterFields = map[gen.AuditLogFilterValuesRequestFilter]string{
	"actor.id":         "actor.id",
	"actor.type":       "actor.type",
	"actor.issuer":     "actor.issuer",
	"actor.session_id": "actor.session_id",
	// The claim key varies by subject kind, so this reads the index template's copy.
	"actor.entitlements":    opensearch.AuditEntitlementValuesField,
	"resource.type":         "resource.type",
	"resource.namespace":    "resource.namespace",
	"resource.environment":  "resource.environment",
	"resource.project":      "resource.project",
	"resource.component":    "resource.component",
	"resource.release_line": "resource.release_line",
	"resource.resource":     "resource.resource",
	"resource.name":         "resource.name",
	"action":                "action",
	"category":              "category",
	"result":                "result",
	"producer":              "producer",
	"surface":               "surface",
	"operation_id":          "operation_id",
	"source_ip":             "source_ip",
	"user_agent":            "user_agent",
}

// QueryAuditLogs implements POST /api/v1alpha1/audit-logs/query.
func (h *LogsHandler) QueryAuditLogs(
	ctx context.Context, request gen.QueryAuditLogsRequestObject,
) (gen.QueryAuditLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryAuditLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}
	body := request.Body

	if !body.EndTime.After(body.StartTime) {
		return gen.QueryAuditLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("endTime must be after startTime"),
		}, nil
	}

	params := toAuditLogsQueryParams(body)
	query := h.auditQueryBuilder.BuildAuditLogsQuery(params)

	// The timeline covers the whole window rather than the returned page.
	timelineInterval := ""
	if body.IncludeTimeline != nil && *body.IncludeTimeline {
		requested := ""
		if body.TimelineInterval != nil {
			requested = *body.TimelineInterval
		}
		resolved, err := opensearch.ResolveTimelineInterval(requested, body.StartTime, body.EndTime)
		if err != nil {
			return gen.QueryAuditLogs400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr(err.Error()),
			}, nil
		}
		timelineInterval = resolved
		query["aggs"] = opensearch.BuildAuditTimelineAgg(
			timelineInterval, params.StartTime, params.EndTime)
	}

	indices := []string{h.auditQueryBuilder.AuditIndexPattern()}
	result, err := h.osClient.Search(ctx, indices, query)
	if err != nil {
		h.logger.Error("Failed to query audit logs",
			slog.String("function", "QueryAuditLogs"),
			slog.Any("error", err),
		)
		return gen.QueryAuditLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	records := make([]gen.AuditLogRecord, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		record, err := opensearch.ParseAuditRecord(hit)
		if err != nil {
			h.logger.Warn("Skipping malformed audit document",
				slog.String("docId", hit.ID),
				slog.Any("error", err),
			)
			continue
		}
		records = append(records, toGenAuditLogRecord(record))
	}

	// Total counts the whole window, not this page.
	response := gen.AuditLogsResponse{
		Records: records,
		Total:   int64(result.Hits.Total.Value),
		TookMs:  int64(result.Took),
	}

	if timelineInterval != "" {
		timeline, err := opensearch.ParseAuditTimeline(result.Aggregations, timelineInterval)
		if err != nil {
			// The records are in hand, so drop the timeline rather than fail the query.
			h.logger.Warn("Failed to parse audit timeline",
				slog.String("function", "QueryAuditLogs"),
				slog.Any("error", err),
			)
		} else if timeline != nil {
			response.Timeline = toGenAuditTimeline(timeline)
		}
	}

	return gen.QueryAuditLogs200JSONResponse(response), nil
}

// QueryAuditLogFilterValues implements POST /api/v1alpha1/audit-logs/filter-values.
func (h *LogsHandler) QueryAuditLogFilterValues(
	ctx context.Context, request gen.QueryAuditLogFilterValuesRequestObject,
) (gen.QueryAuditLogFilterValuesResponseObject, error) {
	if request.Body == nil {
		return gen.QueryAuditLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}
	body := request.Body

	field, ok := auditFilterFields[body.Filter]
	if !ok {
		return gen.QueryAuditLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("unknown filter: " + string(body.Filter)),
		}, nil
	}

	if !body.Query.EndTime.After(body.Query.StartTime) {
		return gen.QueryAuditLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("query.endTime must be after query.startTime"),
		}, nil
	}

	// Dropping the filter's own selections keeps the picker offering alternatives to
	// what is already selected.
	params := toAuditLogsQueryParams(&body.Query)
	clearAuditFilter(&params, body.Filter)

	valueSearch := ""
	if body.ValueSearch != nil {
		valueSearch = *body.ValueSearch
	}
	maxValues := defaultAuditFilterValues
	if body.MaxValues != nil && *body.MaxValues > 0 {
		maxValues = min(*body.MaxValues, maxAuditFilterValues)
	}

	query := h.auditQueryBuilder.BuildAuditFilterValuesQuery(params, field, valueSearch, maxValues)

	indices := []string{h.auditQueryBuilder.AuditIndexPattern()}
	result, err := h.osClient.Search(ctx, indices, query)
	if err != nil {
		h.logger.Error("Failed to query audit log filter values",
			slog.String("function", "QueryAuditLogFilterValues"),
			slog.String("filter", string(body.Filter)),
			slog.Any("error", err),
		)
		return gen.QueryAuditLogFilterValues500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	values, totalValues, err := opensearch.ParseAuditFilterValues(result.Aggregations)
	if err != nil {
		h.logger.Error("Failed to parse audit log filter values",
			slog.String("function", "QueryAuditLogFilterValues"),
			slog.Any("error", err),
		)
		return gen.QueryAuditLogFilterValues500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	// Covers a search too long to push down as an aggregation regex.
	values = opensearch.FilterValuesBySearch(values, valueSearch)

	genValues := make([]gen.AuditLogFilterValue, 0, len(values))
	for _, v := range values {
		genValues = append(genValues, gen.AuditLogFilterValue{Value: v.Value, Count: v.Count})
	}

	return gen.QueryAuditLogFilterValues200JSONResponse{
		Filter:      string(body.Filter),
		Values:      genValues,
		TotalValues: totalValues,
		TookMs:      int64(result.Took),
	}, nil
}

func toAuditLogsQueryParams(body *gen.AuditLogsQueryRequest) opensearch.AuditLogsQueryParams {
	params := opensearch.AuditLogsQueryParams{
		StartTime:    body.StartTime.Format(time.RFC3339Nano),
		EndTime:      body.EndTime.Format(time.RFC3339Nano),
		Actions:      derefSlice(body.Action),
		Producers:    derefSlice(body.Producer),
		OperationIDs: derefSlice(body.OperationId),
		RequestIDs:   derefSlice(body.RequestId),
		EventIDs:     derefSlice(body.EventId),
		SourceIPs:    derefSlice(body.SourceIp),
		UserAgents:   derefSlice(body.UserAgent),
		Limit:        100,
		SortOrder:    "desc",
	}

	if body.Actor != nil {
		params.ActorIDs = derefSlice(body.Actor.Id)
		params.ActorTypes = derefSlice(body.Actor.Type)
		params.ActorIssuers = derefSlice(body.Actor.Issuer)
		params.ActorSessionIDs = derefSlice(body.Actor.SessionId)
		params.ActorEntitlements = derefSlice(body.Actor.Entitlements)
	}

	if body.Resource != nil {
		params.ResourceTypes = derefSlice(body.Resource.Type)
		params.ResourceNamespaces = derefSlice(body.Resource.Namespace)
		params.ResourceEnvironments = derefSlice(body.Resource.Environment)
		params.ResourceProjects = derefSlice(body.Resource.Project)
		params.ResourceComponents = derefSlice(body.Resource.Component)
		params.ResourceReleaseLines = derefSlice(body.Resource.ReleaseLine)
		params.ResourceResources = derefSlice(body.Resource.Resource)
		params.ResourceNames = derefSlice(body.Resource.Name)
	}

	if body.Returned != nil && body.Returned.Items != nil {
		params.ReturnedItemNames = derefSlice(body.Returned.Items.Name)
	}

	if body.Category != nil {
		for _, c := range *body.Category {
			params.Categories = append(params.Categories, string(c))
		}
	}
	if body.Result != nil {
		for _, r := range *body.Result {
			params.Results = append(params.Results, string(r))
		}
	}
	if body.Surface != nil {
		for _, s := range *body.Surface {
			params.Surfaces = append(params.Surfaces, string(s))
		}
	}

	if body.SearchPhrase != nil {
		params.SearchPhrase = *body.SearchPhrase
	}
	if body.Limit != nil && *body.Limit > 0 {
		params.Limit = min(*body.Limit, maxAuditLimit)
	}
	if body.SortOrder != nil {
		params.SortOrder = string(*body.SortOrder)
	}

	return params
}

// clearAuditFilter drops one filter's own selections, for the value picker.
func clearAuditFilter(
	params *opensearch.AuditLogsQueryParams, filter gen.AuditLogFilterValuesRequestFilter,
) {
	switch filter {
	case "actor.id":
		params.ActorIDs = nil
	case "actor.type":
		params.ActorTypes = nil
	case "actor.issuer":
		params.ActorIssuers = nil
	case "actor.session_id":
		params.ActorSessionIDs = nil
	case "actor.entitlements":
		params.ActorEntitlements = nil
	case "resource.type":
		params.ResourceTypes = nil
	case "resource.namespace":
		params.ResourceNamespaces = nil
	case "resource.environment":
		params.ResourceEnvironments = nil
	case "resource.project":
		params.ResourceProjects = nil
	case "resource.component":
		params.ResourceComponents = nil
	case "resource.release_line":
		params.ResourceReleaseLines = nil
	case "resource.resource":
		params.ResourceResources = nil
	case "resource.name":
		params.ResourceNames = nil
	case "action":
		params.Actions = nil
	case "category":
		params.Categories = nil
	case "result":
		params.Results = nil
	case "producer":
		params.Producers = nil
	case "surface":
		params.Surfaces = nil
	case "operation_id":
		params.OperationIDs = nil
	case "source_ip":
		params.SourceIPs = nil
	case "user_agent":
		params.UserAgents = nil
	}
}

func toGenAuditLogRecord(r opensearch.AuditRecord) gen.AuditLogRecord {
	record := gen.AuditLogRecord{
		SchemaVersion: r.SchemaVersion,
		EventId:       r.EventID,
		EventTime:     r.EventTime,
		Actor: gen.AuditLogActor{
			Type:      r.Actor.Type,
			Id:        r.Actor.ID,
			Issuer:    optional(r.Actor.Issuer),
			SessionId: optional(r.Actor.SessionID),
		},
		Action:      r.Action,
		Category:    r.Category,
		Result:      r.Result,
		RequestId:   optional(r.RequestID),
		SourceIp:    optional(r.SourceIP),
		UserAgent:   optional(r.UserAgent),
		Producer:    optional(r.Producer),
		Surface:     optional(r.Surface),
		OperationId: optional(r.OperationID),
	}

	if len(r.Actor.Entitlements) > 0 {
		entitlements := r.Actor.Entitlements
		record.Actor.Entitlements = &entitlements
	}
	if len(r.Metadata) > 0 {
		metadata := r.Metadata
		record.Metadata = &metadata
	}
	if r.HTTP != nil {
		record.Http = &gen.AuditLogHTTPInfo{
			Method: optional(r.HTTP.Method),
			Path:   optional(r.HTTP.Path),
		}
	}
	if r.Resource != nil {
		resource := gen.AuditLogResource{
			Type:        optional(r.Resource.Type),
			Namespace:   optional(r.Resource.Namespace),
			Environment: optional(r.Resource.Environment),
			Project:     optional(r.Resource.Project),
			Component:   optional(r.Resource.Component),
			ReleaseLine: optional(r.Resource.ReleaseLine),
			Resource:    optional(r.Resource.Resource),
			Uid:         optional(r.Resource.UID),
			Name:        optional(r.Resource.Name),
		}
		if len(r.Resource.Metadata) > 0 {
			metadata := r.Resource.Metadata
			resource.Metadata = &metadata
		}
		record.Resource = &resource
	}
	if r.Request != nil {
		request := gen.AuditLogRequest{}
		if r.Request.Parameters != nil {
			parameters := r.Request.Parameters
			request.Parameters = &parameters
		}
		record.Request = &request
	}
	if r.Returned != nil {
		returned := gen.AuditLogReturned{Count: r.Returned.Count}
		if r.Returned.Items != nil {
			items := make([]gen.AuditLogReturnedItem, 0, len(r.Returned.Items))
			for _, item := range r.Returned.Items {
				items = append(items, gen.AuditLogReturnedItem{
					Type:      item.Type,
					Namespace: optional(item.Namespace),
					Name:      item.Name,
					Uid:       optional(item.UID),
				})
			}
			returned.Items = &items
		}
		record.Returned = &returned
	}

	collector := gen.AuditLogCollectorInfo{
		NamespaceName: optional(r.Kubernetes.NamespaceName),
		PodName:       optional(r.Kubernetes.PodName),
		ContainerName: optional(r.Kubernetes.ContainerName),
	}
	if collector != (gen.AuditLogCollectorInfo{}) {
		record.Collector = &collector
	}

	return record
}

func toGenAuditTimeline(t *opensearch.AuditTimeline) *gen.AuditLogTimeline {
	buckets := make([]gen.AuditLogTimelineBucket, 0, len(t.Buckets))
	for _, b := range t.Buckets {
		bucket := gen.AuditLogTimelineBucket{
			StartTime: b.StartTime,
			Total:     b.Total,
		}
		if len(b.Counts) > 0 {
			counts := b.Counts
			bucket.Counts = &counts
		}
		buckets = append(buckets, bucket)
	}
	return &gen.AuditLogTimeline{Interval: t.Interval, Buckets: buckets}
}
