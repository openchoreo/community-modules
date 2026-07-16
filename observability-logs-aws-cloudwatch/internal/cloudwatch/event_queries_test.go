// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"strings"
	"testing"
)

func TestBuildComponentEventsQueryProjectsEventColumns(t *testing.T) {
	query := buildComponentEventsQuery(ComponentEventsParams{
		Namespace: "default",
	})

	// Event columns must use the OTEL/awscloudwatchlogsexporter field shape, not the
	// Fluent Bit kubernetes.labels.* shape used by container logs.
	wantColumns := []string{
		"`body` as message",
		"`severity_text` as type",
		"`attributes.k8s.event.reason` as reason",
		"`resource.k8s.object.kind` as objectKind",
		"`resource.k8s.object.name` as objectName",
		"`attributes.k8s.namespace.name` as objectNamespace",
		"`resource.k8s.object.label.openchoreo.dev/component-uid` as componentUid",
		"`resource.k8s.object.label.openchoreo.dev/namespace` as namespaceName",
	}
	for _, want := range wantColumns {
		if !strings.Contains(query, want) {
			t.Errorf("query missing column %q\nquery:\n%s", want, query)
		}
	}

	if strings.Contains(query, "kubernetes.labels.") {
		t.Errorf("events query must not reference the Fluent Bit kubernetes.labels.* shape\nquery:\n%s", query)
	}
}

func TestBuildComponentEventsQueryFiltersScope(t *testing.T) {
	query := buildComponentEventsQuery(ComponentEventsParams{
		Namespace:     "default",
		ProjectID:     "proj-1",
		EnvironmentID: "env-1",
		ComponentID:   "comp-1",
		SortOrder:     "asc",
		Limit:         50,
	})

	wantFilters := []string{
		"| filter `resource.k8s.object.label.openchoreo.dev/namespace` = \"default\"",
		"| filter `resource.k8s.object.label.openchoreo.dev/project-uid` = \"proj-1\"",
		"| filter `resource.k8s.object.label.openchoreo.dev/environment-uid` = \"env-1\"",
		"| filter `resource.k8s.object.label.openchoreo.dev/component-uid` = \"comp-1\"",
		"| sort @timestamp asc",
		"| limit 50",
	}
	for _, want := range wantFilters {
		if !strings.Contains(query, want) {
			t.Errorf("query missing %q\nquery:\n%s", want, query)
		}
	}
}

func TestBuildComponentEventsQueryOmitsUnsetScope(t *testing.T) {
	query := buildComponentEventsQuery(ComponentEventsParams{Namespace: "default"})

	for _, unwanted := range []string{"project-uid", "environment-uid", "component-uid"} {
		if strings.Contains(query, "filter `resource.k8s.object.label.openchoreo.dev/"+unwanted+"`") {
			t.Errorf("query should not filter on unset %s\nquery:\n%s", unwanted, query)
		}
	}
	// Default sort order is descending.
	if !strings.Contains(query, "| sort @timestamp desc") {
		t.Errorf("expected default desc sort\nquery:\n%s", query)
	}
}

func TestBuildWorkflowEventsQueryMatchesObjectNameAndNamespace(t *testing.T) {
	query := buildWorkflowEventsQuery(WorkflowEventsParams{
		Namespace:       "default",
		WorkflowRunName: "build-123",
		TaskName:        "clone-step",
	})

	wantFilters := []string{
		"| filter `attributes.k8s.namespace.name` = \"workflows-default\"",
		"| filter `resource.k8s.object.name` like /^build-123(-|$)/",
		"| filter `resource.k8s.object.name` like \"clone-step\" or `body` like \"clone-step\" or `resource.k8s.object.name` like /^build-123-clone-step(-|$)/ or `resource.k8s.object.name` like /^build-123-clone(-|$)/",
	}
	for _, want := range wantFilters {
		if !strings.Contains(query, want) {
			t.Errorf("query missing %q\nquery:\n%s", want, query)
		}
	}
}

func TestBuildWorkflowEventsQueryMatchesCheckoutTemplateObjectName(t *testing.T) {
	query := buildWorkflowEventsQuery(WorkflowEventsParams{
		Namespace:       "default",
		WorkflowRunName: "test-service-1783662896161",
		TaskName:        "checkout-source",
	})

	want := "`resource.k8s.object.name` like /^test-service-1783662896161-checkout(-|$)/"
	if !strings.Contains(query, want) {
		t.Errorf("query missing checkout template object-name filter %q\nquery:\n%s", want, query)
	}
}

func TestBuildWorkflowEventsQueryWithoutRunName(t *testing.T) {
	query := buildWorkflowEventsQuery(WorkflowEventsParams{Namespace: "default"})

	if !strings.Contains(query, "| filter `attributes.k8s.namespace.name` = \"workflows-default\"") {
		t.Errorf("expected namespace filter\nquery:\n%s", query)
	}
	if strings.Contains(query, "`resource.k8s.object.name` like") {
		t.Errorf("query should not add an object-name filter when run name is empty\nquery:\n%s", query)
	}
}
