// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client wraps a Kubernetes client for PrometheusRule CRUD operations.
type Client struct {
	client    client.Client
	namespace string
}

// NewClient creates a new Kubernetes client. It works both in-cluster and with kubeconfig.
func NewClient(namespace string) (*Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Client{
		client:    c,
		namespace: namespace,
	}, nil
}

// CreatePrometheusRule creates a PrometheusRule in the configured namespace.
func (c *Client) CreatePrometheusRule(ctx context.Context, rule *monitoringv1.PrometheusRule) error {
	if err := c.client.Create(ctx, rule); err != nil {
		return fmt.Errorf("failed to create PrometheusRule: %w", err)
	}
	return nil
}

// GetPrometheusRule retrieves a PrometheusRule by name from the configured namespace.
func (c *Client) GetPrometheusRule(ctx context.Context, name string) (*monitoringv1.PrometheusRule, error) {
	rule := &monitoringv1.PrometheusRule{}
	err := c.client.Get(ctx, client.ObjectKey{
		Namespace: c.namespace,
		Name:      name,
	}, rule)
	if err != nil {
		return nil, err
	}
	return rule, nil
}

// UpdatePrometheusRule updates an existing PrometheusRule.
func (c *Client) UpdatePrometheusRule(ctx context.Context, rule *monitoringv1.PrometheusRule) error {
	if err := c.client.Update(ctx, rule); err != nil {
		return fmt.Errorf("failed to update PrometheusRule: %w", err)
	}
	return nil
}

// DeletePrometheusRule deletes a PrometheusRule by name from the configured namespace.
func (c *Client) DeletePrometheusRule(ctx context.Context, name string) error {
	rule, err := c.GetPrometheusRule(ctx, name)
	if err != nil {
		return err
	}
	if err := c.client.Delete(ctx, rule); err != nil {
		return fmt.Errorf("failed to delete PrometheusRule: %w", err)
	}
	return nil
}

// PrometheusRuleExists checks whether a PrometheusRule with the given name exists.
func (c *Client) PrometheusRuleExists(ctx context.Context, name string) (bool, error) {
	_, err := c.GetPrometheusRule(ctx, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Namespace returns the namespace this client operates in.
func (c *Client) Namespace() string {
	return c.namespace
}
