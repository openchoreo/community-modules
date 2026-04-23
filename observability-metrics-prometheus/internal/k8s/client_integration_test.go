// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// createFakeK8sClient creates a fake Kubernetes client for testing
func createFakeK8sClient(namespace string, objects ...runtime.Object) *Client {
	scheme := runtime.NewScheme()
	_ = monitoringv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		Build()

	return &Client{
		client:    fakeClient,
		namespace: namespace,
	}
}

// NewFakeClient creates a fake Kubernetes client for testing purposes.
// This is exported for use in other package tests.
func NewFakeClient(namespace string, objects ...runtime.Object) *Client {
	return createFakeK8sClient(namespace, objects...)
}

func TestCreatePrometheusRule(t *testing.T) {
	client := createFakeK8sClient("test-namespace")

	rule := &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "test-namespace",
		},
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name: "test-group",
					Rules: []monitoringv1.Rule{
						{
							Alert: "TestAlert",
							Expr:  intstr.FromString("up == 0"),
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	err := client.CreatePrometheusRule(ctx, rule)
	if err != nil {
		t.Fatalf("CreatePrometheusRule() failed: %v", err)
	}

	// Verify the rule was created
	retrieved, err := client.GetPrometheusRule(ctx, "test-rule")
	if err != nil {
		t.Fatalf("GetPrometheusRule() failed: %v", err)
	}

	if retrieved.Name != "test-rule" {
		t.Errorf("expected rule name test-rule, got %s", retrieved.Name)
	}
}

func TestGetPrometheusRule(t *testing.T) {
	existingRule := &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-rule",
			Namespace: "test-namespace",
		},
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name: "test-group",
				},
			},
		},
	}

	client := createFakeK8sClient("test-namespace", existingRule)

	ctx := context.Background()
	retrieved, err := client.GetPrometheusRule(ctx, "existing-rule")
	if err != nil {
		t.Fatalf("GetPrometheusRule() failed: %v", err)
	}

	if retrieved.Name != "existing-rule" {
		t.Errorf("expected rule name existing-rule, got %s", retrieved.Name)
	}
}

func TestGetPrometheusRule_NotFound(t *testing.T) {
	client := createFakeK8sClient("test-namespace")

	ctx := context.Background()
	_, err := client.GetPrometheusRule(ctx, "nonexistent-rule")
	if err == nil {
		t.Fatal("expected error for nonexistent rule, got nil")
	}

	if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound error, got: %v", err)
	}
}

func TestUpdatePrometheusRule(t *testing.T) {
	existingRule := &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "test-namespace",
		},
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name: "old-group",
				},
			},
		},
	}

	client := createFakeK8sClient("test-namespace", existingRule)

	ctx := context.Background()

	// Get the existing rule
	rule, err := client.GetPrometheusRule(ctx, "test-rule")
	if err != nil {
		t.Fatalf("GetPrometheusRule() failed: %v", err)
	}

	// Update the rule
	rule.Spec.Groups[0].Name = "new-group"
	err = client.UpdatePrometheusRule(ctx, rule)
	if err != nil {
		t.Fatalf("UpdatePrometheusRule() failed: %v", err)
	}

	// Verify the update
	updated, err := client.GetPrometheusRule(ctx, "test-rule")
	if err != nil {
		t.Fatalf("GetPrometheusRule() failed after update: %v", err)
	}

	if updated.Spec.Groups[0].Name != "new-group" {
		t.Errorf("expected group name new-group, got %s", updated.Spec.Groups[0].Name)
	}
}

func TestDeletePrometheusRule(t *testing.T) {
	existingRule := &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "test-namespace",
		},
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{},
		},
	}

	client := createFakeK8sClient("test-namespace", existingRule)

	ctx := context.Background()
	err := client.DeletePrometheusRule(ctx, "test-rule")
	if err != nil {
		t.Fatalf("DeletePrometheusRule() failed: %v", err)
	}

	// Verify the rule was deleted
	_, err = client.GetPrometheusRule(ctx, "test-rule")
	if err == nil {
		t.Fatal("expected error after deletion, got nil")
	}

	if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound error after deletion, got: %v", err)
	}
}

func TestDeletePrometheusRule_NotFound(t *testing.T) {
	client := createFakeK8sClient("test-namespace")

	ctx := context.Background()
	err := client.DeletePrometheusRule(ctx, "nonexistent-rule")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent rule, got nil")
	}
}

func TestPrometheusRuleExists(t *testing.T) {
	existingRule := &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-rule",
			Namespace: "test-namespace",
		},
		Spec: monitoringv1.PrometheusRuleSpec{},
	}

	client := createFakeK8sClient("test-namespace", existingRule)

	ctx := context.Background()

	// Test for existing rule
	exists, err := client.PrometheusRuleExists(ctx, "existing-rule")
	if err != nil {
		t.Fatalf("PrometheusRuleExists() failed: %v", err)
	}
	if !exists {
		t.Error("expected rule to exist")
	}

	// Test for non-existing rule
	exists, err = client.PrometheusRuleExists(ctx, "nonexistent-rule")
	if err != nil {
		t.Fatalf("PrometheusRuleExists() failed: %v", err)
	}
	if exists {
		t.Error("expected rule to not exist")
	}
}
