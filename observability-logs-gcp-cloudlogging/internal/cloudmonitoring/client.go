// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package cloudmonitoring implements OpenChoreo log alerting on top of Google
// Cloud: each rule is a log-based counter metric (Logging API) plus a
// metric-threshold alert policy (Monitoring API) wired to a pre-existing
// notification channel and tagged with OpenChoreo user_labels for lookup.
package cloudmonitoring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	logadmin "cloud.google.com/go/logging/logadmin"
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrNotFound signals that the alert policy for a logical rule was not found.
var ErrNotFound = errors.New("alert rule not found")

// Config holds the alerting construction parameters.
type Config struct {
	ProjectID             string
	NotificationChannelID string
}

// Client wraps the Monitoring alert-policy + notification-channel clients and
// the Logging metric client for log-alert CRUD.
type Client struct {
	policies *monitoring.AlertPolicyClient
	channels *monitoring.NotificationChannelClient
	metrics  *logadmin.Client
	cfg      Config
	parent   string // "projects/<id>"
	logger   *slog.Logger
}

// NewClient builds a Client. Credentials resolve via ADC (Workload Identity in
// production). It creates the two Monitoring clients; the caller supplies the
// already-constructed logadmin client (shared with the query path).
func NewClient(ctx context.Context, metricsClient *logadmin.Client, cfg Config, logger *slog.Logger, opts ...option.ClientOption) (*Client, error) {
	if cfg.ProjectID == "" {
		return nil, errors.New("cloudmonitoring: ProjectID is required")
	}
	policies, err := monitoring.NewAlertPolicyClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cloudmonitoring: NewAlertPolicyClient: %w", err)
	}
	channels, err := monitoring.NewNotificationChannelClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cloudmonitoring: NewNotificationChannelClient: %w", err)
	}
	return &Client{
		policies: policies,
		channels: channels,
		metrics:  metricsClient,
		cfg:      cfg,
		parent:   "projects/" + cfg.ProjectID,
		logger:   logger,
	}, nil
}

// Close releases the Monitoring clients.
func (c *Client) Close() error {
	var errs []error
	if c.policies != nil {
		errs = append(errs, c.policies.Close())
	}
	if c.channels != nil {
		errs = append(errs, c.channels.Close())
	}
	return errors.Join(errs...)
}

// VerifyNotificationChannel confirms the configured channel exists and is
// reachable at boot. A no-op when no channel is configured (alerts can still
// be created; they just won't notify anywhere).
func (c *Client) VerifyNotificationChannel(ctx context.Context) error {
	if c.cfg.NotificationChannelID == "" {
		return nil
	}
	_, err := c.channels.GetNotificationChannel(ctx, &monitoringpb.GetNotificationChannelRequest{
		Name: c.cfg.NotificationChannelID,
	})
	if err != nil {
		return fmt.Errorf("cloudmonitoring: verify notification channel %q: %w", c.cfg.NotificationChannelID, err)
	}
	return nil
}

