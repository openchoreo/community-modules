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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudlogging"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudmonitoring"
)

const (
	testComponentUID = "846bb581-d6ba-446d-a160-8cec468d2219"
	testLineUID      = "93f65294-926f-475b-98f1-dc4f152f38da"
)

// fakeGCP records the filters the adapter sends to Cloud Logging and Cloud Monitoring.
type fakeGCP struct {
	loggingpb.UnimplementedLoggingServiceV2Server
	loggingpb.UnimplementedMetricsServiceV2Server
	monitoringpb.UnimplementedAlertPolicyServiceServer

	mu             sync.Mutex
	entries        []*loggingpb.LogEntry
	entriesFilter  string
	metricFilter   string
	existingPolicy *monitoringpb.AlertPolicy
}

func (f *fakeGCP) ListLogEntries(_ context.Context, req *loggingpb.ListLogEntriesRequest) (*loggingpb.ListLogEntriesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entriesFilter = req.GetFilter()
	return &loggingpb.ListLogEntriesResponse{Entries: f.entries}, nil
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
		_ = metrics.Close()
		_ = alerts.Close()
	})
	return NewLogsHandler(logsClient, alerts, nil, logger)
}

func TestQueryLogs_ReleaseLineScope(t *testing.T) {
	tests := map[string]struct {
		component  *string
		line       *string
		allLines   *bool
		want       []string
		notWant    []string
		wantHeader string
	}{
		"one line": {
			component: ptr(testComponentUID), line: ptr(testLineUID),
			want:       []string{`labels."k8s-pod/openchoreo_dev/release-line-uid"="` + testLineUID + `"`},
			notWant:    []string{"component-uid"},
			wantHeader: "true",
		},
		"every line": {
			component: ptr(testComponentUID), allLines: ptr(true),
			want:       []string{`labels."k8s-pod/openchoreo_dev/release-line-component-uid"="` + testComponentUID + `"`},
			wantHeader: "true",
		},
		"main line": {
			component:  ptr(testComponentUID),
			want:       []string{`NOT labels."k8s-pod/openchoreo_dev/release-line-uid":*`},
			wantHeader: "true",
		},
		"namespace only": {
			allLines: ptr(true),
			notWant:  []string{"component-uid", "release-line"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGCP{entries: []*loggingpb.LogEntry{{
				LogName:   "projects/test/logs/stdout",
				Timestamp: timestamppb.New(time.Date(2026, 6, 15, 0, 30, 0, 0, time.UTC)),
				Payload:   &loggingpb.LogEntry_TextPayload{TextPayload: "hello"},
				Labels: map[string]string{
					"k8s-pod/openchoreo_dev/release-line-component-uid": testComponentUID,
					"k8s-pod/openchoreo_dev/release-line-uid":           testLineUID,
				},
			}}}
			h := newFakeHandler(t, fake)

			scope := gen.LogsQueryRequest_SearchScope{}
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: tt.component, ReleaseLineUid: tt.line, AllReleaseLines: tt.allLines,
			})
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

			for _, w := range tt.want {
				if !strings.Contains(fake.entriesFilter, w) {
					t.Errorf("the Cloud Logging filter is missing %q:\n%s", w, fake.entriesFilter)
				}
			}
			for _, nw := range tt.notWant {
				if strings.Contains(fake.entriesFilter, nw) {
					t.Errorf("the Cloud Logging filter must not contain %q:\n%s", nw, fake.entriesFilter)
				}
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != tt.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, tt.wantHeader)
			}

			entries, err := ok.Body.Logs.AsLogsQueryResponseLogs0()
			if err != nil || len(entries) != 1 {
				t.Fatalf("expected one component log entry, got %v (%v)", entries, err)
			}
			if md := entries[0].Metadata; md == nil || md.ComponentUid == nil || md.ComponentUid.String() != testComponentUID {
				t.Errorf("a release line entry must be attributed to its component, got %+v", md)
			}
		})
	}
}

func TestQueryLogs_WorkflowNeverSetsReleaseLineHeader(t *testing.T) {
	h := newFakeHandler(t, &fakeGCP{})
	scope := gen.LogsQueryRequest_SearchScope{}
	_ = scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: ptr("run-1")})
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
	_ = req.Metadata.ComponentUid.Scan(testComponentUID)
	if line != nil {
		var uid openapi_types.UUID
		_ = uid.Scan(*line)
		req.Metadata.ReleaseLineUid = &uid
	}
	return req
}

