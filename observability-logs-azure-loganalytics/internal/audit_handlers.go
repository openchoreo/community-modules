// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

// auditFilterColumns is closed on purpose: a filter name reaches a KQL column
// name, so an unrecognised one is refused with a 400 rather than passed through.
var auditFilterColumns = map[gen.AuditLogFilterValuesRequestFilter]loganalytics.AuditFilter{
	gen.Action:              loganalytics.AuditFilterAction,
	gen.ActorEntitlements:   loganalytics.AuditFilterActorEntitlements,
	gen.ActorId:             loganalytics.AuditFilterActorID,
	gen.ActorIssuer:         loganalytics.AuditFilterActorIssuer,
	gen.ActorSessionId:      loganalytics.AuditFilterActorSessionID,
	gen.ActorType:           loganalytics.AuditFilterActorType,
	gen.Category:            loganalytics.AuditFilterCategory,
	gen.OperationId:         loganalytics.AuditFilterOperationID,
	gen.Producer:            loganalytics.AuditFilterProducer,
	gen.ResourceComponent:   loganalytics.AuditFilterResourceComponent,
	gen.ResourceEnvironment: loganalytics.AuditFilterResourceEnvironment,
	gen.ResourceName:        loganalytics.AuditFilterResourceName,
	gen.ResourceNamespace:   loganalytics.AuditFilterResourceNamespace,
	gen.ResourceProject:     loganalytics.AuditFilterResourceProject,
	gen.ResourceResource:    loganalytics.AuditFilterResourceResource,
	gen.ResourceType:        loganalytics.AuditFilterResourceType,
	gen.Result:              loganalytics.AuditFilterResult,
	gen.SourceIp:            loganalytics.AuditFilterSourceIP,
	gen.Surface:             loganalytics.AuditFilterSurface,
	gen.UserAgent:           loganalytics.AuditFilterUserAgent,
}

// auditReleaseLineUnsupported is the 501 message for a release line audit
// filter: the audit table has no release line column to filter on.
const auditReleaseLineUnsupported = "filtering audit logs by release line is not supported by this adapter"

// QueryAuditLogs handles POST /api/v1alpha1/audit-logs/query.
func (h *LogsHandler) QueryAuditLogs(
	ctx context.Context, request gen.QueryAuditLogsRequestObject,
) (gen.QueryAuditLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryAuditLogs400JSONResponse(
			makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}

	params, msg := auditParamsFromRequest(*request.Body)
	if msg != "" {
		return gen.QueryAuditLogs400JSONResponse(
			makeError(gen.BadRequest, errCodeBadRequest, msg)), nil
	}
	if filtersByReleaseLine(*request.Body) {
		return gen.QueryAuditLogs501JSONResponse(
			makeError(gen.NotImplemented, errCodeNotImplemented, auditReleaseLineUnsupported)), nil
	}

	result, err := h.client.GetAuditLogs(ctx, params)
	if err != nil {
		if errors.Is(err, loganalytics.ErrAuditNotConfigured) {
			return gen.QueryAuditLogs501JSONResponse(makeError(gen.NotImplemented, errCodeNotImplemented,
				"audit logs are not configured for this adapter")), nil
		}
		h.logger.Error("audit logs query failed", slog.Any("error", err))
		return gen.QueryAuditLogs500JSONResponse(
			makeError(gen.InternalServerError, errCodeInternal, "failed to query audit logs")), nil
	}

	records := make([]gen.AuditLogRecord, 0, len(result.Records))
	for _, r := range result.Records {
		records = append(records, toGenAuditRecord(r))
	}

	return gen.QueryAuditLogs200JSONResponse(gen.AuditLogsResponse{
		Records:  records,
		Total:    result.Total,
		Timeline: toGenAuditTimeline(result.Timeline),
		TookMs:   int64(result.TookMs),
	}), nil
}

