// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// timespanPad widens the SDK Timespan past the query's own window. The window
// is enforced in the query body; the Timespan only bounds the scan, so it must
// never be the narrower of the two. NewTimeInterval formats to whole seconds,
// so without the pad a sub-second end would be truncated and cut events off.
const timespanPad = time.Second

// GetEvents runs an events query and returns one page of events together with
// a total that is greater than the page whenever the page is not the whole
// window.
func (c *Client) GetEvents(ctx context.Context, p EventsParams) (*EventsResult, error) {
	kql, err := BuildEventsKQL(p, c.eventsTable, c.eventsScopeName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()

	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(kql),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime.Add(-timespanPad), p.EndTime.Add(timespanPad))),
	}, nil)
	if err != nil {
		if isMissingTable(err, c.eventsTable) {
			// OTelLogs is built in, but a custom table appears only once events
			// are ingested, which may be after the adapter is installed. Having
			// no events is then simply true.
			c.logger.Warn("events table does not exist in the workspace yet; returning no events",
				slog.String("table", c.eventsTable))
			return &EventsResult{
				Events: []EventEntry{},
				TookMs: int(time.Since(startedAt).Milliseconds()),
			}, nil
		}
		return nil, fmt.Errorf("loganalytics: GetEvents: %w", err)
	}
	if qErr := queryError(resp); qErr != nil {
		return nil, fmt.Errorf("loganalytics: GetEvents: %w", qErr)
	}

	entries := []EventEntry{}
	if t, ok := findTable(resp.Tables, "Reason"); ok {
		var skipped []error
		entries, skipped, err = mapEventRows(t)
		if err != nil {
			return nil, err
		}
		for _, s := range skipped {
			c.logger.Warn("skipping malformed event record", slog.Any("error", s))
		}
	}

	limit := p.Limit
	if limit < 1 {
		limit = DefaultEventsLimit
	}
	if len(entries) >= limit+MaxEventBoundaryGroup {
		c.logger.Error("events sharing one timestamp exceed the boundary group cap; the page cuts the group",
			slog.Int("limit", limit), slog.Int("cap", MaxEventBoundaryGroup))
	}

	total, ok := findScalarTable(resp.Tables, "Total")
	if !ok {
		// Unlike platform logs, the page size is no stand-in here: a caller
		// reads total == len(events) as "window fully read", so guessing on a
		// full page could make it skip events it never saw.
		if len(entries) >= limit {
			return nil, errors.New("loganalytics: GetEvents: response carried no Total for a full page")
		}
		total = int64(len(entries))
	}
	if total < int64(len(entries)) {
		total = int64(len(entries))
	}

	return &EventsResult{
		Events: entries,
		Total:  int(total),
		TookMs: int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// ProbeEventsTable reports whether the events table can be queried. It is a
// boot-time diagnostic only: events may be onboarded after the adapter.
func (c *Client) ProbeEventsTable(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	end := time.Now().UTC()
	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(EventsTableProbeKQL(c.eventsTable)),
		Timespan: to.Ptr(azlogs.NewTimeInterval(end.Add(-1*time.Hour), end)),
	}, nil)
	if err != nil {
		return fmt.Errorf("loganalytics: events table probe failed: %w", err)
	}
	return queryError(resp)
}

// semFailedToResolve is the service's code for a name the query references
// but the workspace cannot resolve - a table or a column alike, so the message
// is what tells them apart.
const semFailedToResolve = "SEM0100"

// queryErrorDetail is one level of the error body Log Analytics returns for a
// rejected query. Each level may nest a more specific one; the depth is not
// part of the contract, so it is walked rather than addressed.
type queryErrorDetail struct {
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	InnerError *queryErrorDetail `json:"innererror"`
}

// isMissingTable reports whether err is the service rejecting the query
// because the configured events table does not exist. Only that table counts:
// a missing column is a real error.
//
// It reads the service's error body rather than ResponseError.Error(), whose
// layout is presentation, not contract. Anything it cannot decode is not a
// missing table, so the query fails loudly instead of answering empty.
func isMissingTable(err error, table string) bool {
	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != http.StatusBadRequest || re.RawResponse == nil {
		return false
	}
	// azcore caches the body when it builds the error, so it is still readable.
	payload, err := runtime.Payload(re.RawResponse)
	if err != nil {
		return false
	}
	var body struct {
		Error *queryErrorDetail `json:"error"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return false
	}
	for d := body.Error; d != nil; d = d.InnerError {
		if d.Code == semFailedToResolve &&
			strings.Contains(d.Message, "Failed to resolve table") &&
			strings.Contains(d.Message, "'"+table+"'") {
			return true
		}
	}
	return false
}
