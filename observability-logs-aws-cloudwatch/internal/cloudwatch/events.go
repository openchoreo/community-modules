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

// eventsLogGroup returns the CloudWatch log group where the events collector's
// awscloudwatchlogsexporter ships enriched Kubernetes events.
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

// runEventsQuery executes an events query and maps the rows to EventEntry values.
//
// The events collector is an optional, opt-in component: when it is not deployed the
// events log group does not exist. Rather than surfacing that as a 500, treat a
// missing log group as an empty result so /api/v1/events/query degrades gracefully.
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

// isResourceNotFound reports whether err is (or wraps) a CloudWatch Logs
// ResourceNotFoundException — e.g. the events log group has not been created because
// the events collector is not deployed.
func isResourceNotFound(err error) bool {
	var notFound *cwltypes.ResourceNotFoundException
	return errors.As(err, &notFound)
}