// QueryAuditLogFilterValues handles POST /api/v1alpha1/audit-logs/filter-values.
func (h *LogsHandler) QueryAuditLogFilterValues(
	ctx context.Context, request gen.QueryAuditLogFilterValuesRequestObject,
) (gen.QueryAuditLogFilterValuesResponseObject, error) {
	if request.Body == nil {
		return gen.QueryAuditLogFilterValues400JSONResponse(
			makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}

	if request.Body.Filter == gen.ResourceReleaseLine {
		return gen.QueryAuditLogFilterValues501JSONResponse(
			makeError(gen.NotImplemented, errCodeNotImplemented, auditReleaseLineUnsupported)), nil
	}
	filter, ok := auditFilterColumns[request.Body.Filter]
	if !ok || !loganalytics.IsListableAuditFilter(filter) {
		return gen.QueryAuditLogFilterValues400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest,
			"filter "+string(request.Body.Filter)+" cannot have its values listed")), nil
	}

	query, msg := auditParamsFromRequest(request.Body.Query)
	if msg != "" {
		return gen.QueryAuditLogFilterValues400JSONResponse(
			makeError(gen.BadRequest, errCodeBadRequest, msg)), nil
	}
	if filtersByReleaseLine(request.Body.Query) {
		return gen.QueryAuditLogFilterValues501JSONResponse(
			makeError(gen.NotImplemented, errCodeNotImplemented, auditReleaseLineUnsupported)), nil
	}
	// The page size and histogram say nothing about a value list.
	query.Limit = 0
	query.IncludeTimeline = false

	valueSearch := derefString(request.Body.ValueSearch)
	// The observer caps this too, but an adapter does not assume it is the only
	// caller. Counted in runes, the contract's maxLength being characters.
	if utf8.RuneCountInString(valueSearch) > maxValueSearchLength {
		return gen.QueryAuditLogFilterValues400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest,
			fmt.Sprintf("valueSearch cannot exceed %d characters", maxValueSearchLength))), nil
	}

	maxValues := loganalytics.DefaultAuditMaxValues
	if request.Body.MaxValues != nil {
		maxValues = *request.Body.MaxValues
	}
	if maxValues < 1 {
		return gen.QueryAuditLogFilterValues400JSONResponse(
			makeError(gen.BadRequest, errCodeBadRequest, "maxValues must be greater than 0")), nil
	}
	// Clamped rather than refused: asking for more values than exist is not a
	// mistake.
	if maxValues > loganalytics.MaxAuditMaxValues {
		maxValues = loganalytics.MaxAuditMaxValues
	}

	result, err := h.client.GetAuditLogFilterValues(ctx, loganalytics.AuditFilterValuesParams{
		Query:       query,
		Filter:      filter,
		ValueSearch: valueSearch,
		MaxValues:   maxValues,
	})
	if err != nil {
		if errors.Is(err, loganalytics.ErrAuditNotConfigured) {
			return gen.QueryAuditLogFilterValues501JSONResponse(makeError(gen.NotImplemented, errCodeNotImplemented,
				"audit logs are not configured for this adapter")), nil
		}
		h.logger.Error("audit log filter values query failed", slog.Any("error", err))
		return gen.QueryAuditLogFilterValues500JSONResponse(
			makeError(gen.InternalServerError, errCodeInternal, "failed to query audit log filter values")), nil
	}

	values := make([]gen.AuditLogFilterValue, 0, len(result.Values))
	for _, v := range result.Values {
		values = append(values, gen.AuditLogFilterValue{Value: v.Value, Count: v.Count})
	}

	return gen.QueryAuditLogFilterValues200JSONResponse(gen.AuditLogFilterValuesResponse{
		Filter:      string(request.Body.Filter),
		Values:      values,
		TotalValues: result.TotalValues,
		TookMs:      int64(result.TookMs),
	}), nil
}

// auditParamsFromRequest returns a non-empty message when the request is
// malformed, which the caller turns into a 400.
func auditParamsFromRequest(body gen.AuditLogsQueryRequest) (loganalytics.AuditParams, string) {
	// Required by the contract, but nothing validates the request before this,
	// so an omitted time arrives as year 1 and passes the ordering check below.
	if body.StartTime.IsZero() {
		return loganalytics.AuditParams{}, "startTime is required"
	}
	if body.EndTime.IsZero() {
		return loganalytics.AuditParams{}, "endTime is required"
	}
	if body.EndTime.Before(body.StartTime) {
		return loganalytics.AuditParams{}, "endTime must be greater than or equal to startTime"
	}

	limit := loganalytics.DefaultAuditLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	if limit < 1 || limit > loganalytics.MaxAuditLimit {
		return loganalytics.AuditParams{}, "limit must be between 1 and 1000"
	}

	sortOrder := loganalytics.SortDesc
	if body.SortOrder != nil {
		switch *body.SortOrder {
		case gen.AuditLogsQueryRequestSortOrderAsc:
			sortOrder = loganalytics.SortAsc
		case gen.AuditLogsQueryRequestSortOrderDesc:
			sortOrder = loganalytics.SortDesc
		default:
			return loganalytics.AuditParams{}, "sortOrder must be asc or desc"
		}
	}

	p := loganalytics.AuditParams{
		StartTime:        body.StartTime,
		EndTime:          body.EndTime,
		Limit:            limit,
		SortOrder:        sortOrder,
		SearchPhrase:     derefString(body.SearchPhrase),
		EventIDs:         derefSlice(body.EventId),
		RequestIDs:       derefSlice(body.RequestId),
		Actions:          derefSlice(body.Action),
		SourceIPs:        derefSlice(body.SourceIp),
		UserAgents:       derefSlice(body.UserAgent),
		Producers:        derefSlice(body.Producer),
		OperationIDs:     derefSlice(body.OperationId),
		IncludeTimeline:  body.IncludeTimeline != nil && *body.IncludeTimeline,
		TimelineInterval: derefString(body.TimelineInterval),
	}

	if body.Category != nil {
		for _, c := range *body.Category {
			p.Categories = append(p.Categories, string(c))
		}
	}
	if body.Result != nil {
		for _, r := range *body.Result {
			p.Results = append(p.Results, string(r))
		}
	}
	if body.Surface != nil {
		for _, s := range *body.Surface {
			p.Surfaces = append(p.Surfaces, string(s))
		}
	}

	if a := body.Actor; a != nil {
		p.ActorTypes = derefSlice(a.Type)
		p.ActorIDs = derefSlice(a.Id)
		p.ActorIssuers = derefSlice(a.Issuer)
		p.ActorSessionIDs = derefSlice(a.SessionId)
		p.ActorEntitlements = derefSlice(a.Entitlements)
	}

	if r := body.Resource; r != nil {
		p.ResourceTypes = derefSlice(r.Type)
		p.ResourceNamespaces = derefSlice(r.Namespace)
		p.ResourceEnvironments = derefSlice(r.Environment)
		p.ResourceProjects = derefSlice(r.Project)
		p.ResourceComponents = derefSlice(r.Component)
		p.ResourceResources = derefSlice(r.Resource)
		p.ResourceNames = derefSlice(r.Name)
	}

	return p, ""
}

