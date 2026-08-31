// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/gen"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/search"
)

// Handler implements gen.ServerInterface.
type Handler struct {
	searchClient *search.Client
}

func New(searchClient *search.Client) *Handler {
	return &Handler{searchClient: searchClient}
}

func (h *Handler) Health(c *gin.Context) {
	probe, err := h.searchClient.HealthProbe(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
		return
	}
	if !probe.Status {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "upstream": probe})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "upstream": probe})
}

func (h *Handler) QueryLogs(c *gin.Context) {
	moesifAppID := c.GetHeader("X-Moesif-App-Id")
	moesifOrgID := c.GetHeader("X-Moesif-Org-Id")

	var req gen.QueryLogsJSONRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{
			Title:   &title,
			Message: strPtr(err.Error()),
		})
		return
	}

	params := search.SearchEventsParams{
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Size:        100,
		MoesifAppID: moesifAppID,
		MoesifOrgID: moesifOrgID,
	}
	if req.Limit != nil {
		params.Size = *req.Limit
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}
	if req.SearchPhrase != nil {
		params.SearchPhrase = *req.SearchPhrase
	}
	if req.LogLevels != nil {
		for _, l := range *req.LogLevels {
			params.LogLevels = append(params.LogLevels, string(l))
		}
	}

	// Determine scope type and populate scope filters.
	var compScope gen.ComponentSearchScope
	if wf, err := req.SearchScope.AsWorkflowSearchScope(); err == nil && wf.Namespace != "" && wf.WorkflowRunName != nil && *wf.WorkflowRunName != "" {
		params.Namespace = wf.Namespace
		params.WorkflowRun = *wf.WorkflowRunName
	} else if cs, err := req.SearchScope.AsComponentSearchScope(); err == nil && cs.Namespace != "" {
		compScope = cs
		params.Namespace = cs.Namespace
		if cs.ProjectUid != nil {
			params.ProjectUID = *cs.ProjectUid
		}
		if cs.ComponentUid != nil {
			params.ComponentUID = *cs.ComponentUid
		}
		if cs.EnvironmentUid != nil {
			params.EnvironmentUID = *cs.EnvironmentUid
		}
	} else {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{
			Title:   &title,
			Message: strPtr("searchScope must be a valid component or workflow scope with a namespace"),
		})
		return
	}

	result, err := h.searchClient.SearchEvents(c.Request.Context(), params)
	if err != nil {
		title := gen.InternalServerError
		c.JSON(http.StatusInternalServerError, gen.ErrorResponse{
			Title:   &title,
			Message: strPtr(err.Error()),
		})
		return
	}

	entries := make(gen.LogsQueryResponseLogs0, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		entry := mapHitToLogEntry(hit.Source, compScope)
		if params.SearchPhrase != "" && entry.Log != nil {
			if !strings.Contains(strings.ToLower(*entry.Log), strings.ToLower(params.SearchPhrase)) {
				continue
			}
		}
		entries = append(entries, entry)
	}

	var logsUnion gen.LogsQueryResponse_Logs
	if err := logsUnion.FromLogsQueryResponseLogs0(entries); err != nil {
		title := gen.InternalServerError
		c.JSON(http.StatusInternalServerError, gen.ErrorResponse{Title: &title})
		return
	}

	c.JSON(http.StatusOK, gen.LogsQueryResponse{
		Logs:   &logsUnion,
		Total:  intPtr(result.Hits.Total),
		TookMs: intPtr(result.Took),
	})
}

func (h *Handler) CreateAlertRule(c *gin.Context) {
	var req gen.CreateAlertRuleJSONRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{
			Title:   &title,
			Message: strPtr(err.Error()),
		})
		return
	}

	// TODO: implement alert rule creation in Moesif backend
	title := gen.InternalServerError
	c.JSON(http.StatusNotImplemented, gen.ErrorResponse{
		Title:   &title,
		Message: strPtr("alert rule creation is not implemented"),
	})
}

func (h *Handler) GetAlertRule(c *gin.Context, ruleName string) {
	// TODO: implement alert rule retrieval from Moesif backend
	title := gen.InternalServerError
	c.JSON(http.StatusNotImplemented, gen.ErrorResponse{
		Title:   &title,
		Message: strPtr("alert rule retrieval is not implemented"),
	})
}

func (h *Handler) UpdateAlertRule(c *gin.Context, ruleName string) {
	var req gen.UpdateAlertRuleJSONRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{
			Title:   &title,
			Message: strPtr(err.Error()),
		})
		return
	}

	// TODO: implement alert rule update in Moesif backend
	title := gen.InternalServerError
	c.JSON(http.StatusNotImplemented, gen.ErrorResponse{
		Title:   &title,
		Message: strPtr("alert rule update is not implemented"),
	})
}

func (h *Handler) DeleteAlertRule(c *gin.Context, ruleName string) {
	// TODO: implement alert rule deletion in Moesif backend
	title := gen.InternalServerError
	c.JSON(http.StatusNotImplemented, gen.ErrorResponse{
		Title:   &title,
		Message: strPtr("alert rule deletion is not implemented"),
	})
}

