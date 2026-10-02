// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

const (
	maxEventsLimit = 1000

	// maxEventReasons mirrors the contract's maxItems on reasons.
	maxEventReasons = 32

	msgUnscopedWithoutReasons = "searchScope is required unless reasons is set for an unscoped sweep"
)

// QueryEvents implements POST /api/v1/events/query.
//
// A scoped query reads the events of one component or workflow run. Without a
// scope it is an unscoped sweep across every namespace, which the contract
// allows only when narrowed by reasons; the adapter serves both. The contract
// carries no end-user identity, so the observer authorizes its own callers.
func (h *LogsHandler) QueryEvents(
	ctx context.Context, request gen.QueryEventsRequestObject,
) (gen.QueryEventsResponseObject, error) {
	if request.Body == nil {
		return eventsBadRequest("request body is required"), nil
	}

	params, msg := eventsQueryParams(*request.Body)
	if msg != "" {
		return eventsBadRequest(msg), nil
	}

	result, err := h.client.GetEvents(ctx, params)
	if err != nil {
		h.logger.Error("events query failed", slog.Any("error", err))
		return eventsInternalError("failed to query events"), nil
	}

	events := make([]gen.EventEntry, 0, len(result.Events))
	for i := range result.Events {
		events = append(events, mapEventEntry(&result.Events[i]))
	}
	took := result.TookMs

	return gen.QueryEvents200JSONResponse(gen.EventsQueryResponse{
		Events: &events,
		Total:  result.Total,
		TookMs: &took,
	}), nil
}

// eventsQueryParams maps the wire request onto query params, returning a
// non-empty message when the request is not answerable.
func eventsQueryParams(body gen.EventsQueryRequest) (loganalytics.EventsParams, string) {
	if body.EndTime.Before(body.StartTime) {
		return loganalytics.EventsParams{}, "endTime must be greater than or equal to startTime"
	}

	limit := loganalytics.DefaultEventsLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	if limit < 1 || limit > maxEventsLimit {
		return loganalytics.EventsParams{}, fmt.Sprintf("limit must be between 1 and %d", maxEventsLimit)
	}

	sortOrder := loganalytics.SortDesc
	if body.SortOrder != nil {
		switch *body.SortOrder {
		case gen.EventsQueryRequestSortOrderAsc:
			sortOrder = loganalytics.SortAsc
		case gen.EventsQueryRequestSortOrderDesc:
			sortOrder = loganalytics.SortDesc
		default:
			return loganalytics.EventsParams{}, "sortOrder must be one of asc, desc"
		}
	}

	var reasons []string
	if body.Reasons != nil {
		reasons = *body.Reasons
	}

	// Checked before the scope is decoded: a missing scope must never read as
	// "all namespaces" on its own, and the generated union accessors do not
	// tolerate a nil scope.
	if body.SearchScope == nil && len(reasons) == 0 {
		return loganalytics.EventsParams{}, msgUnscopedWithoutReasons
	}

	if body.Reasons != nil {
		if msg := validateEventReasons(reasons); msg != "" {
			return loganalytics.EventsParams{}, msg
		}
	}

	params := loganalytics.EventsParams{
		Reasons:   reasons,
		StartTime: body.StartTime,
		EndTime:   body.EndTime,
		Limit:     limit,
		SortOrder: sortOrder,
	}

	if body.SearchScope == nil {
		return params, ""
	}

	// Workflow scope discriminator: presence of workflowRunName.
	if wf, err := body.SearchScope.AsWorkflowSearchScope(); err == nil && wf.WorkflowRunName != nil {
		if strings.TrimSpace(wf.Namespace) == "" {
			return loganalytics.EventsParams{}, "searchScope.namespace is required"
		}
		if strings.TrimSpace(*wf.WorkflowRunName) == "" {
			return loganalytics.EventsParams{}, "searchScope.workflowRunName must not be empty"
		}
		params.Workflow = &loganalytics.WorkflowEventScope{
			Namespace:       wf.Namespace,
			WorkflowRunName: *wf.WorkflowRunName,
			TaskName:        strings.TrimSpace(derefString(wf.TaskName)),
		}
		return params, ""
	}

	scope, err := body.SearchScope.AsComponentSearchScope()
	if err != nil {
		return loganalytics.EventsParams{}, "searchScope is not a valid component or workflow scope"
	}
	if strings.TrimSpace(scope.Namespace) == "" {
		return loganalytics.EventsParams{}, "searchScope.namespace is required"
	}
	params.Component = &loganalytics.ComponentEventScope{
		Namespace:      scope.Namespace,
		ComponentUID:   strings.TrimSpace(derefString(scope.ComponentUid)),
		ProjectUID:     strings.TrimSpace(derefString(scope.ProjectUid)),
		EnvironmentUID: strings.TrimSpace(derefString(scope.EnvironmentUid)),
	}
	return params, ""
}

// validateEventReasons enforces the contract's bounds on reasons. No request
// validator runs ahead of the strict handler, so they are checked here.
func validateEventReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "reasons must contain at least one value when set"
	}
	if len(reasons) > maxEventReasons {
		return fmt.Sprintf("reasons cannot contain more than %d values", maxEventReasons)
	}
	seen := make(map[string]struct{}, len(reasons))
	for _, r := range reasons {
		if strings.TrimSpace(r) == "" {
			return "reasons must not contain empty values"
		}
		if _, dup := seen[r]; dup {
			return fmt.Sprintf("reasons contains %q more than once", r)
		}
		seen[r] = struct{}{}
	}
	return ""
}

func mapEventEntry(e *loganalytics.EventEntry) gen.EventEntry {
	ts := e.Timestamp

	metadata := &struct {
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
		ComponentName:   ptrStringNonEmpty(e.ComponentName),
		EnvironmentName: ptrStringNonEmpty(e.EnvironmentName),
		NamespaceName:   ptrStringNonEmpty(e.NamespaceName),
		ObjectKind:      ptrStringNonEmpty(e.ObjectKind),
		ObjectName:      ptrStringNonEmpty(e.ObjectName),
		ObjectNamespace: ptrStringNonEmpty(e.ObjectNamespace),
		ProjectName:     ptrStringNonEmpty(e.ProjectName),
	}
	if uid, ok := parseUUID(e.ComponentUID); ok {
		metadata.ComponentUid = &uid
	}
	if uid, ok := parseUUID(e.ProjectUID); ok {
		metadata.ProjectUid = &uid
	}
	if uid, ok := parseUUID(e.EnvironmentUID); ok {
		metadata.EnvironmentUid = &uid
	}

	return gen.EventEntry{
		Timestamp: &ts,
		Message:   ptrStringNonEmpty(e.Message),
		Type:      ptrStringNonEmpty(e.Type),
		Reason:    ptrStringNonEmpty(e.Reason),
		Metadata:  metadata,
	}
}

func eventsBadRequest(message string) gen.QueryEvents400JSONResponse {
	return gen.QueryEvents400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, message))
}

func eventsInternalError(message string) gen.QueryEvents500JSONResponse {
	return gen.QueryEvents500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, message))
}
