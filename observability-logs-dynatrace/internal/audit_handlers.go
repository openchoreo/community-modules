// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"

	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/dynatrace"
)

// Contract limits, clamped here because the generated server does not enforce them.
const (
	maxAuditLimit            = 1000
	defaultAuditFilterValues = 100
	maxAuditFilterValues     = 1000
)

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

	if msg := invalidAuditEnum(body); msg != "" {
		return gen.QueryAuditLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr(msg),
		}, nil
	}

	timelineInterval := ""
	if body.IncludeTimeline != nil && *body.IncludeTimeline {
		requested := ""
		if body.TimelineInterval != nil {
			requested = *body.TimelineInterval
		}
		resolved, err := dynatrace.ResolveTimelineInterval(requested, body.StartTime, body.EndTime)
		if err != nil {
			return gen.QueryAuditLogs400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr(err.Error()),
			}, nil
		}
		timelineInterval = resolved
	}

	result, err := h.backend.GetAuditLogs(ctx, toAuditLogsParams(body), timelineInterval)
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

	records := make([]gen.AuditLogRecord, 0, len(result.Records))
	for _, r := range result.Records {
		records = append(records, toGenAuditLogRecord(r))
	}

	response := gen.AuditLogsResponse{
		Records: records,
		Total:   result.Total,
		TookMs:  int64(result.Took),
	}
	if result.Timeline != nil {
		response.Timeline = toGenAuditTimeline(result.Timeline)
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

	filter := string(body.Filter)
	if !dynatrace.IsAuditFilter(filter) {
		return gen.QueryAuditLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("unknown filter: " + filter),
		}, nil
	}

	if !body.Query.EndTime.After(body.Query.StartTime) {
		return gen.QueryAuditLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("query.endTime must be after query.startTime"),
		}, nil
	}

	if msg := invalidAuditEnum(&body.Query); msg != "" {
		return gen.QueryAuditLogFilterValues400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr(msg),
		}, nil
	}

	params := toAuditLogsParams(&body.Query)
	clearAuditFilter(&params, body.Filter)

	valueSearch := ""
	if body.ValueSearch != nil {
		valueSearch = *body.ValueSearch
	}
	maxValues := defaultAuditFilterValues
	if body.MaxValues != nil && *body.MaxValues > 0 {
		maxValues = min(*body.MaxValues, maxAuditFilterValues)
	}

	result, err := h.backend.GetAuditLogFilterValues(ctx, params, filter, valueSearch, maxValues)
	if err != nil {
		h.logger.Error("Failed to query audit log filter values",
			slog.String("function", "QueryAuditLogFilterValues"),
			slog.String("filter", filter),
			slog.Any("error", err),
		)
		return gen.QueryAuditLogFilterValues500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	values := make([]gen.AuditLogFilterValue, 0, len(result.Values))
	for _, v := range result.Values {
		values = append(values, gen.AuditLogFilterValue{Value: v.Value, Count: v.Count})
	}

	return gen.QueryAuditLogFilterValues200JSONResponse{
		Filter:      filter,
		Values:      values,
		TotalValues: result.TotalValues,
		TookMs:      int64(result.Took),
	}, nil
}

// invalidAuditEnum reports the first closed-set field holding a value outside its
// declared enum, or "" when every supplied value is known. category, result, surface
// and sortOrder are documented in the OpenAPI contract as rejecting an unknown value
// with 400 rather than silently matching nothing.
func invalidAuditEnum(body *gen.AuditLogsQueryRequest) string {
	if body.Category != nil {
		for _, c := range *body.Category {
			switch c {
			case gen.Access, gen.Authorization, gen.Management:
			default:
				return "unknown category: " + string(c)
			}
		}
	}
	if body.Result != nil {
		for _, r := range *body.Result {
			switch r {
			case gen.AuditLogsQueryRequestResultDenied, gen.AuditLogsQueryRequestResultFailure,
				gen.AuditLogsQueryRequestResultSuccess:
			default:
				return "unknown result: " + string(r)
			}
		}
	}
	if body.Surface != nil {
		for _, s := range *body.Surface {
			switch s {
			case gen.Mcp, gen.Rest:
			default:
				return "unknown surface: " + string(s)
			}
		}
	}
	if body.SortOrder != nil {
		switch *body.SortOrder {
		case gen.AuditLogsQueryRequestSortOrderAsc, gen.AuditLogsQueryRequestSortOrderDesc:
		default:
			return "unknown sortOrder: " + string(*body.SortOrder)
		}
	}
	return ""
}

func toAuditLogsParams(body *gen.AuditLogsQueryRequest) dynatrace.AuditLogsParams {
	params := dynatrace.AuditLogsParams{
		StartTime:    body.StartTime,
		EndTime:      body.EndTime,
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
		params.ResourceResources = derefSlice(body.Resource.Resource)
		params.ResourceNames = derefSlice(body.Resource.Name)
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

// clearAuditFilter drops a filter's own selections so its picker still offers alternatives.
func clearAuditFilter(params *dynatrace.AuditLogsParams, filter gen.AuditLogFilterValuesRequestFilter) {
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

func toGenAuditLogRecord(r dynatrace.AuditRecord) gen.AuditLogRecord {
	record := gen.AuditLogRecord{
		SchemaVersion: r.SchemaVersion,
		EventId:       r.EventID,
		EventTime:     r.EventTime,
		Actor: gen.AuditLogActor{
			Type:      r.Actor.Type,
			Id:        r.Actor.ID,
			Issuer:    strPtr(r.Actor.Issuer),
			SessionId: strPtr(r.Actor.SessionID),
		},
		Action:      r.Action,
		Category:    r.Category,
		Result:      r.Result,
		RequestId:   strPtr(r.RequestID),
		SourceIp:    strPtr(r.SourceIP),
		UserAgent:   strPtr(r.UserAgent),
		Producer:    strPtr(r.Producer),
		Surface:     strPtr(r.Surface),
		OperationId: strPtr(r.OperationID),
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
			Method: strPtr(r.HTTP.Method),
			Path:   strPtr(r.HTTP.Path),
		}
	}
	if r.Resource != nil {
		resource := gen.AuditLogResource{
			Type:        strPtr(r.Resource.Type),
			Namespace:   strPtr(r.Resource.Namespace),
			Environment: strPtr(r.Resource.Environment),
			Project:     strPtr(r.Resource.Project),
			Component:   strPtr(r.Resource.Component),
			Resource:    strPtr(r.Resource.Resource),
			Uid:         strPtr(r.Resource.UID),
			Name:        strPtr(r.Resource.Name),
		}
		if len(r.Resource.Metadata) > 0 {
			metadata := r.Resource.Metadata
			resource.Metadata = &metadata
		}
		record.Resource = &resource
	}

	collector := gen.AuditLogCollectorInfo{
		NamespaceName: strPtr(r.Collector.NamespaceName),
		PodName:       strPtr(r.Collector.PodName),
		ContainerName: strPtr(r.Collector.ContainerName),
	}
	if collector != (gen.AuditLogCollectorInfo{}) {
		record.Collector = &collector
	}

	return record
}

func toGenAuditTimeline(t *dynatrace.AuditTimeline) *gen.AuditLogTimeline {
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
