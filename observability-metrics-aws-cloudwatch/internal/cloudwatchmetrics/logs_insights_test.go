// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// stubLogsAPI is a minimal logsAPI for exercising runLogsInsightsQuery.
type stubLogsAPI struct {
	startErr error
}

func (s *stubLogsAPI) StartQuery(context.Context, *cloudwatchlogs.StartQueryInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error) {
	return nil, s.startErr
}

func (s *stubLogsAPI) GetQueryResults(context.Context, *cloudwatchlogs.GetQueryResultsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	return nil, errors.New("not implemented")
}

func (s *stubLogsAPI) StopQuery(context.Context, *cloudwatchlogs.StopQueryInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StopQueryOutput, error) {
	return nil, nil
}

// A missing EMF log group must degrade to empty results, not surface as an
// error (which the handlers would turn into a 500). Mirrors the Prometheus
// reference and repo commit 7fa15e0.
func TestRunLogsInsightsQueryLogGroupNotFoundReturnsEmpty(t *testing.T) {
	logs := &stubLogsAPI{startErr: &fakeAPIError{code: "ResourceNotFoundException"}}
	c := NewClientWithAWS(&stubCloudWatchAPI{}, logs, &stubSTSAPI{}, Config{}, nil)

	rows, err := c.runLogsInsightsQuery(context.Background(), "fields @timestamp",
		time.Unix(1000, 0), time.Unix(2000, 0))
	if err != nil {
		t.Fatalf("expected nil error for missing log group, got %v", err)
	}
	if rows != nil {
		t.Fatalf("expected nil rows for missing log group, got %#v", rows)
	}
}

// Any other StartQuery error must still propagate.
func TestRunLogsInsightsQueryOtherErrorPropagates(t *testing.T) {
	logs := &stubLogsAPI{startErr: errors.New("throttled")}
	c := NewClientWithAWS(&stubCloudWatchAPI{}, logs, &stubSTSAPI{}, Config{}, nil)

	_, err := c.runLogsInsightsQuery(context.Background(), "fields @timestamp",
		time.Unix(1000, 0), time.Unix(2000, 0))
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
