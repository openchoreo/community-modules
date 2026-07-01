// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/finops-opencost/internal/api/gen"
	"github.com/openchoreo/community-modules/finops-opencost/internal/observer"
	"github.com/openchoreo/community-modules/finops-opencost/internal/opencost"
	"github.com/openchoreo/community-modules/finops-opencost/internal/recommend"
)

// maxRecommendationConcurrency bounds the number of in-flight Observer metrics
// queries when computing recommendations, so a project with many components does
// not open an unbounded number of upstream connections.
const maxRecommendationConcurrency = 8

type CostHandler struct {
	openCost     *opencost.Client
	observer     *observer.Client
	recommendCfg recommend.Config
	metricsStep  string
	logger       *slog.Logger
}

func NewCostHandler(
	openCostClient *opencost.Client,
	observerClient *observer.Client,
	recommendCfg recommend.Config,
	metricsStep string,
	logger *slog.Logger,
) *CostHandler {
	return &CostHandler{
		openCost:     openCostClient,
		observer:     observerClient,
		recommendCfg: recommendCfg,
		metricsStep:  metricsStep,
		logger:       logger,
	}
}

var _ gen.StrictServerInterface = (*CostHandler)(nil)

func (h *CostHandler) GetComponentCosts(ctx context.Context, request gen.GetComponentCostsRequestObject) (gen.GetComponentCostsResponseObject, error) {
	params := request.Params
	if msg := validateWindow(params.StartTime, params.EndTime); msg != "" {
		return gen.GetComponentCosts400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse{Message: msg}}, nil
	}
	if params.ComponentUid != nil && params.ProjectUid == nil {
		return gen.GetComponentCosts400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse{Message: "componentUid requires projectUid"}}, nil
	}

	step, err := opencost.NormalizeGranularity(derefString(params.Granularity))
	if err != nil {
		return gen.GetComponentCosts400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse{Message: err.Error()}}, nil
	}

	query := opencost.AllocationQuery{
		Namespace:      request.Namespace,
		EnvironmentUID: request.EnvironmentUid.String(),
		ProjectUID:     uuidPtrString(params.ProjectUid),
		ComponentUID:   uuidPtrString(params.ComponentUid),
		Start:          params.StartTime,
		End:            params.EndTime,
		Step:           step,
	}

	sets, err := h.openCost.QueryAllocations(ctx, query)
	if err != nil {
		h.logger.Error("failed to query OpenCost allocations", slog.Any("error", err))
		return gen.GetComponentCosts500JSONResponse{InternalErrorJSONResponse: gen.InternalErrorJSONResponse{Message: "failed to query cost data"}}, nil
	}

	items := make([]gen.ComponentCost, 0)
	for _, set := range sets {
		items = append(items, h.aggregateCosts(set, request.Namespace, request.EnvironmentUid, params.StartTime, params.EndTime)...)
	}

	return gen.GetComponentCosts200JSONResponse{Items: items}, nil
}

type costBucket struct {
	component      string
	componentUID   string
	project        string
	projectUID     string
	environment    string
	cpuCost        float64
	ramCost        float64
	weightedCPUEff float64
	weightedRAMEff float64
	totalEffSum    float64
	count          int
	start          time.Time
	end            time.Time
}

func (h *CostHandler) aggregateCosts(set map[string]opencost.Allocation, namespace string, environmentUID openapi_types.UUID, reqStart, reqEnd time.Time) []gen.ComponentCost {
	buckets := make(map[string]*costBucket)
	order := make([]string, 0)

	for _, alloc := range set {
		componentUID := alloc.Label(opencost.LabelComponentUID)
		if componentUID == "" {
			continue
		}
		b, ok := buckets[componentUID]
		if !ok {
			b = &costBucket{
				component:    alloc.Label(opencost.LabelComponent),
				componentUID: componentUID,
				project:      alloc.Label(opencost.LabelProject),
				projectUID:   alloc.Label(opencost.LabelProjectUID),
				environment:  alloc.Label(opencost.LabelEnvironment),
				start:        bucketTime(alloc.Window.Start, reqStart),
				end:          bucketTime(alloc.Window.End, reqEnd),
			}
			buckets[componentUID] = b
			order = append(order, componentUID)
		}
		b.cpuCost += alloc.CPUCost
		b.ramCost += alloc.RAMCost
		b.weightedCPUEff += alloc.CPUCost * alloc.CPUEfficiency
		b.weightedRAMEff += alloc.RAMCost * alloc.RAMEfficiency
		b.totalEffSum += alloc.TotalEfficiency
		b.count++
	}

	items := make([]gen.ComponentCost, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		items = append(items, gen.ComponentCost{
			ComponentUid:   parseUUID(b.componentUID),
			Component:      b.component,
			StartTime:      b.start,
			EndTime:        b.end,
			EnvironmentUid: environmentUID,
			Environment:    b.environment,
			ProjectUid:     parseUUID(b.projectUID),
			Project:        b.project,
			Namespace:      namespace,
			CpuCost:        float32(b.cpuCost),
			MemoryCost:     float32(b.ramCost),
			Efficiency:     float32(b.efficiency()),
		})
	}
	return items
}

