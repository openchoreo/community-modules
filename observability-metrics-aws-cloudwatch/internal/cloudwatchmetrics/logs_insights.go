// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// runLogsInsightsQuery starts a Logs Insights query and polls until it reaches
// a terminal state. Returned rows are keyed by field alias.
func (c *Client) runLogsInsightsQuery(ctx context.Context, query string, startTime, endTime time.Time) ([]map[string]string, error) {
	if c.logs == nil {
		return nil, errors.New("cloudwatch logs client is not configured")
	}
	if startTime.IsZero() || endTime.IsZero() {
		return nil, errors.New("startTime and endTime are required")
	}
	if !endTime.After(startTime) {
		return nil, fmt.Errorf("endTime (%s) must be after startTime (%s)", endTime, startTime)
	}

	startOut, err := c.logs.StartQuery(ctx, &cloudwatchlogs.StartQueryInput{
		LogGroupName: aws.String(c.metricsLogGroup),
		StartTime:    aws.Int64(startTime.Unix()),
		EndTime:      aws.Int64(endTime.Unix()),
		QueryString:  aws.String(query),
	})
	if err != nil {
		// The EMF log group may not exist yet (e.g. the adapter is running before
		// the collector has created it, or on a cluster without the Hubble
		// pipeline). Degrade to empty results rather than a 500, mirroring the
		// Prometheus reference (a missing metric never errors) and the repo
		// precedent in commit 7fa15e0 ("treat missing OpenObserve stream as empty
		// results").
		if isAWSNotFound(err) {
			if c.logger != nil {
				c.logger.Debug("logs insights query skipped: log group not found",
					"logGroup", c.metricsLogGroup)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("start_query: %w", err)
	}
	queryID := aws.ToString(startOut.QueryId)

	deadline := time.Now().Add(c.queryTimeout)
	for {
		if time.Now().After(deadline) {
			stopCtx, cancel := context.WithTimeout(context.Background(), stopQueryTimeout)
			_, _ = c.logs.StopQuery(stopCtx, &cloudwatchlogs.StopQueryInput{QueryId: aws.String(queryID)})
			cancel()
			return nil, fmt.Errorf("query %s timed out after %s", queryID, c.queryTimeout)
		}

		res, err := c.logs.GetQueryResults(ctx, &cloudwatchlogs.GetQueryResultsInput{QueryId: aws.String(queryID)})
		if err != nil {
			return nil, fmt.Errorf("get_query_results: %w", err)
		}
		switch res.Status {
		case cwltypes.QueryStatusComplete:
			return mapLogsInsightsRows(res.Results), nil
		case cwltypes.QueryStatusFailed, cwltypes.QueryStatusCancelled, cwltypes.QueryStatusTimeout:
			return nil, fmt.Errorf("query %s ended with status %s", queryID, res.Status)
		case cwltypes.QueryStatusRunning, cwltypes.QueryStatusScheduled, cwltypes.QueryStatusUnknown:
		}

		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), stopQueryTimeout)
			_, _ = c.logs.StopQuery(stopCtx, &cloudwatchlogs.StopQueryInput{QueryId: aws.String(queryID)})
			cancel()
			return nil, ctx.Err()
		case <-time.After(c.pollEvery):
		}
	}
}

func mapLogsInsightsRows(results [][]cwltypes.ResultField) []map[string]string {
	out := make([]map[string]string, 0, len(results))
	for _, row := range results {
		m := make(map[string]string, len(row))
		for _, f := range row {
			name := aws.ToString(f.Field)
			if name == "" || name == "@ptr" {
				continue
			}
			m[name] = aws.ToString(f.Value)
		}
		out = append(out, m)
	}
	return out
}
