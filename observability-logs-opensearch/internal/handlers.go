// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/observer"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

// LogsHandler implements the generated StrictServerInterface.
type LogsHandler struct {
	osClient           *opensearch.Client
	queryBuilder       *opensearch.QueryBuilder
	eventsQueryBuilder *opensearch.QueryBuilder
	auditQueryBuilder  *opensearch.QueryBuilder
	observerClient     *observer.Client
	logger             *slog.Logger
}

// NewLogsHandler creates a new LogsHandler.
func NewLogsHandler(
	osClient *opensearch.Client,
	queryBuilder *opensearch.QueryBuilder,
	eventsQueryBuilder *opensearch.QueryBuilder,
	auditQueryBuilder *opensearch.QueryBuilder,
	observerClient *observer.Client,
	logger *slog.Logger,
) *LogsHandler {
	return &LogsHandler{
		osClient:           osClient,
		queryBuilder:       queryBuilder,
		eventsQueryBuilder: eventsQueryBuilder,
		auditQueryBuilder:  auditQueryBuilder,
		observerClient:     observerClient,
		logger:             logger,
	}
}

// Ensure LogsHandler implements the interface at compile time.
var _ gen.StrictServerInterface = (*LogsHandler)(nil)

// Health implements the health check endpoint.
func (h *LogsHandler) Health(_ context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

// QueryLogs implements POST /api/v1/logs/query.
func (h *LogsHandler) QueryLogs(ctx context.Context, request gen.QueryLogsRequestObject) (gen.QueryLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	// Try to interpret the search scope as a WorkflowSearchScope first
	workflowScope, err := request.Body.SearchScope.AsWorkflowSearchScope()
	if err == nil && workflowScope.WorkflowRunName != nil {
		if strings.TrimSpace(workflowScope.Namespace) == "" {
			return gen.QueryLogs400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope with a valid namespace is required"),
			}, nil
		}

		return h.queryWorkflowLogs(ctx, request.Body, &workflowScope)
	}

	// Fall back to ComponentSearchScope
	scope, err := request.Body.SearchScope.AsComponentSearchScope()
	if err != nil || strings.TrimSpace(scope.Namespace) == "" {
		return gen.QueryLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("searchScope with a valid namespace is required"),
		}, nil
	}

	return h.queryComponentLogs(ctx, request.Body, &scope)
}