func (b *costBucket) efficiency() float64 {
	totalCost := b.cpuCost + b.ramCost
	if totalCost > 0 {
		return clamp01((b.weightedCPUEff + b.weightedRAMEff) / totalCost)
	}
	if b.count > 0 {
		return clamp01(b.totalEffSum / float64(b.count))
	}
	return 0
}

func (h *CostHandler) GetRecommendations(ctx context.Context, request gen.GetRecommendationsRequestObject) (gen.GetRecommendationsResponseObject, error) {
	token := bearerTokenFromContext(ctx)
	if token == "" {
		return gen.GetRecommendations401JSONResponse{UnauthorizedJSONResponse: gen.UnauthorizedJSONResponse{Message: "missing bearer token"}}, nil
	}

	params := request.Params
	if msg := validateWindow(params.StartTime, params.EndTime); msg != "" {
		return gen.GetRecommendations400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse{Message: msg}}, nil
	}
	if params.Environment == "" {
		return gen.GetRecommendations400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse{Message: "environment is required"}}, nil
	}
	if params.ComponentUid != nil && params.ProjectUid == nil {
		return gen.GetRecommendations400JSONResponse{BadRequestJSONResponse: gen.BadRequestJSONResponse{Message: "componentUid requires projectUid"}}, nil
	}

	query := opencost.AllocationQuery{
		Namespace:      request.Namespace,
		EnvironmentUID: request.EnvironmentUid.String(),
		ProjectUID:     uuidPtrString(params.ProjectUid),
		ComponentUID:   uuidPtrString(params.ComponentUid),
		Start:          params.StartTime,
		End:            params.EndTime,
	}

	sets, err := h.openCost.QueryAllocations(ctx, query)
	if err != nil {
		h.logger.Error("failed to query OpenCost allocations", slog.Any("error", err))
		return gen.GetRecommendations500JSONResponse{InternalErrorJSONResponse: gen.InternalErrorJSONResponse{Message: "failed to query cost data"}}, nil
	}

	components := collectComponents(sets)
	windowHours := params.EndTime.Sub(params.StartTime).Hours()

	results := make([]*gen.ComponentRecommendation, len(components))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < maxRecommendationConcurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = h.recommendComponent(ctx, token, request, params, components[i], windowHours)
			}
		}()
	}
	for i := range components {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	items := make([]gen.ComponentRecommendation, 0, len(components))
	for _, r := range results {
		if r != nil {
			items = append(items, *r)
		}
	}

	return gen.GetRecommendations200JSONResponse{Items: items}, nil
}

// recommendComponent fetches usage metrics for a single component and builds its
// recommendation. It returns nil (and logs) when metrics cannot be fetched, so
// one failing component does not fail the whole response.
func (h *CostHandler) recommendComponent(
	ctx context.Context,
	token string,
	request gen.GetRecommendationsRequestObject,
	params gen.GetRecommendationsParams,
	comp componentPricing,
	windowHours float64,
) *gen.ComponentRecommendation {
	metrics, err := h.observer.QueryResourceMetrics(ctx, token, observer.ComponentSearchScope{
		Namespace:   request.Namespace,
		Project:     comp.project,
		Component:   comp.component,
		Environment: params.Environment,
	}, params.StartTime, params.EndTime, h.metricsStep)
	if err != nil {
		h.logger.Warn("failed to fetch metrics for component, skipping",
			slog.String("component", comp.component), slog.Any("error", err))
		return nil
	}

	current := recommend.Profile{
		CPURequest: maxValue(metrics.CPURequests),
		CPULimit:   maxValue(metrics.CPULimits),
		MemRequest: maxValue(metrics.MemoryRequests),
		MemLimit:   maxValue(metrics.MemoryLimits),
	}
	usage := recommend.Usage{
		CPUSamples: values(metrics.CPUUsage),
		MemSamples: values(metrics.MemoryUsage),
	}
	recommended := recommend.Compute(usage, current, h.recommendCfg)

	cpuPrice, ramPrice := comp.unitPrices()
	return &gen.ComponentRecommendation{
		ComponentUid:   parseUUID(comp.componentUID),
		Component:      comp.component,
		EnvironmentUid: request.EnvironmentUid,
		Environment:    params.Environment,
		ProjectUid:     parseUUID(comp.projectUID),
		Project:        comp.project,
		Namespace:      request.Namespace,
		// Current cost is the actual spend reported by OpenCost. The
		// recommendation cost is projected from the recommended request at
		// the same unit price, since no actual spend exists for it yet.
		Current:        buildProfile(current, comp.cpuCost, comp.ramCost),
		Recommendation: buildProfile(recommended, recommended.CPURequest*windowHours*cpuPrice, recommended.MemRequest*windowHours*ramPrice),
	}
}

