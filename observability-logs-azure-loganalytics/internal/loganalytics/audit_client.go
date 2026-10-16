// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// ErrAuditNotConfigured becomes a 501. An empty 200 would read as "nothing
// happened" rather than "not available here".
var ErrAuditNotConfigured = errors.New("loganalytics: audit is not configured")

// GetAuditLogs returns one page plus the exact count for the window.
func (c *Client) GetAuditLogs(ctx context.Context, p AuditParams) (*AuditResult, error) {
	if !c.AuditEnabled() {
		return nil, ErrAuditNotConfigured
	}

	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	kql := BuildAuditLogsKQL(p, c.auditTable)

	resp, err := c.api.QueryWorkspace(ctx, c.auditWorkspaceID, azlogs.QueryBody{
		Query:    to.Ptr(kql),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime.Add(-timespanPad), p.EndTime.Add(timespanPad))),
	}, nil)
	if err != nil {
		if isMissingTable(err, c.auditTable) {
			// The table appears only once a record has been ingested. No records
			// is not the same answer as no audit trail.
			c.logger.Warn("audit table does not exist in the workspace yet",
				slog.String("table", c.auditTable))
			return nil, ErrAuditNotConfigured
		}
		return nil, fmt.Errorf("loganalytics: GetAuditLogs: %w", err)
	}
	if qErr := queryError(resp); qErr != nil {
		return nil, fmt.Errorf("loganalytics: GetAuditLogs: %w", qErr)
	}

	result := &AuditResult{Records: []AuditRecord{}}

	if t, ok := findTable(resp.Tables, ColEventID); ok {
		records, skipped, err := mapAuditRows(t)
		if err != nil {
			return nil, err
		}
		for _, s := range skipped {
			c.logger.Warn("skipping malformed audit record", slog.Any("error", s))
		}
		result.Records = records
	}

	total, ok := findScalarTable(resp.Tables, ColTotal)
	if !ok {
		// The page size is no stand-in: a caller reads total == len(records) as
		// "window fully read" and would stop before records it never saw.
		if len(result.Records) >= effectiveAuditLimit(p.Limit) {
			return nil, errors.New("loganalytics: GetAuditLogs: response carried no Total for a full page")
		}
		total = int64(len(result.Records))
	}
	if total < int64(len(result.Records)) {
		total = int64(len(result.Records))
	}
	result.Total = total

	if p.IncludeTimeline {
		interval, _, width := ResolveTimelineInterval(p.StartTime, p.EndTime, p.TimelineInterval)
		if t, ok := findTable(resp.Tables, ColBucketStart); ok {
			timeline, err := mapAuditTimeline(t, p.StartTime, p.EndTime, interval, width)
			if err != nil {
				// A page without its histogram is still useful.
				c.logger.Warn("dropping audit timeline", slog.Any("error", err))
			} else {
				result.Timeline = timeline
			}
		}
	}

	result.TookMs = int(time.Since(startedAt).Milliseconds())
	return result, nil
}

// GetAuditLogFilterValues lists the distinct values one filter takes.
func (c *Client) GetAuditLogFilterValues(
	ctx context.Context, p AuditFilterValuesParams,
) (*AuditFilterValuesResult, error) {
	if !c.AuditEnabled() {
		return nil, ErrAuditNotConfigured
	}

	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	kql := BuildAuditFilterValuesKQL(p, c.auditTable)

	resp, err := c.api.QueryWorkspace(ctx, c.auditWorkspaceID, azlogs.QueryBody{
		Query: to.Ptr(kql),
		Timespan: to.Ptr(azlogs.NewTimeInterval(
			p.Query.StartTime.Add(-timespanPad), p.Query.EndTime.Add(timespanPad))),
	}, nil)
	if err != nil {
		if isMissingTable(err, c.auditTable) {
			c.logger.Warn("audit table does not exist in the workspace yet",
				slog.String("table", c.auditTable))
			return nil, ErrAuditNotConfigured
		}
		return nil, fmt.Errorf("loganalytics: GetAuditLogFilterValues: %w", err)
	}
	if qErr := queryError(resp); qErr != nil {
		return nil, fmt.Errorf("loganalytics: GetAuditLogFilterValues: %w", qErr)
	}

	result := &AuditFilterValuesResult{Filter: p.Filter, Values: []AuditFilterValue{}}

	if t, ok := findTable(resp.Tables, ColValue); ok {
		values, err := mapAuditFilterValues(t)
		if err != nil {
			return nil, err
		}
		result.Values = values
	}

	totalValues, ok := findScalarTable(resp.Tables, ColTotalValues)
	if !ok || totalValues < int64(len(result.Values)) {
		totalValues = int64(len(result.Values))
	}
	result.TotalValues = totalValues
	result.TookMs = int(time.Since(startedAt).Milliseconds())

	return result, nil
}

func (c *Client) AuditEnabled() bool {
	return c.auditWorkspaceID != "" && c.auditTable != ""
}

func (c *Client) AuditTable() string {
	return c.auditTable
}

// ProbeAuditTable is a boot diagnostic only: audit collection may be set up
// after the adapter is installed.
func (c *Client) ProbeAuditTable(ctx context.Context) error {
	if !c.AuditEnabled() {
		return ErrAuditNotConfigured
	}

	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	end := time.Now().UTC()
	resp, err := c.api.QueryWorkspace(ctx, c.auditWorkspaceID, azlogs.QueryBody{
		Query:    to.Ptr(AuditTableProbeKQL(c.auditTable)),
		Timespan: to.Ptr(azlogs.NewTimeInterval(end.Add(-1*time.Hour), end)),
	}, nil)
	if err != nil {
		return fmt.Errorf("loganalytics: audit table probe failed: %w", err)
	}
	return queryError(resp)
}

func effectiveAuditLimit(limit int) int {
	if limit < 1 {
		return DefaultAuditLimit
	}
	if limit > MaxAuditLimit {
		return MaxAuditLimit
	}
	return limit
}