func (h *Handler) HandleAlertWebhook(c *gin.Context) {
	var body gen.HandleAlertWebhookJSONRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{
			Title:   &title,
			Message: strPtr(err.Error()),
		})
		return
	}

	// TODO: implement webhook handling
	title := gen.InternalServerError
	c.JSON(http.StatusNotImplemented, gen.ErrorResponse{
		Title:   &title,
		Message: strPtr("alert webhook handling is not implemented"),
	})
}

// mapHitToLogEntry converts a Moesif search hit into a ComponentLogEntry.
// The _source fields are nested objects (e.g. source["request"]["time"]).
func mapHitToLogEntry(source map[string]interface{}, scope gen.ComponentSearchScope) gen.ComponentLogEntry {
	entry := gen.ComponentLogEntry{}

	req, _ := source["request"].(map[string]interface{})

	// timestamp from request.time — format: "2006-01-02T15:04:05.000" (no timezone)
	if req != nil {
		if rt, ok := req["time"].(string); ok {
			for _, layout := range []string{"2006-01-02T15:04:05.000", time.RFC3339Nano, time.RFC3339} {
				if t, err := time.Parse(layout, rt); err == nil {
					entry.Timestamp = &t
					break
				}
			}
		}
	}

	// log message: base64-decode request.body._raw when present, otherwise fallback
	logStr := extractLogMessage(req)
	entry.Log = &logStr

	// level from log.severity.text; default to INFO if not present
	level := ""
	if logObj, ok := source["log"].(map[string]interface{}); ok {
		if sevObj, ok := logObj["severity"].(map[string]interface{}); ok {
			if text, ok := sevObj["text"].(string); ok && text != "" {
				level = text
			}
		}
	}
	entry.Level = &level

	// Extract resource fields
	var componentName *string
	var environmentName *string
	var namespaceName *string
	var projectName *string
	var componentUID *string
	var environmentUID *string
	var projectUID *string
	if res, ok := source["resource"].(map[string]interface{}); ok {
		if cn, ok := res["openchoreo.dev/component"].(string); ok && cn != "" {
			componentName = &cn
		}
		if en, ok := res["openchoreo.dev/environment"].(string); ok && en != "" {
			environmentName = &en
		}
		if ns, ok := res["openchoreo.dev/namespace"].(string); ok && ns != "" {
			namespaceName = &ns
		}
		if pn, ok := res["openchoreo.dev/project"].(string); ok && pn != "" {
			projectName = &pn
		}
		if cu, ok := res["openchoreo.dev/component-uid"].(string); ok && cu != "" {
			componentUID = &cu
		}
		if eu, ok := res["openchoreo.dev/environment-uid"].(string); ok && eu != "" {
			environmentUID = &eu
		}
		if pu, ok := res["openchoreo.dev/project-uid"].(string); ok && pu != "" {
			projectUID = &pu
		}
	}

	// metadata populated from the resource info, falling back to search scope
	ns := scope.Namespace
	if namespaceName != nil {
		ns = *namespaceName
	}

	// Resolve UIDs: prefer resource values, fall back to scope
	resolvedComponentUID := parseUUID(componentUID)
	if resolvedComponentUID == nil {
		resolvedComponentUID = parseUUID(scope.ComponentUid)
	}
	resolvedProjectUID := parseUUID(projectUID)
	if resolvedProjectUID == nil {
		resolvedProjectUID = parseUUID(scope.ProjectUid)
	}
	resolvedEnvironmentUID := parseUUID(environmentUID)
	if resolvedEnvironmentUID == nil {
		resolvedEnvironmentUID = parseUUID(scope.EnvironmentUid)
	}

	entry.Metadata = &struct {
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
		NamespaceName:   &ns,
		ComponentName:   componentName,
		ComponentUid:    resolvedComponentUID,
		EnvironmentName: environmentName,
		EnvironmentUid:  resolvedEnvironmentUID,
		ProjectName:     projectName,
		ProjectUid:      resolvedProjectUID,
	}

	return entry
}

// extractLogMessage returns the decoded log message from request.body._raw,
// falling back to "verb uri" if the body is absent.
func extractLogMessage(req map[string]interface{}) string {
	if req != nil {
		if body, ok := req["body"].(map[string]interface{}); ok {
			if raw, ok := body["_raw"].(string); ok && raw != "" {
				decoded, err := base64.StdEncoding.DecodeString(raw)
				if err == nil {
					// strip surrounding JSON string quotes if present
					msg := string(decoded)
					if len(msg) >= 2 && msg[0] == '"' && msg[len(msg)-1] == '"' {
						msg = msg[1 : len(msg)-1]
					}
					return msg
				}
			}
		}
		verb, _ := req["verb"].(string)
		uri, _ := req["uri"].(string)
		return fmt.Sprintf("%s %s", verb, uri)
	}
	return ""
}

func parseUUID(s *string) *openapi_types.UUID {
	if s == nil {
		return nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	uid := openapi_types.UUID(id)
	return &uid
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
