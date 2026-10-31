// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
)

// Config carries the client's construction parameters.
type Config struct {
	ProjectID    string
	QueryTimeout time.Duration
}

// listTimeSeriesFunc runs one ListTimeSeries request to completion and
// returns all matching series. Production drains the SDK iterator; tests
// inject a fake.
type listTimeSeriesFunc func(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error)

// Client queries GKE system metrics through the Cloud Monitoring API using
// Application Default Credentials (Workload Identity on GKE).
type Client struct {
	projectID    string
	queryTimeout time.Duration
	list         listTimeSeriesFunc
	metricClient *monitoring.MetricClient
	logger       *slog.Logger
}

// NewClient constructs a Client backed by the real Cloud Monitoring API.
func NewClient(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	mc, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create metric client: %w", err)
	}
	c := newClientWithLister(cfg, logger, func(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
		var out []*monitoringpb.TimeSeries
		it := mc.ListTimeSeries(ctx, req)
		for {
			ts, err := it.Next()
			if err == iterator.Done {
				return out, nil
			}
			if err != nil {
				return nil, err
			}
			out = append(out, ts)
		}
	})
	c.metricClient = mc
	return c, nil
}

// newClientWithLister wires an arbitrary lister; used by NewClient and tests.
func newClientWithLister(cfg Config, logger *slog.Logger, list listTimeSeriesFunc) *Client {
	timeout := cfg.QueryTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		projectID:    cfg.ProjectID,
		queryTimeout: timeout,
		list:         list,
		logger:       logger,
	}
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	if c.metricClient != nil {
		return c.metricClient.Close()
	}
	return nil
}

// Ping validates credentials and API reachability at boot. An empty result is
// only a warning: the project may simply have no GKE workloads yet.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	series, err := c.list(ctx, buildPingRequest(c.projectID, time.Now()))
	if err != nil {
		return fmt.Errorf("cloud monitoring ping: %w", err)
	}
	if len(series) == 0 {
		c.logger.Warn("no k8s_container CPU series found in the last hour; GKE system metrics may not be flowing into this project",
			slog.String("projectId", c.projectID),
		)
	}
	return nil
}

// GetResourceMetrics runs the per-metric ListTimeSeries queries in parallel,
// six or, for every line of a component, twelve, and assembles the result.
// The series of a metric's queries add up. The first error wins and cancels
// the remaining queries.
func (c *Client) GetResourceMetrics(ctx context.Context, p MetricsQueryParams) (*ResourceMetricsResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	results := make(map[string][]*monitoringpb.TimeSeries, len(resourceMetricSpecs))
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)

	for _, spec := range resourceMetricSpecs {
		for _, req := range buildListRequests(c.projectID, spec, p) {
			wg.Add(1)
			go func(key string, req *monitoringpb.ListTimeSeriesRequest) {
				defer wg.Done()
				series, err := c.list(ctx, req)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("query %s: %w", key, err)
						cancel()
					}
					return
				}
				results[key] = append(results[key], series...)
			}(spec.key, req)
		}
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return &ResourceMetricsResult{
		CPUUsage:       timeSeriesToPoints(results["cpuUsage"]),
		CPURequests:    timeSeriesToPoints(results["cpuRequests"]),
		CPULimits:      timeSeriesToPoints(results["cpuLimits"]),
		MemoryUsage:    timeSeriesToPoints(results["memoryUsage"]),
		MemoryRequests: timeSeriesToPoints(results["memoryRequests"]),
		MemoryLimits:   timeSeriesToPoints(results["memoryLimits"]),
	}, nil
}
