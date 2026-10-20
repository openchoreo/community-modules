// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/logging/apiv2/loggingpb"
	"cloud.google.com/go/logging/logadmin"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudlogging"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudmonitoring"
)

const testLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

// fakeGCP records the filters the adapter sends to Cloud Logging and Cloud Monitoring.
type fakeGCP struct {
	loggingpb.UnimplementedLoggingServiceV2Server
	loggingpb.UnimplementedMetricsServiceV2Server
	monitoringpb.UnimplementedAlertPolicyServiceServer

	mu             sync.Mutex
	entriesFilter  string
	metricFilter   string
	existingPolicy *monitoringpb.AlertPolicy
}

func (f *fakeGCP) ListLogEntries(_ context.Context, req *loggingpb.ListLogEntriesRequest) (*loggingpb.ListLogEntriesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entriesFilter = req.GetFilter()
	return &loggingpb.ListLogEntriesResponse{}, nil
}

func (f *fakeGCP) CreateLogMetric(_ context.Context, req *loggingpb.CreateLogMetricRequest) (*loggingpb.LogMetric, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metricFilter = req.GetMetric().GetFilter()
	return req.GetMetric(), nil
}

func (f *fakeGCP) UpdateLogMetric(_ context.Context, req *loggingpb.UpdateLogMetricRequest) (*loggingpb.LogMetric, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metricFilter = req.GetMetric().GetFilter()
	return req.GetMetric(), nil
}

func (f *fakeGCP) ListAlertPolicies(_ context.Context, _ *monitoringpb.ListAlertPoliciesRequest) (*monitoringpb.ListAlertPoliciesResponse, error) {
	if f.existingPolicy == nil {
		return &monitoringpb.ListAlertPoliciesResponse{}, nil
	}
	return &monitoringpb.ListAlertPoliciesResponse{AlertPolicies: []*monitoringpb.AlertPolicy{f.existingPolicy}}, nil
}

func (f *fakeGCP) CreateAlertPolicy(_ context.Context, req *monitoringpb.CreateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	p := req.GetAlertPolicy()
	p.Name = "projects/test/alertPolicies/1"
	return p, nil
}

func (f *fakeGCP) UpdateAlertPolicy(_ context.Context, req *monitoringpb.UpdateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	return req.GetAlertPolicy(), nil
}

// newFakeHandler serves fakeGCP over gRPC and returns a handler wired to it.
func newFakeHandler(t *testing.T, fake *fakeGCP) *LogsHandler {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	loggingpb.RegisterLoggingServiceV2Server(srv, fake)
	loggingpb.RegisterMetricsServiceV2Server(srv, fake)
	monitoringpb.RegisterAlertPolicyServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	opts := []option.ClientOption{
		option.WithEndpoint(lis.Addr().String()),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	logsClient, err := cloudlogging.NewClient(ctx, cloudlogging.Config{ProjectID: "test"}, logger, opts...)
	if err != nil {
		t.Fatalf("cloudlogging client: %v", err)
	}
	metrics, err := logadmin.NewClient(ctx, "test", opts...)
	if err != nil {
		t.Fatalf("logadmin client: %v", err)
	}
	alerts, err := cloudmonitoring.NewClient(ctx, metrics, cloudmonitoring.Config{ProjectID: "test"}, logger, opts...)
	if err != nil {
		t.Fatalf("cloudmonitoring client: %v", err)
	}
	t.Cleanup(func() {
		_ = logsClient.Close()
		_ = alerts.Close()
	})
	return NewLogsHandler(logsClient, alerts, nil, logger)
}

func lineCases() map[string]*string {
	line := testLineUID
	return map[string]*string{"with a line": &line, "without a line": nil}
}

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGCP{}
			h := newFakeHandler(t, fake)

			scope := gen.LogsQueryRequest_SearchScope{}
			comp := "846bb581-d6ba-446d-a160-8cec468d2219"
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{Namespace: "default", ComponentUid: &comp, ReleaseLineUid: line})
			resp, err := h.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
				StartTime:   time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
				EndTime:     time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC),
				SearchScope: scope,
			}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.QueryLogs200JSONResponse)
			if !is {
				t.Fatalf("expected 200 response, got %T", resp)
			}

			wantHeader := ""
			hasLine := strings.Contains(fake.entriesFilter, `labels."k8s-pod/openchoreo_dev/release-line-uid"="`+testLineUID+`"`)
			if line != nil {
				wantHeader = "true"
				if !hasLine {
					t.Errorf("the release line did not reach the Cloud Logging filter:\n%s", fake.entriesFilter)
				}
			} else if hasLine {
				t.Errorf("unexpected release line clause:\n%s", fake.entriesFilter)
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, wantHeader)
			}
		})
	}
}

func TestQueryLogs_WorkflowNeverSetsReleaseLineHeader(t *testing.T) {
	h := newFakeHandler(t, &fakeGCP{})
	scope := gen.LogsQueryRequest_SearchScope{}
	run := "run-1"
	_ = scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: &run})
	resp, err := h.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
		StartTime:   time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC),
		SearchScope: scope,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.QueryLogs200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
}

func alertRequestWithLine(line *string) gen.AlertRuleRequest {
	req := validAlertRequest()
	if line != nil {
		var uid openapi_types.UUID
		_ = uid.Scan(*line)
		req.Metadata.ReleaseLineUid = &uid
	}
	return req
}

func assertMetricScoped(t *testing.T, fake *fakeGCP, line *string) string {
	t.Helper()
	hasLine := strings.Contains(fake.metricFilter, `labels."k8s-pod/openchoreo_dev/release-line-uid"="`+testLineUID+`"`)
	if line == nil {
		if hasLine {
			t.Errorf("unexpected release line clause in the metric filter:\n%s", fake.metricFilter)
		}
		return ""
	}
	if !hasLine {
		t.Errorf("the release line did not reach the metric filter:\n%s", fake.metricFilter)
	}
	return "true"
}

func TestCreateAlertRule_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGCP{}
			h := newFakeHandler(t, fake)
			body := alertRequestWithLine(line)
			resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.CreateAlertRule201JSONResponse)
			if !is {
				t.Fatalf("expected 201 response, got %T (%+v)", resp, resp)
			}
			want := assertMetricScoped(t, fake, line)
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != want {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateAlertRule_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGCP{existingPolicy: &monitoringpb.AlertPolicy{Name: "projects/test/alertPolicies/1"}}
			h := newFakeHandler(t, fake)
			body := gen.UpdateAlertRuleJSONRequestBody(alertRequestWithLine(line))
			resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{RuleName: body.Metadata.Name, Body: &body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.UpdateAlertRule200JSONResponse)
			if !is {
				t.Fatalf("expected 200 response, got %T (%+v)", resp, resp)
			}
			want := assertMetricScoped(t, fake, line)
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != want {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
			}
		})
	}
}

func TestCreateAlertRule_RawFilterIsNotLineScoped(t *testing.T) {
	fake := &fakeGCP{}
	h := newFakeHandler(t, fake)
	line := testLineUID
	body := alertRequestWithLine(&line)
	body.Source.Query = `severity>=ERROR`
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.CreateAlertRule201JSONResponse)
	if !is {
		t.Fatalf("expected 201 response, got %T", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty for a raw filter", got)
	}
}