// CreateRule provisions (or replaces) the log-based metric and alert policy
// for a rule. CreateOrUpdate semantics: an existing policy for the same
// logical identity is deleted first so the policy is recreated cleanly.
func (c *Client) CreateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	metricID := deriveResourceName(in.Namespace, in.RuleName)

	// Upsert the log-based counter metric.
	metric := &logadmin.Metric{
		ID:          metricID,
		Description: fmt.Sprintf("OpenChoreo log alert %q (namespace %q)", in.RuleName, in.Namespace),
		Filter:      BuildAlertFilter(in),
	}
	if err := c.metrics.CreateMetric(ctx, metric); err != nil {
		// CreateMetric fails if it already exists; fall back to update.
		if err2 := c.metrics.UpdateMetric(ctx, metric); err2 != nil {
			return nil, fmt.Errorf("cloudmonitoring: upsert log metric: create=%v update=%w", err, err2)
		}
	}

	// Replace any existing policy for this identity (create-or-update).
	if existing, err := c.findPolicy(ctx, in.Namespace, in.RuleName); err == nil {
		if delErr := c.policies.DeleteAlertPolicy(ctx, &monitoringpb.DeleteAlertPolicyRequest{Name: existing.GetName()}); delErr != nil {
			return nil, fmt.Errorf("cloudmonitoring: replace policy: %w", delErr)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	policy, err := buildAlertPolicy(in, c.cfg, metricID)
	if err != nil {
		return nil, err
	}

	// A freshly-created log-based metric's descriptor is not immediately
	// queryable by Cloud Monitoring — CreateAlertPolicy returns NotFound
	// ("Cannot find metric(s)...") until it propagates. Retry with a short
	// backoff within the request's deadline. If it still isn't ready, return
	// the error so the OpenChoreo alert controller retries on its next
	// reconcile (propagation can take minutes; we don't block that long).
	var created *monitoringpb.AlertPolicy
	err = retryOnMetricNotReady(ctx, func() error {
		var e error
		created, e = c.policies.CreateAlertPolicy(ctx, &monitoringpb.CreateAlertPolicyRequest{
			Name:        c.parent,
			AlertPolicy: policy,
		})
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("cloudmonitoring: create alert policy: %w", err)
	}

	return &RuleResult{
		BackendID:  created.GetName(),
		LogicalID:  metricID,
		LastSynced: NowRFC3339(),
	}, nil
}

// retryOnMetricNotReady retries fn while it fails with a NotFound caused by a
// just-created log-based metric descriptor not having propagated yet. Other
// errors (and success) return immediately. Bounded by the context deadline.
func retryOnMetricNotReady(ctx context.Context, fn func() error) error {
	const backoff = 3 * time.Second
	for {
		err := fn()
		if err == nil || !isMetricNotReady(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
	}
}

// isMetricNotReady reports whether err is the transient NotFound Cloud
// Monitoring returns while a new log-based metric's descriptor propagates.
func isMetricNotReady(err error) bool {
	return grpcCode(err) == codes.NotFound &&
		strings.Contains(err.Error(), "Cannot find metric")
}

// UpdateRule is CreateOrUpdate — identical to CreateRule.
func (c *Client) UpdateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	return c.CreateRule(ctx, in)
}

// GetRule finds the policy for a logical (namespace, ruleName) and returns its
// result view. namespace may be empty — then only ruleName is matched.
func (c *Client) FindRuleByName(ctx context.Context, ruleName string) (*RuleResult, string, error) {
	policy, err := c.findPolicyByRuleName(ctx, ruleName)
	if err != nil {
		return nil, "", err
	}
	ns := policy.GetUserLabels()[UserLabelNamespace]
	return &RuleResult{
		BackendID:  policy.GetName(),
		LogicalID:  deriveResourceName(ns, ruleName),
		LastSynced: NowRFC3339(),
	}, ns, nil
}

// DeleteRule removes the alert policy and the log-based metric for a rule.
func (c *Client) DeleteRule(ctx context.Context, namespace, ruleName string) error {
	policy, err := c.findPolicy(ctx, namespace, ruleName)
	if err != nil {
		return err
	}
	if err := c.policies.DeleteAlertPolicy(ctx, &monitoringpb.DeleteAlertPolicyRequest{Name: policy.GetName()}); err != nil {
		return fmt.Errorf("cloudmonitoring: delete alert policy: %w", err)
	}
	// Best-effort delete of the metric; a missing metric is not an error.
	metricID := deriveResourceName(namespace, ruleName)
	if err := c.metrics.DeleteMetric(ctx, metricID); err != nil && grpcCode(err) != codes.NotFound {
		c.logger.Warn("failed to delete log metric", slog.String("metricId", metricID), slog.Any("error", err))
	}
	return nil
}

// DeleteRuleByBackendName deletes a policy directly by its backend resource
// name (used when the handler already resolved it).
func (c *Client) DeleteRuleByBackendName(ctx context.Context, backendName string) error {
	if err := c.policies.DeleteAlertPolicy(ctx, &monitoringpb.DeleteAlertPolicyRequest{Name: backendName}); err != nil {
		if grpcCode(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("cloudmonitoring: delete alert policy: %w", err)
	}
	return nil
}

// findPolicy locates a policy by (namespace, ruleName) user_labels.
func (c *Client) findPolicy(ctx context.Context, namespace, ruleName string) (*monitoringpb.AlertPolicy, error) {
	filter := fmt.Sprintf(`user_labels.%s="%s" AND user_labels.%s="%s"`,
		UserLabelNamespace, sanitizeLabelValue(namespace),
		UserLabelRuleName, sanitizeLabelValue(ruleName))
	return c.firstPolicyMatching(ctx, filter)
}

// findPolicyByRuleName locates a policy by ruleName alone.
func (c *Client) findPolicyByRuleName(ctx context.Context, ruleName string) (*monitoringpb.AlertPolicy, error) {
	filter := fmt.Sprintf(`user_labels.%s="%s"`, UserLabelRuleName, sanitizeLabelValue(ruleName))
	return c.firstPolicyMatching(ctx, filter)
}

func (c *Client) firstPolicyMatching(ctx context.Context, filter string) (*monitoringpb.AlertPolicy, error) {
	it := c.policies.ListAlertPolicies(ctx, &monitoringpb.ListAlertPoliciesRequest{
		Name:   c.parent,
		Filter: filter,
	})
	policy, err := it.Next()
	if errors.Is(err, iterator.Done) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cloudmonitoring: list alert policies: %w", err)
	}
	return policy, nil
}

func grpcCode(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return codes.Unknown
}