// assertRuleScoped checks the metric filter against the rule's line.
func assertRuleScoped(t *testing.T, fake *fakeGCP, line *string) {
	t.Helper()
	lineClause := `labels."k8s-pod/openchoreo_dev/release-line-uid"="` + testLineUID + `"`
	if line == nil {
		if !strings.Contains(fake.metricFilter, `NOT labels."k8s-pod/openchoreo_dev/release-line-uid":*`) ||
			strings.Contains(fake.metricFilter, lineClause) {
			t.Errorf("a main line rule must exclude release line entries:\n%s", fake.metricFilter)
		}
		return
	}
	if !strings.Contains(fake.metricFilter, lineClause) || strings.Contains(fake.metricFilter, "component-uid") {
		t.Errorf("a release line rule must match the line UID alone:\n%s", fake.metricFilter)
	}
}

func lineCases() map[string]*string {
	return map[string]*string{"release line": ptr(testLineUID), "main line": nil}
}

func TestCreateAlertRule_ReleaseLine(t *testing.T) {
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
			assertRuleScoped(t, fake, line)
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

func TestUpdateAlertRule_ReleaseLine(t *testing.T) {
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
			assertRuleScoped(t, fake, line)
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

// A raw Cloud Logging filter is used verbatim, so a release line rule can't
// use one.
func TestAlertRule_ReleaseLineRejectsRawFilter(t *testing.T) {
	wantRejected := func(t *testing.T, got gen.ErrorResponse, fake *fakeGCP) {
		t.Helper()
		if got.Message == nil || !strings.Contains(*got.Message, "raw Cloud Logging filter") {
			t.Errorf("400 message = %v, want it to name the raw filter", got.Message)
		}
		if fake.metricFilter != "" {
			t.Errorf("the rule reached Cloud Logging: %q", fake.metricFilter)
		}
	}

	t.Run("create", func(t *testing.T) {
		fake := &fakeGCP{}
		h := newFakeHandler(t, fake)
		body := alertRequestWithLine(ptr(testLineUID))
		body.Source.Query = `severity>=ERROR`
		resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &body})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, is := resp.(gen.CreateAlertRule400JSONResponse)
		if !is {
			t.Fatalf("expected 400 response, got %T", resp)
		}
		wantRejected(t, gen.ErrorResponse(got), fake)
	})
	t.Run("update", func(t *testing.T) {
		fake := &fakeGCP{existingPolicy: &monitoringpb.AlertPolicy{Name: "projects/test/alertPolicies/1"}}
		h := newFakeHandler(t, fake)
		body := gen.UpdateAlertRuleJSONRequestBody(alertRequestWithLine(ptr(testLineUID)))
		body.Source.Query = `severity>=ERROR`
		resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{RuleName: body.Metadata.Name, Body: &body})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, is := resp.(gen.UpdateAlertRule400JSONResponse)
		if !is {
			t.Fatalf("expected 400 response, got %T", resp)
		}
		wantRejected(t, gen.ErrorResponse(got), fake)
	})
}

// A main line rule may still use a raw filter, which is used verbatim and so
// withholds the header.
func TestCreateAlertRule_MainLineRawFilterIsNotLineScoped(t *testing.T) {
	fake := &fakeGCP{}
	h := newFakeHandler(t, fake)
	body := alertRequestWithLine(nil)
	body.Source.Query = `severity>=ERROR`
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.CreateAlertRule201JSONResponse)
	if !is {
		t.Fatalf("expected 201 response, got %T", resp)
	}
	if fake.metricFilter != `severity>=ERROR` {
		t.Errorf("metric filter = %q, want the raw filter verbatim", fake.metricFilter)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty for a raw filter", got)
	}
}

func TestAuditAndPlatformLogs_NotImplemented(t *testing.T) {
	h := &LogsHandler{}
	ctx := context.Background()
	responses := map[string]any{}
	responses["audit logs"], _ = h.QueryAuditLogs(ctx, gen.QueryAuditLogsRequestObject{})
	responses["audit log filter values"], _ = h.QueryAuditLogFilterValues(ctx, gen.QueryAuditLogFilterValuesRequestObject{})
	responses["platform logs"], _ = h.QueryPlatformLogs(ctx, gen.QueryPlatformLogsRequestObject{})
	responses["platform log filter values"], _ = h.QueryPlatformLogFilterValues(ctx, gen.QueryPlatformLogFilterValuesRequestObject{})
	for name, resp := range responses {
		switch resp.(type) {
		case gen.QueryAuditLogs501JSONResponse, gen.QueryAuditLogFilterValues501JSONResponse,
			gen.QueryPlatformLogs501JSONResponse, gen.QueryPlatformLogFilterValues501JSONResponse:
		default:
			t.Errorf("%s: response type = %T, want a 501", name, resp)
		}
	}
}

func ptr[T any](v T) *T { return &v }
