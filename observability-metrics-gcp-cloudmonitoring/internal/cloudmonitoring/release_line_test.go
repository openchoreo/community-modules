// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

var (
	componentClause            = `metadata.user_labels."openchoreo.dev/component-uid" = "` + testComponentUID + `"`
	releaseLineComponentClause = `metadata.user_labels."openchoreo.dev/release-line-component-uid" = "` + testComponentUID + `"`
	releaseLineClause          = `metadata.user_labels."openchoreo.dev/release-line-uid" = "` + testReleaseLineUID + `"`
)

func TestBuildResourceMetricsFilters_ReleaseLine(t *testing.T) {
	tests := []struct {
		name        string
		p           MetricsQueryParams
		want        []string // one clause per filter
		notWant     []string
		wantApplied bool
	}{
		{
			name:    "main line",
			p:       MetricsQueryParams{ComponentUID: testComponentUID},
			want:    []string{componentClause},
			notWant: []string{"release-line"},
		},
		{
			name:        "one release line matches its UID alone",
			p:           MetricsQueryParams{ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID},
			want:        []string{releaseLineClause},
			notWant:     []string{"component-uid"},
			wantApplied: true,
		},
		{
			name:        "one release line takes precedence over every line",
			p:           MetricsQueryParams{ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID, AllReleaseLines: true},
			want:        []string{releaseLineClause},
			notWant:     []string{"component-uid"},
			wantApplied: true,
		},
		{
			name:        "every line takes a filter per component label",
			p:           MetricsQueryParams{ComponentUID: testComponentUID, AllReleaseLines: true},
			want:        []string{componentClause, releaseLineComponentClause},
			wantApplied: true,
		},
		{
			name:    "every line without a component",
			p:       MetricsQueryParams{AllReleaseLines: true},
			want:    []string{`metric.type = "kubernetes.io/container/cpu/core_usage_time"`},
			notWant: []string{"component-uid", "release-line"},
		},
		{
			name:    "zero UUID release line is the main line",
			p:       MetricsQueryParams{ComponentUID: testComponentUID, ReleaseLineUID: zeroUUID},
			want:    []string{componentClause},
			notWant: []string{"release-line"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.p.ProjectUID = testProjectUID
			filters := BuildResourceMetricsFilters(specByKey(t, "cpuUsage"), tt.p)
			if len(filters) != len(tt.want) {
				t.Fatalf("got %d filters, want %d: %q", len(filters), len(tt.want), filters)
			}
			for i, f := range filters {
				if !strings.Contains(f, tt.want[i]) {
					t.Errorf("filter %d missing %q:\n%s", i, tt.want[i], f)
				}
				if !strings.Contains(f, `metadata.user_labels."openchoreo.dev/project-uid" = "`+testProjectUID+`"`) {
					t.Errorf("filter %d lost the project clause:\n%s", i, f)
				}
				for _, nw := range tt.notWant {
					if strings.Contains(f, nw) {
						t.Errorf("filter %d must not contain %q:\n%s", i, nw, f)
					}
				}
			}
			if got := ReleaseLineApplied(tt.p); got != tt.wantApplied {
				t.Errorf("ReleaseLineApplied = %v, want %v", got, tt.wantApplied)
			}
		})
	}
}

// Every line of a component reads each metric under both component labels and
// adds the series up.
func TestGetResourceMetrics_EveryLineSumsBothLabels(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var (
		mu      sync.Mutex
		filters []string
	)
	c := newClientWithLister(Config{ProjectID: "my-project"}, testLogger(), func(_ context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
		mu.Lock()
		filters = append(filters, req.Filter)
		mu.Unlock()
		v := 1.0
		if strings.Contains(req.Filter, "release-line-component-uid") {
			v = 2
		}
		return []*monitoringpb.TimeSeries{{
			Points: []*monitoringpb.Point{{
				Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(t0)},
				Value:    &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: v}},
			}},
		}}, nil
	})

	result, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "default", ComponentUID: testComponentUID, AllReleaseLines: true,
		StartTime: t0.Add(-time.Hour), EndTime: t0, Step: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filters) != 2*len(resourceMetricSpecs) {
		t.Errorf("ran %d queries, want %d", len(filters), 2*len(resourceMetricSpecs))
	}
	if len(result.CPUUsage) != 1 || result.CPUUsage[0].Value != 3 {
		t.Errorf("cpuUsage = %+v, want the main and release line series added up to 3", result.CPUUsage)
	}
}

func TestToAlertPolicy_ReleaseLine(t *testing.T) {
	in := validRuleInput()
	p, err := ToAlertPolicy(in, baseTranslatorCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mt := p.GetConditions()[0].GetConditionThreshold()
	if !strings.Contains(mt.GetFilter(), componentClause) || strings.Contains(mt.GetFilter(), "release-line") {
		t.Errorf("a main line rule must match the component UID: %s", mt.GetFilter())
	}
	if _, ok := p.GetUserLabels()["openchoreo_release_line_uid"]; ok {
		t.Errorf("a main line rule must not carry a release line label: %v", p.GetUserLabels())
	}

	in.ReleaseLineUID = testReleaseLineUID
	p, err = ToAlertPolicy(in, baseTranslatorCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mt = p.GetConditions()[0].GetConditionThreshold()
	for name, f := range map[string]string{"numerator": mt.GetFilter(), "denominator": mt.GetDenominatorFilter()} {
		if !strings.Contains(f, releaseLineClause) || strings.Contains(f, "component-uid") {
			t.Errorf("a release line rule's %s must match the line UID alone: %s", name, f)
		}
	}
	if got := p.GetUserLabels()["openchoreo_release_line_uid"]; got != testReleaseLineUID {
		t.Errorf("release line label = %q, want %q", got, testReleaseLineUID)
	}
	if got := p.GetUserLabels()["openchoreo_component_uid"]; got != testComponentUID {
		t.Errorf("component label = %q, want %q", got, testComponentUID)
	}
}