func (h *LogsHandler) queryComponentLogs(ctx context.Context, req *gen.LogsQueryRequest, scope *gen.ComponentSearchScope) (gen.QueryLogsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	params := opensearch.ComponentLogsQueryParamsV1{
		StartTime:     startTime,
		EndTime:       endTime,
		NamespaceName: scope.Namespace,
	}
	if scope.ProjectUid != nil {
		params.ProjectID = *scope.ProjectUid
	}
	if scope.EnvironmentUid != nil {
		params.EnvironmentID = *scope.EnvironmentUid
	}
	if scope.ComponentUid != nil {
		params.ComponentID = *scope.ComponentUid
	}
	if scope.ReleaseLineUid != nil {
		params.ReleaseLineID = *scope.ReleaseLineUid
	}
	if scope.DefaultLineOnly != nil {
		params.DefaultLineOnly = *scope.DefaultLineOnly
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}
	if req.SearchPhrase != nil {
		params.SearchPhrase = *req.SearchPhrase
	}
	if req.LogLevels != nil {
		levels := make([]string, len(*req.LogLevels))
		for i, l := range *req.LogLevels {
			levels[i] = string(l)
		}
		params.LogLevels = levels
	}

	query, err := h.queryBuilder.BuildComponentLogsQueryV1(params)
	if err != nil {
		h.logger.Error("Failed to build component logs query",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	indices, err := h.queryBuilder.GenerateIndices(startTime, endTime)
	if err != nil {
		h.logger.Error("Failed to generate indices",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	result, err := h.osClient.Search(ctx, indices, query)
	if err != nil {
		h.logger.Error("Failed to query component logs",
			slog.String("function", "QueryLogs"),
			slog.String("namespace", scope.Namespace),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	entries := make([]gen.ComponentLogEntry, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		logEntry := opensearch.ParseLogEntry(hit)
		entry := toComponentLogEntry(&logEntry)
		entries = append(entries, entry)
	}

	total := result.Hits.Total.Value
	took := result.Took
	resp := gen.LogsQueryResponse{
		Total:  &total,
		TookMs: &took,
	}
	logs := gen.LogsQueryResponse_Logs{}
	if err := logs.FromLogsQueryResponseLogs0(entries); err != nil {
		h.logger.Error("Failed to serialize component log entries",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	resp.Logs = &logs

	return gen.QueryLogs200JSONResponse{
		Body:    resp,
		Headers: gen.QueryLogs200ResponseHeaders{OpenChoreoReleaseLineApplied: releaseLineApplied(params.ReleaseLineID, params.ComponentID)},
	}, nil
}

func (h *LogsHandler) queryWorkflowLogs(ctx context.Context, req *gen.LogsQueryRequest, scope *gen.WorkflowSearchScope) (gen.QueryLogsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	queryParams := opensearch.QueryParams{
		StartTime:     startTime,
		EndTime:       endTime,
		NamespaceName: scope.Namespace,
		Limit:         100,
		SortOrder:     "desc",
	}
	if req.Limit != nil {
		queryParams.Limit = *req.Limit
	}
	if req.SortOrder != nil {
		queryParams.SortOrder = string(*req.SortOrder)
	}
	if req.SearchPhrase != nil {
		queryParams.SearchPhrase = *req.SearchPhrase
	}
	if req.LogLevels != nil {
		levels := make([]string, len(*req.LogLevels))
		for i, l := range *req.LogLevels {
			levels[i] = string(l)
		}
		queryParams.LogLevels = levels
	}

	workflowRunName := ""
	if scope.WorkflowRunName != nil {
		workflowRunName = *scope.WorkflowRunName
	}

	stepName := ""
	if scope.TaskName != nil {
		stepName = *scope.TaskName
	}

	params := opensearch.WorkflowRunQueryParams{
		QueryParams:   queryParams,
		WorkflowRunID: workflowRunName,
		StepName:      stepName,
	}

	query := h.queryBuilder.BuildWorkflowRunLogsQuery(params)

	indices, err := h.queryBuilder.GenerateIndices(startTime, endTime)
	if err != nil {
		h.logger.Error("Failed to generate indices",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	result, err := h.osClient.Search(ctx, indices, query)
	if err != nil {
		h.logger.Error("Failed to query workflow logs",
			slog.String("function", "QueryLogs"),
			slog.String("namespace", scope.Namespace),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	entries := make([]gen.WorkflowLogEntry, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var ts *time.Time
		if tsVal, ok := hit.Source["@timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, tsVal); err == nil {
				ts = &parsed
			}
		}
		log := ""
		if logVal, ok := hit.Source["log"].(string); ok {
			log = logVal
		}
		entry := gen.WorkflowLogEntry{
			Timestamp: ts,
			Log:       &log,
		}
		entries = append(entries, entry)
	}

	total := result.Hits.Total.Value
	took := result.Took
	resp := gen.LogsQueryResponse{
		Total:  &total,
		TookMs: &took,
	}
	logs := gen.LogsQueryResponse_Logs{}
	if err := logs.FromLogsQueryResponseLogs1(entries); err != nil {
		h.logger.Error("Failed to serialize workflow log entries",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	resp.Logs = &logs

	return gen.QueryLogs200JSONResponse{Body: resp}, nil
}

// QueryEvents implements POST /api/v1/events/query.
func (h *LogsHandler) QueryEvents(ctx context.Context, request gen.QueryEventsRequestObject) (gen.QueryEventsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryEvents400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}
	req := request.Body

	if req.SearchScope == nil {
		reasons := eventReasonsOf(req)
		if len(reasons) == 0 {
			return gen.QueryEvents400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope is required unless reasons is set for an unscoped sweep"),
			}, nil
		}
		return h.queryUnscopedEvents(ctx, req, reasons)
	}

	// Try to interpret the search scope as a WorkflowSearchScope first
	workflowScope, err := req.SearchScope.AsWorkflowSearchScope()
	if err == nil && workflowScope.WorkflowRunName != nil {
		if strings.TrimSpace(workflowScope.Namespace) == "" || strings.TrimSpace(*workflowScope.WorkflowRunName) == "" {
			return gen.QueryEvents400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope with a valid namespace and workflowRunName is required"),
			}, nil
		}

		return h.queryWorkflowEvents(ctx, req, &workflowScope)
	}

	// Fall back to ComponentSearchScope
	scope, err := req.SearchScope.AsComponentSearchScope()
	if err != nil || strings.TrimSpace(scope.Namespace) == "" {
		return gen.QueryEvents400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("searchScope with a valid namespace is required"),
		}, nil
	}

	return h.queryComponentEvents(ctx, req, &scope)
}

// eventReasonsOf returns the request's reasons filter, or nil when unset.
func eventReasonsOf(req *gen.EventsQueryRequest) []string {
	if req.Reasons == nil {
		return nil
	}
	return *req.Reasons
}

// eventLimitOf returns the request's limit, defaulting to 100 to match the
// query builders' own default (kept in sync so the "page was full" check in
// executeEventsQuery reflects the limit actually sent to OpenSearch).
func eventLimitOf(req *gen.EventsQueryRequest) int {
	if req.Limit != nil && *req.Limit > 0 {
		return *req.Limit
	}
	return 100
}

func (h *LogsHandler) queryComponentEvents(ctx context.Context, req *gen.EventsQueryRequest, scope *gen.ComponentSearchScope) (gen.QueryEventsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	limit := eventLimitOf(req)
	params := opensearch.EventsQueryParams{
		StartTime:     startTime,
		EndTime:       endTime,
		NamespaceName: scope.Namespace,
		Limit:         limit,
		Reasons:       eventReasonsOf(req),
	}
	if scope.ProjectUid != nil {
		params.ProjectID = *scope.ProjectUid
	}
	if scope.EnvironmentUid != nil {
		params.EnvironmentID = *scope.EnvironmentUid
	}
	if scope.ComponentUid != nil {
		params.ComponentID = *scope.ComponentUid
	}
	if scope.ReleaseLineUid != nil {
		params.ReleaseLineID = *scope.ReleaseLineUid
	}
	if scope.DefaultLineOnly != nil {
		params.DefaultLineOnly = *scope.DefaultLineOnly
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}

	query, err := h.eventsQueryBuilder.BuildComponentEventsQuery(params)
	if err != nil {
		h.logger.Error("Failed to build component events query",
			slog.String("function", "QueryEvents"),
			slog.Any("error", err),
		)
		return gen.QueryEvents500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	return h.executeEventsQuery(ctx, query, startTime, endTime, scope.Namespace, limit, releaseLineApplied(params.ReleaseLineID, params.ComponentID))
}

func (h *LogsHandler) queryWorkflowEvents(ctx context.Context, req *gen.EventsQueryRequest, scope *gen.WorkflowSearchScope) (gen.QueryEventsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	limit := eventLimitOf(req)
	params := opensearch.WorkflowEventsQueryParams{
		StartTime:     startTime,
		EndTime:       endTime,
		NamespaceName: scope.Namespace,
		Limit:         limit,
		Reasons:       eventReasonsOf(req),
	}
	if scope.WorkflowRunName != nil {
		params.WorkflowRunID = *scope.WorkflowRunName
	}
	if scope.TaskName != nil {
		params.TaskName = *scope.TaskName
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}

	query, err := h.eventsQueryBuilder.BuildWorkflowEventsQuery(params)
	if err != nil {
		h.logger.Error("Failed to build workflow events query",
			slog.String("function", "QueryEvents"),
			slog.Any("error", err),
		)
		return gen.QueryEvents500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	return h.executeEventsQuery(ctx, query, startTime, endTime, scope.Namespace, limit, "")
}

// queryUnscopedEvents runs a reason-filtered sweep across every namespace,
// for machine consumers (e.g. the Delivery Insights aggregator) reading
// controller-emitted events rather than interactively browsing one component.
func (h *LogsHandler) queryUnscopedEvents(ctx context.Context, req *gen.EventsQueryRequest, reasons []string) (gen.QueryEventsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	limit := eventLimitOf(req)
	params := opensearch.ReasonFilteredEventsQueryParams{
		StartTime: startTime,
		EndTime:   endTime,
		Reasons:   reasons,
		Limit:     limit,
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}

	query, err := h.eventsQueryBuilder.BuildReasonFilteredEventsQuery(params)
	if err != nil {
		h.logger.Error("Failed to build unscoped events query",
			slog.String("function", "QueryEvents"),
			slog.Any("error", err),
		)
		return gen.QueryEvents500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	return h.executeEventsQuery(ctx, query, startTime, endTime, "", limit, "")
}

// executeEventsQuery runs an events query against the events indices and assembles the response.
// limit is the size actually sent to OpenSearch; the query counts one past it, so a total greater
// than the events returned tells the caller the window holds more than it just read and must be
// resumed from the last event's timestamp rather than treated as read.
// releaseLineApplied is the OpenChoreo-Release-Line-Applied header value for a query scoped to a
// component or one of its release lines, which this adapter matches by line: "true" when it was,
// empty otherwise.
func releaseLineApplied(releaseLineID, componentID string) string {
	if releaseLineID == "" && componentID == "" {
		return ""
	}
	return "true"
}

func (h *LogsHandler) executeEventsQuery(
	ctx context.Context, query map[string]interface{}, startTime, endTime, namespace string, limit int,
	releaseLineHeader string,
) (gen.QueryEventsResponseObject, error) {
	indices, err := h.eventsQueryBuilder.GenerateIndices(startTime, endTime)
	if err != nil {
		h.logger.Error("Failed to generate event indices",
			slog.String("function", "QueryEvents"),
			slog.Any("error", err),
		)
		return gen.QueryEvents500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	result, err := h.osClient.Search(ctx, indices, query)
	if err != nil {
		h.logger.Error("Failed to query events",
			slog.String("function", "QueryEvents"),
			slog.String("namespace", namespace),
			slog.Any("error", err),
		)
		return gen.QueryEvents500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	hits := result.Hits.Hits
	// A page cut at limit can land inside a group of events sharing one
	// timestamp. The caller resumes at the last timestamp it was handed, so the
	// unreturned members of that group would never be read by anyone -- finish
	// the group before returning, even where that takes the page past limit.
	pageBeforeExtension := len(hits)
	if limit > 0 && len(hits) >= limit {
		completed, err := h.completeTimestampGroup(ctx, indices, query, hits, limit)
		if err != nil {
			h.logger.Error("Failed to complete the boundary timestamp group",
				slog.String("function", "QueryEvents"),
				slog.Any("error", err),
			)
			return gen.QueryEvents500JSONResponse{
				Title:   ptr(gen.InternalServerError),
				Message: ptr("internal server error"),
			}, nil
		}
		hits = completed
	}

	entries := make([]gen.EventEntry, 0, len(hits))
	for _, hit := range hits {
		eventEntry := opensearch.ParseEventHit(hit)
		entries = append(entries, toEventEntry(&eventEntry))
	}

	// total is how the caller learns whether it read the whole window: equal to the
	// events returned means it did, greater means the read stopped short. The query
	// asks OpenSearch to count to limit+1 (see eventsTrackTotalHits) so that
	// "exactly a full page" and "more than a page" are distinguishable; without
	// that the count saturates at 10000 and a caller could read a truncated window
	// as fully read.
	total := result.Hits.Total.Value
	// Completing the boundary group hands back more events than the search counted,
	// so that count no longer describes this page -- left alone it can report fewer
	// events than are in the body, and cannot say whether anything follows the
	// group. Re-count as far as the page actually returned.
	if len(hits) > pageBeforeExtension {
		recounted, err := h.countEvents(ctx, indices, query, len(hits)+1)
		if err != nil {
			h.logger.Error("Failed to re-count events after completing the boundary group",
				slog.String("function", "QueryEvents"),
				slog.Any("error", err),
			)
			return gen.QueryEvents500JSONResponse{
				Title:   ptr(gen.InternalServerError),
				Message: ptr("internal server error"),
			}, nil
		}
		total = recounted
	}
	took := result.Took
	resp := gen.EventsQueryResponse{
		Events: &entries,
		Total:  total,
		TookMs: &took,
	}

	return gen.QueryEvents200JSONResponse{
		Body:    resp,
		Headers: gen.QueryEvents200ResponseHeaders{OpenChoreoReleaseLineApplied: releaseLineHeader},
	}, nil
}

// maxBoundaryGroup bounds how far completeTimestampGroup will page a single
// timestamp group. It matches OpenSearch's default index.max_result_window, past
// which from+size is refused anyway. Reaching it needs more than 10000 events
// stamped at the same instant, which no real event stream produces.
const maxBoundaryGroup = 10000

// completeTimestampGroup extends a full page so it ends on a timestamp boundary.
//
// It re-reads every event bearing the page's last timestamp and appends the ones
// the page did not already carry, matched on document id. Returning a page that
// stops mid-group is what loses events: the caller resumes at that timestamp and
// the remainder falls between the two reads.
func (h *LogsHandler) completeTimestampGroup(
	ctx context.Context, indices []string, query map[string]interface{},
	hits []opensearch.Hit, limit int,
) ([]opensearch.Hit, error) {
	last := opensearch.ParseEventHit(hits[len(hits)-1])
	if last.Timestamp.IsZero() {
		// Nothing to bound the follow-up on; returning the page unchanged is no
		// worse than the read that produced it.
		return hits, nil
	}

	ts := last.Timestamp.UTC().Format(time.RFC3339Nano)
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		seen[hit.ID] = struct{}{}
	}

	// Page the follow-up: a group bigger than one page would be cut short by the
	// same truncation this is here to undo.
	for from := 0; from < maxBoundaryGroup; from += limit {
		boundaryQuery, err := h.eventsQueryBuilder.BuildEventsAtTimestampQuery(query, ts, limit, from)
		if err != nil {
			return nil, err
		}
		boundary, err := h.osClient.Search(ctx, indices, boundaryQuery)
		if err != nil {
			return nil, err
		}
		for _, hit := range boundary.Hits.Hits {
			if _, dup := seen[hit.ID]; dup {
				continue
			}
			seen[hit.ID] = struct{}{}
			hits = append(hits, hit)
		}
		if len(boundary.Hits.Hits) < limit {
			return hits, nil
		}
	}

	// Only reachable if more than maxBoundaryGroup events share one timestamp.
	// The page is returned as far as it was read; the re-count in the caller then
	// exceeds it, so the window reads as incomplete rather than as fully read.
	h.logger.Error("Boundary timestamp group exceeds the paging bound; the group may still be split",
		slog.String("function", "QueryEvents"),
		slog.String("timestamp", ts),
		slog.Int("bound", maxBoundaryGroup),
	)
	return hits, nil
}

// countEvents answers how many events the window holds, counted no further than
// trackTotalHits. size is zero: only the count is wanted.
func (h *LogsHandler) countEvents(
	ctx context.Context, indices []string, query map[string]interface{}, trackTotalHits int,
) (int, error) {
	countQuery, err := h.eventsQueryBuilder.BuildEventsCountQuery(query, trackTotalHits)
	if err != nil {
		return 0, err
	}
	counted, err := h.osClient.Search(ctx, indices, countQuery)
	if err != nil {
		return 0, err
	}
	return counted.Hits.Total.Value, nil
}

// CreateAlertRule implements POST /api/v1alpha1/alerts/rules.
func (h *LogsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	params := toAlertingRuleRequest(request.Body)

	monitorBody, err := h.queryBuilder.BuildLogAlertingRuleMonitorBody(params)
	if err != nil {
		h.logger.Error("Failed to build monitor body",
			slog.String("function", "CreateAlertRule"),
			slog.Any("error", err),
		)
		return gen.CreateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	alertID, _, err := h.osClient.CreateMonitor(ctx, monitorBody)
	if err != nil {
		h.logger.Error("Failed to create monitor",
			slog.String("function", "CreateAlertRule"),
			slog.Any("alertName", params.Metadata.Name),
			slog.Any("error", err),
		)
		return gen.CreateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.CreateAlertRule201JSONResponse{
		Body: gen.AlertingRuleSyncResponse{
			Action:        ptr(gen.Created),
			Status:        ptr(gen.Synced),
			RuleLogicalId: &params.Metadata.Name,
			RuleBackendId: &alertID,
			LastSyncedAt:  &now,
		},
		Headers: gen.CreateAlertRule201ResponseHeaders{
			OpenChoreoReleaseLineApplied: releaseLineApplied(params.Metadata.ReleaseLineUID, params.Metadata.ComponentUID),
		},
	}, nil
}

// DeleteAlertRule implements DELETE /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	monitorID, found, err := h.osClient.SearchMonitorByName(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to search for monitor",
			slog.String("function", "DeleteAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.DeleteAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	if !found {
		return gen.DeleteAlertRule404JSONResponse{
			Title:   ptr(gen.NotFound),
			Message: ptr("alert rule not found"),
		}, nil
	}

	if err := h.osClient.DeleteMonitor(ctx, monitorID); err != nil {
		h.logger.Error("Failed to delete monitor",
			slog.String("function", "DeleteAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.DeleteAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.DeleteAlertRule200JSONResponse{
		Action:        ptr(gen.Deleted),
		Status:        ptr(gen.Synced),
		RuleLogicalId: &request.RuleName,
		RuleBackendId: &monitorID,
		LastSyncedAt:  &now,
	}, nil
}

// GetAlertRule implements GET /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	monitorID, found, err := h.osClient.SearchMonitorByName(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to search for monitor",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	if !found {
		return gen.GetAlertRule404JSONResponse{
			Title:   ptr(gen.NotFound),
			Message: ptr("alert rule not found"),
		}, nil
	}

	monitor, err := h.osClient.GetMonitorByID(ctx, monitorID)
	if err != nil {
		h.logger.Error("Failed to get monitor",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	response, err := parseMonitorToAlertRuleResponse(monitor)
	if err != nil {
		h.logger.Error("Failed to parse monitor to alert rule response",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	return gen.GetAlertRule200JSONResponse(response), nil
}

// UpdateAlertRule implements PUT /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	monitorID, found, err := h.osClient.SearchMonitorByName(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to search for monitor",
			slog.String("function", "UpdateAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.UpdateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	if !found {
		return gen.UpdateAlertRule404JSONResponse{
			Title:   ptr(gen.NotFound),
			Message: ptr("alert rule not found"),
		}, nil
	}

	params := toAlertingRuleRequest(request.Body)

	monitorBody, err := h.queryBuilder.BuildLogAlertingRuleMonitorBody(params)
	if err != nil {
		h.logger.Error("Failed to build monitor body",
			slog.String("function", "UpdateAlertRule"),
			slog.Any("error", err),
		)
		return gen.UpdateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	if _, err := h.osClient.UpdateMonitor(ctx, monitorID, monitorBody); err != nil {
		h.logger.Error("Failed to update monitor",
			slog.String("function", "UpdateAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.UpdateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.UpdateAlertRule200JSONResponse{
		Body: gen.AlertingRuleSyncResponse{
			Action:        ptr(gen.Updated),
			Status:        ptr(gen.Synced),
			RuleLogicalId: &request.RuleName,
			RuleBackendId: &monitorID,
			LastSyncedAt:  &now,
		},
		Headers: gen.UpdateAlertRule200ResponseHeaders{
			OpenChoreoReleaseLineApplied: releaseLineApplied(params.Metadata.ReleaseLineUID, params.Metadata.ComponentUID),
		},
	}, nil
}

// HandleAlertWebhook implements POST /api/v1alpha1/alerts/webhook.
func (h *LogsHandler) HandleAlertWebhook(_ context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if request.Body == nil {
		h.logger.Warn("Alert webhook received with nil body")
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.AlertWebhookResponseStatusSuccess),
		}, nil
	}
	body := *request.Body

	ruleName, ruleNamespace, alertValue, alertTimestamp, err := parseAlertWebhookBody(body)
	if err != nil {
		h.logger.Error("Failed to parse alert webhook body", slog.Any("error", err))
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.AlertWebhookResponseStatusSuccess),
		}, nil
	}

	go func() {
		forwardCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := h.observerClient.ForwardAlert(forwardCtx, ruleName, ruleNamespace, alertValue, alertTimestamp); err != nil {
			h.logger.Error("Failed to forward alert webhook to observer API",
				slog.Any("error", err),
			)
		}
	}()

	return gen.HandleAlertWebhook200JSONResponse{
		Message: ptr("alert webhook received successfully"),
		Status:  ptr(gen.AlertWebhookResponseStatusSuccess),
	}, nil
}

// parseAlertWebhookBody extracts alert fields from the incoming OpenSearch alerting webhook body.
func parseAlertWebhookBody(body map[string]interface{}) (ruleName string, ruleNamespace string, alertValue float64, alertTimestamp time.Time, err error) {
	ruleNameVal, ok := body["ruleName"]
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("missing ruleName in webhook body")
	}
	ruleName, ok = ruleNameVal.(string)
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("ruleName is not a string")
	}

	if nsVal, ok := body["ruleNamespace"]; ok {
		if ns, ok := nsVal.(string); ok {
			ruleNamespace = ns
		}
	}

	if countVal, ok := body["alertValue"]; ok {
		switch v := countVal.(type) {
		case float64:
			alertValue = v
		case string:
			alertValue, err = strconv.ParseFloat(v, 64)
			if err != nil {
				return "", "", 0, time.Time{}, fmt.Errorf("failed to parse alertValue %q: %w", v, err)
			}
		}
	}

	if tsVal, ok := body["alertTimestamp"]; ok {
		switch v := tsVal.(type) {
		case string:
			parsed, parseErr := time.Parse(time.RFC3339, v)
			if parseErr != nil {
				alertTimestamp = time.Now()
			} else {
				alertTimestamp = parsed
			}
		default:
			alertTimestamp = time.Now()
		}
	} else {
		alertTimestamp = time.Now()
	}

	return ruleName, ruleNamespace, alertValue, alertTimestamp, nil
}

// uuidString returns the UUID as a string, empty when it is nil.
func uuidString(id *openapi_types.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// toAlertingRuleRequest converts the generated AlertRuleRequest to the internal type.
func toAlertingRuleRequest(req *gen.AlertRuleRequest) opensearch.AlertingRuleRequest {
	return opensearch.AlertingRuleRequest{
		Metadata: opensearch.AlertingRuleMetadata{
			Name:           req.Metadata.Name,
			Namespace:      req.Metadata.Namespace,
			ProjectUID:     req.Metadata.ProjectUid.String(),
			EnvironmentUID: req.Metadata.EnvironmentUid.String(),
			ComponentUID:   req.Metadata.ComponentUid.String(),
			ReleaseLineUID: uuidString(req.Metadata.ReleaseLineUid),
		},
		Source: opensearch.AlertingRuleSource{
			Query: req.Source.Query,
		},
		Condition: opensearch.AlertingRuleCondition{
			Enabled:   req.Condition.Enabled,
			Window:    req.Condition.Window,
			Interval:  req.Condition.Interval,
			Operator:  string(req.Condition.Operator),
			Threshold: float64(req.Condition.Threshold),
		},
	}
}

// parseMonitorToAlertRuleResponse parses an OpenSearch monitor to the API AlertRuleResponse.
func parseMonitorToAlertRuleResponse(monitor map[string]interface{}) (gen.AlertRuleResponse, error) {
	name := getStringFromMap(monitor, "name")

	// Extract metadata from the monitor's trigger action message template
	var namespace, projectUID, environmentUID, componentUID, searchQuery string
	var enabled bool
	var threshold float32
	var operator, window, interval string

	if enabledVal, ok := monitor["enabled"].(bool); ok {
		enabled = enabledVal
	}

	// Extract query and metadata from inputs
	if inputs, ok := monitor["inputs"].([]interface{}); ok && len(inputs) > 0 {
		if input, ok := inputs[0].(map[string]interface{}); ok {
			if search, ok := input["search"].(map[string]interface{}); ok {
				if queryMap, ok := search["query"].(map[string]interface{}); ok {
					searchQuery, namespace, projectUID, environmentUID, componentUID = extractQueryMetadata(queryMap)
				}
			}
		}
	}

	// Extract trigger condition
	if triggers, ok := monitor["triggers"].([]interface{}); ok && len(triggers) > 0 {
		if trigger, ok := triggers[0].(map[string]interface{}); ok {
			if qlt, ok := trigger["query_level_trigger"].(map[string]interface{}); ok {
				if componentUID == "" {
					componentUID = componentUIDFromTrigger(qlt)
				}
				if condition, ok := qlt["condition"].(map[string]interface{}); ok {
					if script, ok := condition["script"].(map[string]interface{}); ok {
						if source, ok := script["source"].(string); ok {
							operator, threshold = parseConditionScript(source)
						}
					}
				}
			}
		}
	}

	// Extract schedule for interval
	if schedule, ok := monitor["schedule"].(map[string]interface{}); ok {
		if period, ok := schedule["period"].(map[string]interface{}); ok {
			intervalVal := 0.0
			if v, ok := period["interval"].(float64); ok {
				intervalVal = v
			}
			unit := getStringFromMap(period, "unit")
			interval = formatScheduleToInterval(intervalVal, unit)
		}
	}

	// Extract window from query time range
	window = extractWindowFromQuery(monitor)

	operatorEnum := gen.AlertRuleResponseConditionOperator(opensearch.ReverseMapOperator(operator))

	metadata := &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{
		Name:      &name,
		Namespace: strPtr(namespace),
	}
	if projectUID != "" {
		if uid, ok := parseUUID(projectUID); ok {
			metadata.ProjectUid = &uid
		}
	}
	if environmentUID != "" {
		if uid, ok := parseUUID(environmentUID); ok {
			metadata.EnvironmentUid = &uid
		}
	}
	if componentUID != "" {
		if uid, ok := parseUUID(componentUID); ok {
			metadata.ComponentUid = &uid
		}
	}

	response := gen.AlertRuleResponse{
		Metadata: metadata,
		Source: &struct {
			Metric *gen.AlertRuleResponseSourceMetric `json:"metric,omitempty"`
			Query  *string                            `json:"query,omitempty"`
		}{
			Metric: ptr(gen.AlertRuleResponseSourceMetric("log")),
			Query:  &searchQuery,
		},
		Condition: &struct {
			Enabled   *bool                                   `json:"enabled,omitempty"`
			Interval  *string                                 `json:"interval,omitempty"`
			Operator  *gen.AlertRuleResponseConditionOperator `json:"operator,omitempty"`
			Threshold *float32                                `json:"threshold,omitempty"`
			Window    *string                                 `json:"window,omitempty"`
		}{
			Enabled:   &enabled,
			Operator:  &operatorEnum,
			Threshold: &threshold,
			Window:    &window,
			Interval:  &interval,
		},
	}

	return response, nil
}

// webhookComponentUID matches the component UID in a trigger's webhook message template.
var webhookComponentUID = regexp.MustCompile(`"componentUid":"([^"]+)"`)

// componentUIDFromTrigger reads the component UID from a trigger's webhook message. The rule's
// query does not always name it at the top level: a default line rule nests it in a bool filter, and
// a release line rule filters on the line alone.
func componentUIDFromTrigger(qlt map[string]interface{}) string {
	actions, _ := qlt["actions"].([]interface{})
	for _, a := range actions {
		action, _ := a.(map[string]interface{})
		tmpl, _ := action["message_template"].(map[string]interface{})
		if m := webhookComponentUID.FindStringSubmatch(getStringFromMap(tmpl, "source")); m != nil {
			return m[1]
		}
	}
	return ""
}

// extractQueryMetadata extracts metadata from the monitor's search query.
func extractQueryMetadata(queryMap map[string]interface{}) (searchQuery, namespace, projectUID, environmentUID, componentUID string) {
	boolQuery, ok := queryMap["query"].(map[string]interface{})
	if !ok {
		return
	}
	boolMap, ok := boolQuery["bool"].(map[string]interface{})
	if !ok {
		return
	}
	filters, ok := boolMap["filter"].([]interface{})
	if !ok {
		return
	}

	for _, f := range filters {
		filter, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		if termMap, ok := filter["term"].(map[string]interface{}); ok {
			for key, val := range termMap {
				valMap, ok := val.(map[string]interface{})
				if !ok {
					continue
				}
				value := getStringFromMap(valMap, "value")
				switch key {
				case opensearch.OSComponentID:
					componentUID = value
				case opensearch.OSEnvironmentID:
					environmentUID = value
				case opensearch.OSProjectID:
					projectUID = value
				}
			}
		}
		if wildcardMap, ok := filter["wildcard"].(map[string]interface{}); ok {
			if logMap, ok := wildcardMap["log"].(map[string]interface{}); ok {
				if pattern, ok := logMap["wildcard"].(string); ok {
					// Remove leading/trailing * from pattern
					searchQuery = strings.TrimPrefix(strings.TrimSuffix(pattern, "*"), "*")
				}
			}
		}
	}

	return searchQuery, namespace, projectUID, environmentUID, componentUID
}

// parseConditionScript parses the trigger condition script to extract operator and threshold.
func parseConditionScript(source string) (string, float32) {
	// Format: "ctx.results[0].hits.total.value > 10"
	parts := strings.Fields(source)
	if len(parts) < 3 {
		return "", 0
	}
	operator := parts[len(parts)-2]
	thresholdStr := parts[len(parts)-1]
	threshold, _ := strconv.ParseFloat(thresholdStr, 32)
	return operator, float32(threshold)
}

// formatScheduleToInterval converts schedule period to a duration string.
func formatScheduleToInterval(interval float64, unit string) string {
	switch strings.ToUpper(unit) {
	case "MINUTES":
		return fmt.Sprintf("%dm", int(interval))
	case "HOURS":
		return fmt.Sprintf("%dh", int(interval))
	}
	return fmt.Sprintf("%dm", int(interval))
}

// extractWindowFromQuery extracts the window duration from the monitor's query time range.
func extractWindowFromQuery(monitor map[string]interface{}) string {
	if inputs, ok := monitor["inputs"].([]interface{}); ok && len(inputs) > 0 {
		if input, ok := inputs[0].(map[string]interface{}); ok {
			if search, ok := input["search"].(map[string]interface{}); ok {
				if queryMap, ok := search["query"].(map[string]interface{}); ok {
					if boolQuery, ok := queryMap["query"].(map[string]interface{}); ok {
						if boolMap, ok := boolQuery["bool"].(map[string]interface{}); ok {
							if filters, ok := boolMap["filter"].([]interface{}); ok {
								for _, f := range filters {
									filter, ok := f.(map[string]interface{})
									if !ok {
										continue
									}
									if rangeMap, ok := filter["range"].(map[string]interface{}); ok {
										if tsMap, ok := rangeMap["@timestamp"].(map[string]interface{}); ok {
											if from, ok := tsMap["from"].(string); ok {
												// Format: "{{period_end}}||-1h"
												if idx := strings.Index(from, "||-"); idx != -1 {
													return from[idx+3:]
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return ""
}

func toComponentLogEntry(l *opensearch.LogEntry) gen.ComponentLogEntry {
	ts := l.Timestamp
	entry := gen.ComponentLogEntry{
		Timestamp: &ts,
		Log:       &l.Log,
		Level:     &l.LogLevel,
		Metadata: &struct {
			ComponentName   *string             `json:"componentName,omitempty"`
			ComponentUid    *openapi_types.UUID `json:"componentUid,omitempty"`
			ContainerName   *string             `json:"containerName,omitempty"`
			EnvironmentName *string             `json:"environmentName,omitempty"`
			EnvironmentUid  *openapi_types.UUID `json:"environmentUid,omitempty"`
			NamespaceName   *string             `json:"namespaceName,omitempty"`
			PodName         *string             `json:"podName,omitempty"`
			PodNamespace    *string             `json:"podNamespace,omitempty"`
			ProjectName     *string             `json:"projectName,omitempty"`
			ProjectUid      *openapi_types.UUID `json:"projectUid,omitempty"`
		}{
			NamespaceName:   strPtr(l.NamespaceName),
			ContainerName:   strPtr(l.ContainerName),
			PodName:         strPtr(l.PodName),
			PodNamespace:    strPtr(l.PodNamespace),
			ComponentName:   strPtr(l.ComponentName),
			ProjectName:     strPtr(l.ProjectName),
			EnvironmentName: strPtr(l.EnvironmentName),
		},
	}

	if l.ComponentID != "" {
		if uid, ok := parseUUID(l.ComponentID); ok {
			entry.Metadata.ComponentUid = &uid
		}
	}
	if l.ProjectID != "" {
		if uid, ok := parseUUID(l.ProjectID); ok {
			entry.Metadata.ProjectUid = &uid
		}
	}
	if l.EnvironmentID != "" {
		if uid, ok := parseUUID(l.EnvironmentID); ok {
			entry.Metadata.EnvironmentUid = &uid
		}
	}

	return entry
}

func toEventEntry(e *opensearch.EventEntry) gen.EventEntry {
	ts := e.Timestamp
	entry := gen.EventEntry{
		Timestamp: &ts,
		Message:   strPtr(e.Message),
		Type:      strPtr(e.Type),
		Reason:    strPtr(e.Reason),
		Metadata: &struct {
			ComponentName   *string             `json:"componentName,omitempty"`
			ComponentUid    *openapi_types.UUID `json:"componentUid,omitempty"`
			EnvironmentName *string             `json:"environmentName,omitempty"`
			EnvironmentUid  *openapi_types.UUID `json:"environmentUid,omitempty"`
			NamespaceName   *string             `json:"namespaceName,omitempty"`
			ObjectKind      *string             `json:"objectKind,omitempty"`
			ObjectName      *string             `json:"objectName,omitempty"`
			ObjectNamespace *string             `json:"objectNamespace,omitempty"`
			ProjectName     *string             `json:"projectName,omitempty"`
			ProjectUid      *openapi_types.UUID `json:"projectUid,omitempty"`
		}{
			ComponentName:   strPtr(e.ComponentName),
			EnvironmentName: strPtr(e.EnvironmentName),
			NamespaceName:   strPtr(e.NamespaceName),
			ObjectKind:      strPtr(e.ObjectKind),
			ObjectName:      strPtr(e.ObjectName),
			ObjectNamespace: strPtr(e.ObjectNamespace),
			ProjectName:     strPtr(e.ProjectName),
		},
	}

	if e.ComponentID != "" {
		if uid, ok := parseUUID(e.ComponentID); ok {
			entry.Metadata.ComponentUid = &uid
		}
	}
	if e.ProjectID != "" {
		if uid, ok := parseUUID(e.ProjectID); ok {
			entry.Metadata.ProjectUid = &uid
		}
	}
	if e.EnvironmentID != "" {
		if uid, ok := parseUUID(e.EnvironmentID); ok {
			entry.Metadata.EnvironmentUid = &uid
		}
	}

	return entry
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func parseUUID(s string) (openapi_types.UUID, bool) {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return openapi_types.UUID{}, false
	}
	return openapi_types.UUID(parsed), true
}

func ptr[T any](v T) *T {
	return &v
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
