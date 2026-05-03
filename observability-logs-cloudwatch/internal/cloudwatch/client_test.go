// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func TestExtractLogLevelMatchesOpenObserveFallback(t *testing.T) {
	tests := []struct {
		name     string
		log      string
		expected string
	}{
		{name: "error", log: "2025-01-01 ERROR: something failed", expected: "ERROR"},
		{name: "warn", log: "[WARN] potential issue", expected: "WARN"},
		{name: "warning", log: "WARNING: deprecated function", expected: "WARN"},
		{name: "debug", log: "DEBUG: verbose output", expected: "DEBUG"},
		{name: "info", log: "INFO: service started", expected: "INFO"},
		{name: "fatal", log: "FATAL: cannot continue", expected: "FATAL"},
		{name: "severe", log: "SEVERE: critical problem", expected: "SEVERE"},
		{name: "plain", log: "just a regular log message", expected: "INFO"},
		{name: "empty", log: "", expected: "INFO"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractLogLevel(test.log); got != test.expected {
				t.Fatalf("extractLogLevel(%q) = %q, want %q", test.log, got, test.expected)
			}
		})
	}
}

func TestLogLevelFromRowPrefersStructuredFields(t *testing.T) {
	tests := []struct {
		name     string
		row      map[string]string
		expected string
	}{
		{
			name: "top-level logLevel",
			row: map[string]string{
				"@message": "INFO fallback should not win",
				"logLevel": "ERROR",
			},
			expected: "ERROR",
		},
		{
			name: "merged processed logLevel",
			row: map[string]string{
				"@message":             "INFO fallback should not win",
				"logProcessedLogLevel": "WARN",
			},
			expected: "WARN",
		},
		{
			name: "fallback extraction",
			row: map[string]string{
				"@message": "DEBUG fallback",
			},
			expected: "DEBUG",
		},
		{
			name: "default fallback",
			row: map[string]string{
				"@message": "plain message",
			},
			expected: "INFO",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := logLevelFromRow(test.row); got != test.expected {
				t.Fatalf("logLevelFromRow() = %q, want %q", got, test.expected)
			}
		})
	}
}

// queryStubLogsAPI captures StartQuery/GetQueryResults/StopQuery for runQuery tests.
type queryStubLogsAPI struct {
	startCalls   int
	startErr     error
	resultsErr   error
	stopCalls    int
	stopErr      error
	statusQueue  []cwltypes.QueryStatus
	resultsQueue [][]cwltypes.ResultField
}

func (s *queryStubLogsAPI) StartQuery(_ context.Context, _ *cloudwatchlogs.StartQueryInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error) {
	s.startCalls++
	if s.startErr != nil {
		return nil, s.startErr
	}
	return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String("query-1")}, nil
}

func (s *queryStubLogsAPI) GetQueryResults(_ context.Context, _ *cloudwatchlogs.GetQueryResultsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	if s.resultsErr != nil {
		return nil, s.resultsErr
	}
	if len(s.statusQueue) == 0 {
		return &cloudwatchlogs.GetQueryResultsOutput{Status: cwltypes.QueryStatusComplete, Results: s.resultsQueue}, nil
	}
	status := s.statusQueue[0]
	s.statusQueue = s.statusQueue[1:]
	out := &cloudwatchlogs.GetQueryResultsOutput{Status: status}
	if status == cwltypes.QueryStatusComplete {
		out.Results = s.resultsQueue
	}
	return out, nil
}

func (s *queryStubLogsAPI) StopQuery(_ context.Context, _ *cloudwatchlogs.StopQueryInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StopQueryOutput, error) {
	s.stopCalls++
	return &cloudwatchlogs.StopQueryOutput{}, s.stopErr
}

