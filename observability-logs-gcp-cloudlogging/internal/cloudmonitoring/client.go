// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var ErrNotFound = errors.New("alert rule not found")
var ErrAlreadyExists = errors.New("alert rule already exists")

type Config struct {
	ProjectID             string
	NotificationChannelID string
}

type Client struct {
	policies *monitoring.AlertPolicyClient
	channels *monitoring.NotificationChannelClient
	metrics  *logadmin.Client
	cfg      Config
	parent   string // "projects/<id>"
	logger   *slog.Logger
}

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

func (c *Client) CreateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	// Reject a conflict before writing anything.
	if _, err := c.findPolicy(ctx, in.Namespace, in.RuleName); err == nil {
		return nil, ErrAlreadyExists
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	metricID := deriveResourceName(in.Namespace, in.RuleName)
	if err := c.metrics.CreateMetric(ctx, c.metricFor(in, metricID)); err != nil {
		return nil, fmt.Errorf("cloudmonitoring: create log metric: %w", err)
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

// metricFor builds the log-based counter metric for a rule.
func (c *Client) metricFor(in RuleInput, metricID string) *logadmin.Metric {
	return &logadmin.Metric{
		ID:          metricID,
		Description: fmt.Sprintf("OpenChoreo log alert %q (namespace %q)", in.RuleName, in.Namespace),
		Filter:      BuildAlertFilter(in),
	}
}

const metricReadyMaxWait = 30 * time.Second

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

func isMetricNotReady(err error) bool {
	return grpcCode(err) == codes.NotFound &&
		strings.Contains(err.Error(), "Cannot find metric")
}

// UpdateRule updates the rule's metric and policy in place. Returns ErrNotFound
// if no rule with this identity exists (strict PUT semantics). Because the
// metric and policy already exist, the update needs no metric-propagation
// retry and preserves the policy's resource name.
func (c *Client) UpdateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	existing, err := c.findPolicy(ctx, in.Namespace, in.RuleName)
	if err != nil {
		return nil, err // ErrNotFound propagates as a 404 at the handler
	}

	metricID := deriveResourceName(in.Namespace, in.RuleName)
	if err := c.metrics.UpdateMetric(ctx, c.metricFor(in, metricID)); err != nil {
		return nil, fmt.Errorf("cloudmonitoring: update log metric: %w", err)
	}

	policy, err := buildAlertPolicy(in, c.cfg, metricID)
	if err != nil {
		return nil, err
	}
	// Target the existing policy by name and replace the fields the adapter
	// manages; an empty update mask would clear server-managed fields.
	policy.Name = existing.GetName()
	updated, err := c.policies.UpdateAlertPolicy(ctx, &monitoringpb.UpdateAlertPolicyRequest{
		AlertPolicy: policy,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"display_name", "conditions", "enabled", "user_labels", "notification_channels",
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("cloudmonitoring: update alert policy: %w", err)
	}

	return &RuleResult{
		BackendID:  updated.GetName(),
		LogicalID:  metricID,
		LastSynced: NowRFC3339(),
	}, nil
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
