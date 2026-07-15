// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-aws-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-aws-cloudwatch/internal/cloudwatchmetrics"
)

// QueryRuntimeTopology handles POST /api/v1alpha1/metrics/runtime-topology.
func (h *MetricsHandler) QueryRuntimeTopology(ctx context.Context, request gen.QueryRuntimeTopologyRequestObject) (gen.QueryRuntimeTopologyResponseObject, error) {
	if request.Body == nil {
		return runtimeTopologyBadRequest("request body is required"), nil
	}
	scope := request.Body.SearchScope
	if scope.Namespace == "" {
		return runtimeTopologyBadRequest("searchScope.namespace is required"), nil
	}
	if scope.ProjectUid == "" {
		return runtimeTopologyBadRequest("searchScope.projectUid is required"), nil
	}
	if scope.EnvironmentUid == "" {
		return runtimeTopologyBadRequest("searchScope.environmentUid is required"), nil
	}
	if !request.Body.EndTime.After(request.Body.StartTime) {
		return runtimeTopologyBadRequest("endTime must be after startTime"), nil
	}

	params := cloudwatchmetrics.RuntimeTopologyQueryParams{
		Namespace:      scope.Namespace,
		ComponentUID:   derefString(scope.ComponentUid),
		ProjectUID:     scope.ProjectUid,
		EnvironmentUID: scope.EnvironmentUid,
		StartTime:      request.Body.StartTime,
		EndTime:        request.Body.EndTime,
	}
	result, err := h.client.GetRuntimeTopology(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query runtime topology",
			slog.String("namespace", params.Namespace),
			slog.String("projectUID", params.ProjectUID),
			slog.String("environmentUID", params.EnvironmentUID),
			slog.Any("error", err),
		)
		return runtimeTopologyServerError(fmt.Sprintf("query failed: %v", err)), nil
	}

	edges := topologyEdgesFromResult(params.Namespace, params.ProjectUID, result)
	response := gen.RuntimeTopologyResponse{
		Edges: &edges,
		Nodes: nil,
		Summary: gen.RuntimeTopologySummary{
			StartTime:   request.Body.StartTime,
			EndTime:     request.Body.EndTime,
			GeneratedAt: time.Now().UTC(),
		},
	}
	return gen.QueryRuntimeTopology200JSONResponse(response), nil
}

func topologyEdgesFromResult(namespace, projectUID string, result *cloudwatchmetrics.RuntimeTopologyResult) []gen.RuntimeTopologyEdge {
	if result == nil {
		return []gen.RuntimeTopologyEdge{}
	}
	edges := make([]gen.RuntimeTopologyEdge, 0, len(result.Edges))
	for _, edge := range result.Edges {
		srcNamespace := firstNonEmptyString(edge.SourceNamespace, namespace)
		dstNamespace := firstNonEmptyString(edge.DestinationNamespace, namespace)
		proj := projectUID
		var source, target gen.RuntimeTopologyNodeRef
		_ = source.FromRuntimeTopologyNodeRefComponent(gen.RuntimeTopologyNodeRefComponent{
			Kind:         gen.RuntimeTopologyNodeRefComponentKindComponent,
			Component:    edge.SourceComponentName,
			ComponentUid: edge.SourceComponentUID,
			ProjectUid:   &proj,
			Namespace:    &srcNamespace,
		})
		_ = target.FromRuntimeTopologyNodeRefComponent(gen.RuntimeTopologyNodeRefComponent{
			Kind:         gen.RuntimeTopologyNodeRefComponentKindComponent,
			Component:    edge.DestinationComponentName,
			ComponentUid: edge.DestinationComponentUID,
			ProjectUid:   &proj,
			Namespace:    &dstNamespace,
		})
		edges = append(edges, gen.RuntimeTopologyEdge{
			Id:       fmt.Sprintf("%s->%s", edge.SourceComponentUID, edge.DestinationComponentUID),
			Source:   source,
			Target:   target,
			Protocol: gen.RuntimeTopologyEdgeProtocolHttp,
			Metrics: &gen.RuntimeTopologyMetrics{
				RequestCount:             &edge.RequestCount,
				UnsuccessfulRequestCount: &edge.ErrorCount,
				MeanLatency:              &edge.MeanLatency,
				// Latency percentiles are reconstructed from the preserved `le`
				// buckets (the collector emits a dedicated bucket series so they
				// survive the awsemf histogram path). Nil when the edge has no
				// duration samples in the window.
				LatencyP50: edge.LatencyP50,
				LatencyP90: edge.LatencyP90,
				LatencyP99: edge.LatencyP99,
			},
		})
	}
	return edges
}

func runtimeTopologyBadRequest(detail string) gen.QueryRuntimeTopology400JSONResponse {
	return gen.QueryRuntimeTopology400JSONResponse{
		Title:  errorTitle(gen.BadRequest),
		Detail: strPtr(detail),
	}
}

func runtimeTopologyServerError(detail string) gen.QueryRuntimeTopology500JSONResponse {
	return gen.QueryRuntimeTopology500JSONResponse{
		Title:  errorTitle(gen.InternalServerError),
		Detail: strPtr(detail),
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