type componentPricing struct {
	component    string
	componentUID string
	project      string
	projectUID   string
	cpuCost      float64
	cpuCoreHours float64
	ramCost      float64
	ramByteHours float64
}

func (c componentPricing) unitPrices() (cpuPerCoreHour, ramPerByteHour float64) {
	if c.cpuCoreHours > 0 {
		cpuPerCoreHour = c.cpuCost / c.cpuCoreHours
	}
	if c.ramByteHours > 0 {
		ramPerByteHour = c.ramCost / c.ramByteHours
	}
	return cpuPerCoreHour, ramPerByteHour
}

func collectComponents(sets []map[string]opencost.Allocation) []componentPricing {
	byUID := make(map[string]*componentPricing)
	order := make([]string, 0)
	for _, set := range sets {
		for _, alloc := range set {
			uid := alloc.Label(opencost.LabelComponentUID)
			if uid == "" {
				continue
			}
			c, ok := byUID[uid]
			if !ok {
				c = &componentPricing{
					component:    alloc.Label(opencost.LabelComponent),
					componentUID: uid,
					project:      alloc.Label(opencost.LabelProject),
					projectUID:   alloc.Label(opencost.LabelProjectUID),
				}
				byUID[uid] = c
				order = append(order, uid)
			}
			c.cpuCost += alloc.CPUCost
			c.cpuCoreHours += alloc.CPUCoreHours
			c.ramCost += alloc.RAMCost
			c.ramByteHours += alloc.RAMByteHours
		}
	}
	result := make([]componentPricing, 0, len(order))
	for _, uid := range order {
		result = append(result, *byUID[uid])
	}
	return result
}

func buildProfile(p recommend.Profile, cpuCost, memoryCost float64) gen.ResourceProfile {
	return gen.ResourceProfile{
		CpuRequest:    coresToQuantity(p.CPURequest),
		CpuLimit:      coresToQuantity(p.CPULimit),
		MemoryRequest: bytesToQuantity(p.MemRequest),
		MemoryLimit:   bytesToQuantity(p.MemLimit),
		CpuCost:       float32(cpuCost),
		MemoryCost:    float32(memoryCost),
	}
}

func validateWindow(start, end time.Time) string {
	if !end.After(start) {
		return "endTime must be strictly greater than startTime"
	}
	return ""
}

func bucketTime(fromAlloc, fallback time.Time) time.Time {
	if fromAlloc.IsZero() {
		return fallback
	}
	return fromAlloc
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxValue(items []observer.TimeSeriesItem) float64 {
	max := 0.0
	for _, item := range items {
		if item.Value > max {
			max = item.Value
		}
	}
	return max
}

func values(items []observer.TimeSeriesItem) []float64 {
	out := make([]float64, 0, len(items))
	for _, item := range items {
		out = append(out, item.Value)
	}
	return out
}

func coresToQuantity(cores float64) string {
	millicores := int64(cores*1000 + 0.5)
	return fmt.Sprintf("%dm", millicores)
}

func bytesToQuantity(bytes float64) string {
	mib := int64(bytes/(1024*1024) + 0.5)
	return fmt.Sprintf("%dMi", mib)
}

func parseUUID(s string) openapi_types.UUID {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return openapi_types.UUID{}
	}
	return parsed
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func uuidPtrString(u *openapi_types.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}