func (s *queryStubLogsAPI) PutMetricFilter(context.Context, *cloudwatchlogs.PutMetricFilterInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutMetricFilterOutput, error) {
	return nil, errors.New("unexpected")
}
func (s *queryStubLogsAPI) DescribeMetricFilters(context.Context, *cloudwatchlogs.DescribeMetricFiltersInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeMetricFiltersOutput, error) {
	return nil, errors.New("unexpected")
}
func (s *queryStubLogsAPI) DeleteMetricFilter(context.Context, *cloudwatchlogs.DeleteMetricFilterInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteMetricFilterOutput, error) {
	return nil, errors.New("unexpected")
}

func newQueryTestClient(api *queryStubLogsAPI) *Client {
	return NewClientWithAWS(api, &stubAlarmsAPI{}, &stsStub{}, Config{
		ClusterName:    "test-cluster",
		LogGroupPrefix: "/aws/containerinsights",
		QueryTimeout:   2 * time.Second,
		PollEvery:      5 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type stsStub struct{}

func (s *stsStub) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{}, nil
}

func TestPingDelegatesToSTS(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestGetComponentLogsHappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	api := &queryStubLogsAPI{
		statusQueue: []cwltypes.QueryStatus{cwltypes.QueryStatusRunning, cwltypes.QueryStatusComplete},
		resultsQueue: [][]cwltypes.ResultField{
			{
				{Field: aws.String("@timestamp"), Value: aws.String(now.Format("2006-01-02 15:04:05.000"))},
				{Field: aws.String("@message"), Value: aws.String("hello world")},
				{Field: aws.String("namespace"), Value: aws.String("default")},
				{Field: aws.String("podName"), Value: aws.String("pod-1")},
				{Field: aws.String("containerName"), Value: aws.String("c1")},
				{Field: aws.String("componentUid"), Value: aws.String("33333333-3333-3333-3333-333333333333")},
				{Field: aws.String("componentName"), Value: aws.String("comp")},
				{Field: aws.String("logLevel"), Value: aws.String("ERROR")},
				{Field: aws.String("@ptr"), Value: aws.String("ignore-me")},
			},
		},
	}
	c := newQueryTestClient(api)
	res, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("GetComponentLogs() error = %v", err)
	}
	if len(res.Logs) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.Logs))
	}
	got := res.Logs[0]
	if got.Log != "hello world" || got.PodName != "pod-1" || got.LogLevel != "ERROR" || got.Namespace != "default" {
		t.Fatalf("unexpected entry: %#v", got)
	}
	if !got.Timestamp.Equal(now) {
		t.Fatalf("unexpected timestamp: %s", got.Timestamp)
	}
}

func TestGetWorkflowLogsHappyPath(t *testing.T) {
	now := time.Now().UTC()
	api := &queryStubLogsAPI{
		resultsQueue: [][]cwltypes.ResultField{
			{
				{Field: aws.String("@timestamp"), Value: aws.String(now.Format("2006-01-02 15:04:05.000"))},
				{Field: aws.String("@message"), Value: aws.String("running step")},
			},
		},
	}
	c := newQueryTestClient(api)
	res, err := c.GetWorkflowLogs(context.Background(), WorkflowLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("GetWorkflowLogs() error = %v", err)
	}
	if len(res.Logs) != 1 || res.Logs[0].Log != "running step" {
		t.Fatalf("unexpected logs: %#v", res.Logs)
	}
}

func TestRunQueryReturnsErrorWhenStartTimeMissing(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{})
	_, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{Namespace: "default"})
	if err == nil {
		t.Fatal("expected error for missing start/end time")
	}
}

func TestRunQueryRejectsEndTimeBeforeStartTime(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{})
	now := time.Now()
	_, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default",
		StartTime: now,
		EndTime:   now.Add(-time.Minute),
	})
	if err == nil {
		t.Fatal("expected error for endTime before startTime")
	}
}

