// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	trace "cloud.google.com/go/trace/apiv1"
	"cloud.google.com/go/trace/apiv1/tracepb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// pageSize is the per-page size for ListTraces; the iterator fetches further
// pages transparently until the requested limit is reached.
const pageSize = 100

// listTracesFunc runs one ListTraces request and returns up to max traces.
// Production drains the SDK iterator; tests inject a fake.
type listTracesFunc func(ctx context.Context, req *tracepb.ListTracesRequest, max int) ([]*tracepb.Trace, error)

// getTraceFunc looks up one trace by ID.
type getTraceFunc func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error)

// Config carries the client's construction parameters.
type Config struct {
	ProjectID    string
	QueryTimeout time.Duration
}

// Client queries Cloud Trace through the v1 read API using Application
// Default Credentials (Workload Identity on GKE). The v1 API is the only
// Cloud Trace read surface; v2 is write-only.
type Client struct {
	projectID    string
	queryTimeout time.Duration
	list         listTracesFunc
	get          getTraceFunc
	traceClient  *trace.Client
	logger       *slog.Logger
}

// NewClient constructs a Client backed by the real Cloud Trace API.
func NewClient(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	tc, err := trace.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace client: %w", err)
	}
	c := newClientWithAPI(cfg, logger,
		func(ctx context.Context, req *tracepb.ListTracesRequest, max int) ([]*tracepb.Trace, error) {
			var out []*tracepb.Trace
			it := tc.ListTraces(ctx, req)
			for len(out) < max {
				t, err := it.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					return nil, err
				}
				out = append(out, t)
			}
			return out, nil
		},
		func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			return tc.GetTrace(ctx, req)
		},
	)
	c.traceClient = tc
	return c, nil
}

// newClientWithAPI wires arbitrary API functions; used by NewClient and tests.
func newClientWithAPI(cfg Config, logger *slog.Logger, list listTracesFunc, get getTraceFunc) *Client {
	timeout := cfg.QueryTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		projectID:    cfg.ProjectID,
		queryTimeout: timeout,
		list:         list,
		get:          get,
		logger:       logger,
	}
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	if c.traceClient != nil {
		return c.traceClient.Close()
	}
	return nil
}

// Ping validates credentials and API reachability at boot. An empty result
// is only a warning: the project may simply have no traces yet.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	now := time.Now().UTC()
	traces, err := c.list(ctx, &tracepb.ListTracesRequest{
		ProjectId: c.projectID,
		View:      tracepb.ListTracesRequest_MINIMAL,
		PageSize:  1,
		StartTime: timestamppb.New(now.Add(-1 * time.Hour)),
		EndTime:   timestamppb.New(now),
	}, 1)
	if err != nil {
		return fmt.Errorf("cloud trace ping: %w", err)
	}
	if len(traces) == 0 {
		c.logger.Warn("no traces found in the last hour; spans may not be flowing into this project yet",
			slog.String("projectId", c.projectID),
		)
	}
	return nil
}

// QueryTraces runs the traces-list query: ListTraces with the scope filter
// and COMPLETE view, then per-trace summaries computed from the spans. The
// COMPLETE view costs more per trace than ROOTSPAN but is the only view that
// yields spanCount and hasErrors without a follow-up GetTrace per trace.
func (c *Client) QueryTraces(ctx context.Context, p TracesParams) (*TracesResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	traces, err := c.list(ctx, &tracepb.ListTracesRequest{
		ProjectId: c.projectID,
		View:      tracepb.ListTracesRequest_COMPLETE,
		PageSize:  min(int32(p.Limit), pageSize),
		StartTime: timestamppb.New(p.StartTime),
		EndTime:   timestamppb.New(p.EndTime),
		Filter:    BuildScopeFilter(p),
		OrderBy:   orderBy(p.SortOrder),
	}, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("cloudtrace: QueryTraces: %w", err)
	}

	entries := make([]TraceEntry, 0, len(traces))
	for _, t := range traces {
		entries = append(entries, summarizeTrace(t))
	}
	return &TracesResult{
		Traces: entries,
		Total:  len(entries),
		TookMs: int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// QuerySpans returns the spans of one trace. p.TraceID must be set. GetTrace
// carries no filter expression, so the tenancy scope is enforced after the
// fact: a trace whose spans never match the scope is reported as empty, the
// same outcome ListTraces would produce.
func (c *Client) QuerySpans(ctx context.Context, p TracesParams) (*SpansResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	t, err := c.getTrace(ctx, p.TraceID)
	if err != nil {
		return nil, fmt.Errorf("cloudtrace: QuerySpans: %w", err)
	}

	result := &SpansResult{Spans: []Span{}}
	if t == nil || !anySpanMatchesScope(t, p) {
		result.TookMs = int(time.Since(startedAt).Milliseconds())
		return result, nil
	}

	spans := make([]Span, 0, len(t.GetSpans()))
	for _, s := range t.GetSpans() {
		spans = append(spans, mapSpan(s, p.IncludeAttributes))
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].StartTime.Before(spans[j].StartTime)
	})
	if p.Limit > 0 && len(spans) > p.Limit {
		spans = spans[:p.Limit]
	}

	result.Spans = spans
	result.Total = len(spans)
	result.TookMs = int(time.Since(startedAt).Milliseconds())
	return result, nil
}

// GetSpanDetails looks up one span by trace and span ID. Returns nil when
// the trace or span does not exist.
func (c *Client) GetSpanDetails(ctx context.Context, traceID, spanID string) (*Span, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	id, err := ParseSpanID(spanID)
	if err != nil {
		return nil, fmt.Errorf("cloudtrace: invalid span id %q: %w", spanID, err)
	}

	t, err := c.getTrace(ctx, traceID)
	if err != nil {
		return nil, fmt.Errorf("cloudtrace: GetSpanDetails: %w", err)
	}
	if t == nil {
		return nil, nil
	}
	for _, s := range t.GetSpans() {
		if s.GetSpanId() == id {
			span := mapSpan(s, true)
			return &span, nil
		}
	}
	return nil, nil
}

// getTrace normalizes the not-found outcome to (nil, nil) so callers can
// produce empty results instead of surfacing a backend error.
func (c *Client) getTrace(ctx context.Context, traceID string) (*tracepb.Trace, error) {
	t, err := c.get(ctx, &tracepb.GetTraceRequest{
		ProjectId: c.projectID,
		TraceId:   strings.ToLower(traceID),
	})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

func anySpanMatchesScope(t *tracepb.Trace, p TracesParams) bool {
	for _, s := range t.GetSpans() {
		if matchesScope(s.GetLabels(), p) {
			return true
		}
	}
	return false
}

// orderBy maps the adapter sort order onto the ListTraces order_by clause.
// Ascending is the API default and has no suffix form.
func orderBy(sortOrder string) string {
	if strings.EqualFold(sortOrder, "asc") {
		return "start"
	}
	return "start desc"
}
