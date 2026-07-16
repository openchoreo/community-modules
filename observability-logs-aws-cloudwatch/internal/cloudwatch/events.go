// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"context"
	"errors"
	"log/slog"
	"time"

	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// eventsLogGroup returns the log group holding the collected Kubernetes events.
func (c *Client) eventsLogGroup() string {
	return c.eventsLogGroupName
}

// GetComponentEvents runs a Logs Insights query for component-scoped events.
func (c *Client) GetComponentEvents(ctx context.Context, params ComponentEventsParams) (*EventsResult, error) {
	return c.runEventsQuery(ctx, buildComponentEventsQuery(params), params.StartTime, params.EndTime)
}

// GetWorkflowEvents runs a Logs Insights query for workflow-scoped events.
func (c *Client) GetWorkflowEvents(ctx context.Context, params WorkflowEventsParams) (*EventsResult, error) {
	return c.runEventsQuery(ctx, buildWorkflowEventsQuery(params), params.StartTime, params.EndTime)
}

// runEventsQuery runs an events query and maps the rows to EventEntry values. A
// missing log group (collector not deployed) returns an empty result, not an error.
func (c *Client) runEventsQuery(ctx context.Context, query string, startTime, endTime time.Time) (*EventsResult, error) {
	started := time.Now()

	c.logger.Debug("CloudWatch Logs Insights events query",
		slog.String("logGroup", c.eventsLogGroup()),
		slog.String("query", query),
	)

	rows, err := c.runQuery(ctx, c.eventsLogGroup(), query, startTime, endTime)
	if err != nil {
		if isResourceNotFound(err) {
			c.logger.Debug("Events log group not found; returning empty result",
				slog.String("logGroup", c.eventsLogGroup()),
			)
			return &EventsResult{Events: []EventEntry{}, TotalCount: 0, Took: int(time.Since(started).Milliseconds())}, nil
		}
		return nil, err
	}

	entries := make([]EventEntry, 0, len(rows))
	for _, row := range rows {
		entry := EventEntry{
			Message:         row["message"],
			Type:            row["type"],
			Reason:          row["reason"],
			ObjectKind:      row["objectKind"],
			ObjectName:      row["objectName"],
			ObjectNamespace: row["objectNamespace"],
			ComponentUID:    row["componentUid"],
			ComponentName:   row["componentName"],
			EnvironmentUID:  row["environmentUid"],
			EnvironmentName: row["environmentName"],
			ProjectUID:      row["projectUid"],
			ProjectName:     row["projectName"],
			NamespaceName:   row["namespaceName"],
		}
		if ts, err := parseInsightsTimestamp(row["@timestamp"]); err == nil {
			entry.Timestamp = ts
		}
		entries = append(entries, entry)
	}

	return &EventsResult{
		Events:     entries,
		TotalCount: len(entries),
		Took:       int(time.Since(started).Milliseconds()),
	}, nil
}

// isResourceNotFound reports whether err is a CloudWatch Logs ResourceNotFoundException.
func isResourceNotFound(err error) bool {
	var notFound *cwltypes.ResourceNotFoundException
	return errors.As(err, &notFound)
}