func TestRunQueryStartQueryError(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{startErr: errors.New("boom")})
	now := time.Now()
	_, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err == nil || !strings.Contains(err.Error(), "start_query") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunQueryGetResultsError(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{resultsErr: errors.New("results boom")})
	now := time.Now()
	_, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err == nil || !strings.Contains(err.Error(), "get_query_results") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunQueryFailedStatusReturnsError(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{
		statusQueue: []cwltypes.QueryStatus{cwltypes.QueryStatusFailed},
	})
	now := time.Now()
	_, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err == nil || !strings.Contains(err.Error(), "Failed") && !strings.Contains(strings.ToLower(err.Error()), "status") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunQueryCancelledByContext(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{
		statusQueue: []cwltypes.QueryStatus{cwltypes.QueryStatusRunning, cwltypes.QueryStatusRunning, cwltypes.QueryStatusRunning},
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	now := time.Now()
	_, err := c.GetComponentLogs(ctx, ComponentLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err == nil {
		t.Fatal("expected ctx cancel error")
	}
}

func TestRunQueryDeadlineExceededTriggersStopQuery(t *testing.T) {
	api := &queryStubLogsAPI{
		// PollEvery > QueryTimeout below already guarantees the deadline fires
		// before the queue is exhausted, but keep enough Running entries so a
		// future tweak to the timing constants cannot accidentally let the stub
		// fall through to Complete and skip the StopQuery path.
		statusQueue: []cwltypes.QueryStatus{
			cwltypes.QueryStatusRunning,
			cwltypes.QueryStatusRunning,
			cwltypes.QueryStatusRunning,
			cwltypes.QueryStatusRunning,
			cwltypes.QueryStatusRunning,
			cwltypes.QueryStatusRunning,
		},
	}
	c := NewClientWithAWS(api, &stubAlarmsAPI{}, &stsStub{}, Config{
		ClusterName:    "test-cluster",
		LogGroupPrefix: "/aws/containerinsights",
		// PollEvery is intentionally larger than QueryTimeout: after the first
		// GetQueryResults returns Running we sleep for PollEvery, so the next
		// deadline check is guaranteed to be past QueryTimeout regardless of CI
		// scheduling jitter.
		QueryTimeout: 5 * time.Millisecond,
		PollEvery:    50 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	_, err := c.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
	if api.stopCalls == 0 {
		t.Fatal("expected StopQuery to be called on timeout")
	}
}

func TestParseInsightsTimestampLayouts(t *testing.T) {
	tests := []struct {
		input string
		want  time.Time
	}{
		{"2026-04-23 10:00:05.000", time.Date(2026, 4, 23, 10, 0, 5, 0, time.UTC)},
		{"2026-04-23 10:00:05", time.Date(2026, 4, 23, 10, 0, 5, 0, time.UTC)},
		{"2026-04-23T10:00:05Z", time.Date(2026, 4, 23, 10, 0, 5, 0, time.UTC)},
	}
	for _, test := range tests {
		got, err := parseInsightsTimestamp(test.input)
		if err != nil {
			t.Fatalf("parseInsightsTimestamp(%q) error = %v", test.input, err)
		}
		if !got.Equal(test.want) {
			t.Fatalf("parseInsightsTimestamp(%q) = %s, want %s", test.input, got, test.want)
		}
	}
	if _, err := parseInsightsTimestamp(""); err == nil {
		t.Fatal("expected empty input to error")
	}
	if _, err := parseInsightsTimestamp("not-a-timestamp"); err == nil {
		t.Fatal("expected unrecognised input to error")
	}
}

func TestApplicationLogGroup(t *testing.T) {
	c := newQueryTestClient(&queryStubLogsAPI{})
	if got := c.applicationLogGroup(); got != "/aws/containerinsights/test-cluster/application" {
		t.Fatalf("applicationLogGroup() = %q", got)
	}
}

func TestNewClientReturnsErrorOnInvalidAWSConfig(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", "")
	// LoadDefaultConfig succeeds even without region — but this still exercises the constructor's success path.
	c, err := NewClient(context.Background(), Config{Region: "eu-north-1", ClusterName: "x", LogGroupPrefix: "/aws/containerinsights"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}
