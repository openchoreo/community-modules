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

// NewClient builds a Client; the caller supplies the shared logadmin client.
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

// VerifyNotificationChannel confirms the configured channel is reachable at
// boot. A no-op when no channel is configured.
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
// for a rule. An existing policy for the same identity is deleted first.
func (c *Client) CreateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	metricID := deriveResourceName(in.Namespace, in.RuleName)

	metric := &logadmin.Metric{
		ID:          metricID,
		Description: fmt.Sprintf("OpenChoreo log alert %q (namespace %q)", in.RuleName, in.Namespace),
		Filter:      BuildAlertFilter(in),
	}
	if err := c.metrics.CreateMetric(ctx, metric); err != nil {
		// Only an already-exists collision warrants an update; any other error
		// (permission denied, invalid argument, quota) is a real failure and
		// must be surfaced — otherwise a following UpdateMetric would fail with
		// NotFound and mask the true cause.
		if grpcCode(err) != codes.AlreadyExists {
			return nil, fmt.Errorf("cloudmonitoring: create log metric: %w", err)
		}
		if err2 := c.metrics.UpdateMetric(ctx, metric); err2 != nil {
			return nil, fmt.Errorf("cloudmonitoring: update existing log metric: %w", err2)
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
	// queryable — CreateAlertPolicy returns NotFound ("Cannot find metric(s)...")
	// until it propagates. Retry with a short backoff, capped so a stalled
	// request cannot pin the goroutine when the caller's context never cancels;
	// on timeout the NotFound surfaces and the alert controller retries.
	retryCtx, cancelRetry := context.WithTimeout(ctx, metricReadyMaxWait)
	defer cancelRetry()
	var created *monitoringpb.AlertPolicy
	err = retryOnMetricNotReady(retryCtx, func() error {
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

// metricReadyMaxWait bounds how long CreateRule retries the alert-policy create
// while a new log-based metric propagates. Kept short so a stalled request
// never pins a goroutine; the OpenChoreo controller re-reconciles beyond this.
const metricReadyMaxWait = 30 * time.Second

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

// FindRuleByName finds the policy by ruleName alone and returns its result
// view plus the namespace recovered from the policy's labels.
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

// findPolicy locates a policy by (namespace, ruleName). Both are known here, so
// it matches on the collision-free rule-id anchor (a SHA of the pair), which is
// also injection-free by construction. Constrained to adapter-owned policies.
func (c *Client) findPolicy(ctx context.Context, namespace, ruleName string) (*monitoringpb.AlertPolicy, error) {
	filter := fmt.Sprintf(`user_labels.%s="%s" AND user_labels.%s="%s"`,
		UserLabelManagedBy, escapeFilterValue(ManagedByValue),
		UserLabelRuleID, escapeFilterValue(deriveResourceName(namespace, ruleName)))
	return c.firstPolicyMatching(ctx, filter)
}

// findPolicyByRuleName locates a policy by ruleName alone (namespace not known
// at the call site). Same escaping and managed-by constraint as findPolicy.
func (c *Client) findPolicyByRuleName(ctx context.Context, ruleName string) (*monitoringpb.AlertPolicy, error) {
	filter := fmt.Sprintf(`user_labels.%s="%s" AND user_labels.%s="%s"`,
		UserLabelManagedBy, escapeFilterValue(ManagedByValue),
		UserLabelRuleName, escapeFilterValue(sanitizeLabelValue(ruleName)))
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