// filtersByReleaseLine reports whether the query filters on
// resource.release_line.
func filtersByReleaseLine(body gen.AuditLogsQueryRequest) bool {
	return body.Resource != nil && len(derefSlice(body.Resource.ReleaseLine)) > 0
}

func toGenAuditRecord(r loganalytics.AuditRecord) gen.AuditLogRecord {
	out := gen.AuditLogRecord{
		SchemaVersion: r.SchemaVersion,
		EventId:       r.EventID,
		EventTime:     r.EventTime,
		Action:        r.Action,
		Category:      r.Category,
		Result:        r.Result,
		Actor: gen.AuditLogActor{
			Type:      r.Actor.Type,
			Id:        r.Actor.ID,
			Issuer:    ptrStringNonEmpty(r.Actor.Issuer),
			SessionId: ptrStringNonEmpty(r.Actor.SessionID),
		},
		RequestId:   ptrStringNonEmpty(r.RequestID),
		SourceIp:    ptrStringNonEmpty(r.SourceIP),
		UserAgent:   ptrStringNonEmpty(r.UserAgent),
		Producer:    ptrStringNonEmpty(r.Producer),
		Surface:     ptrStringNonEmpty(r.Surface),
		OperationId: ptrStringNonEmpty(r.OperationID),
	}

	if len(r.Actor.Entitlements) > 0 {
		e := r.Actor.Entitlements
		out.Actor.Entitlements = &e
	}
	if len(r.Metadata) > 0 {
		m := r.Metadata
		out.Metadata = &m
	}
	if r.HTTP != nil {
		out.Http = &gen.AuditLogHTTPInfo{
			Method: ptrStringNonEmpty(r.HTTP.Method),
			Path:   ptrStringNonEmpty(r.HTTP.Path),
		}
	}
	if r.Resource != nil {
		res := &gen.AuditLogResource{
			Type:        ptrStringNonEmpty(r.Resource.Type),
			Namespace:   ptrStringNonEmpty(r.Resource.Namespace),
			Environment: ptrStringNonEmpty(r.Resource.Environment),
			Project:     ptrStringNonEmpty(r.Resource.Project),
			Component:   ptrStringNonEmpty(r.Resource.Component),
			Resource:    ptrStringNonEmpty(r.Resource.Resource),
			Uid:         ptrStringNonEmpty(r.Resource.UID),
			Name:        ptrStringNonEmpty(r.Resource.Name),
		}
		if len(r.Resource.Metadata) > 0 {
			m := r.Resource.Metadata
			res.Metadata = &m
		}
		out.Resource = res
	}
	if r.Collector != nil {
		out.Collector = &gen.AuditLogCollectorInfo{
			NamespaceName: ptrStringNonEmpty(r.Collector.NamespaceName),
			PodName:       ptrStringNonEmpty(r.Collector.PodName),
			ContainerName: ptrStringNonEmpty(r.Collector.ContainerName),
		}
	}

	return out
}

func toGenAuditTimeline(t *loganalytics.AuditTimeline) *gen.AuditLogTimeline {
	if t == nil {
		return nil
	}
	buckets := make([]gen.AuditLogTimelineBucket, 0, len(t.Buckets))
	for _, b := range t.Buckets {
		bucket := gen.AuditLogTimelineBucket{
			StartTime: b.StartTime,
			Total:     b.Total,
		}
		if len(b.Counts) > 0 {
			c := b.Counts
			bucket.Counts = &c
		}
		buckets = append(buckets, bucket)
	}
	return &gen.AuditLogTimeline{Interval: t.Interval, Buckets: buckets}
}
