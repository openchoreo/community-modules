// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// realAlertPolicyAPI adapts the generated *monitoring.AlertPolicyClient to the
// narrow alertPolicyAPI interface, draining the List iterator to a slice.
type realAlertPolicyAPI struct {
	c *monitoring.AlertPolicyClient
}

func (r realAlertPolicyAPI) CreateAlertPolicy(ctx context.Context, req *monitoringpb.CreateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	return r.c.CreateAlertPolicy(ctx, req)
}

func (r realAlertPolicyAPI) GetAlertPolicy(ctx context.Context, req *monitoringpb.GetAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	return r.c.GetAlertPolicy(ctx, req)
}

func (r realAlertPolicyAPI) DeleteAlertPolicy(ctx context.Context, req *monitoringpb.DeleteAlertPolicyRequest) error {
	return r.c.DeleteAlertPolicy(ctx, req)
}

func (r realAlertPolicyAPI) UpdateAlertPolicy(ctx context.Context, req *monitoringpb.UpdateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error) {
	return r.c.UpdateAlertPolicy(ctx, req)
}

func (r realAlertPolicyAPI) ListAlertPolicies(ctx context.Context, req *monitoringpb.ListAlertPoliciesRequest) ([]*monitoringpb.AlertPolicy, error) {
	var out []*monitoringpb.AlertPolicy
	it := r.c.ListAlertPolicies(ctx, req)
	for {
		p, err := it.Next()
		if err == iterator.Done {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
}

var (
	ErrRuleNotFound      = errors.New("alert rule not found")
	ErrRuleAlreadyExists = errors.New("alert rule already exists")
)

// alertPolicyAPI is the slice of the Cloud Monitoring AlertPolicy client this
// package depends on. Production uses *monitoring.AlertPolicyClient; tests
// inject a fake.
type alertPolicyAPI interface {
	CreateAlertPolicy(ctx context.Context, req *monitoringpb.CreateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error)
	GetAlertPolicy(ctx context.Context, req *monitoringpb.GetAlertPolicyRequest) (*monitoringpb.AlertPolicy, error)
	DeleteAlertPolicy(ctx context.Context, req *monitoringpb.DeleteAlertPolicyRequest) error
	UpdateAlertPolicy(ctx context.Context, req *monitoringpb.UpdateAlertPolicyRequest) (*monitoringpb.AlertPolicy, error)
	ListAlertPolicies(ctx context.Context, req *monitoringpb.ListAlertPoliciesRequest) ([]*monitoringpb.AlertPolicy, error)
}

// RuleResult is the backend-neutral outcome of a rule mutation, mapped by the
// handler into the AlertingRuleSyncResponse.
type RuleResult struct {
	BackendID  string // full AlertPolicy resource name
	LogicalID  string // OpenChoreo rule name
	LastSynced string // RFC3339 timestamp
}

// AlertClient manages Cloud Monitoring alert policies on behalf of the adapter.
type AlertClient struct {
	projectID  string
	cfg        TranslatorConfig
	timeout    time.Duration
	api        alertPolicyAPI
	policyAPI  *monitoring.AlertPolicyClient         // non-nil in production, for Close
	channelAPI *monitoring.NotificationChannelClient // non-nil in production, for boot verify + Close
	logger     *slog.Logger
}

// NowRFC3339 is a thin wrapper so tests can stub time.
var NowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }

// NewAlertClient constructs an AlertClient backed by the real Cloud Monitoring
// API.
func NewAlertClient(ctx context.Context, cfg TranslatorConfig, timeout time.Duration, logger *slog.Logger) (*AlertClient, error) {
	if cfg.ProjectID == "" {
		return nil, errors.New("projectID is required")
	}
	pc, err := monitoring.NewAlertPolicyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create alert policy client: %w", err)
	}
	cc, err := monitoring.NewNotificationChannelClient(ctx)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("create notification channel client: %w", err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c := &AlertClient{
		projectID:  cfg.ProjectID,
		cfg:        cfg,
		timeout:    timeout,
		policyAPI:  pc,
		channelAPI: cc,
		api:        realAlertPolicyAPI{pc},
		logger:     logger,
	}
	return c, nil
}

// VerifyNotificationChannel confirms the pre-configured notification channel
// exists and is reachable, so a misconfigured channel fails fast at boot
// rather than silently swallowing alerts. A no-op when no channel is set.
func (c *AlertClient) VerifyNotificationChannel(ctx context.Context) error {
	if c.cfg.NotificationChannelID == "" {
		return nil
	}
	if c.channelAPI == nil {
		return nil // fake client in tests
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if _, err := c.channelAPI.GetNotificationChannel(ctx, &monitoringpb.GetNotificationChannelRequest{
		Name: c.cfg.NotificationChannelID,
	}); err != nil {
		return fmt.Errorf("notification channel %q: %w", c.cfg.NotificationChannelID, err)
	}
	return nil
}

// Close releases the underlying gRPC connections.
func (c *AlertClient) Close() error {
	var firstErr error
	if c.policyAPI != nil {
		if err := c.policyAPI.Close(); err != nil {
			firstErr = err
		}
	}
	if c.channelAPI != nil {
		if err := c.channelAPI.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// CreateRule creates a new managed alert policy.
func (c *AlertClient) CreateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	policy, err := ToAlertPolicy(in, c.cfg)
	if err != nil {
		return nil, err
	}

	if existing, err := c.findByHash(ctx, ruleHash(in.Namespace, in.RuleName)); err == nil && existing != nil {
		return nil, ErrRuleAlreadyExists
	} else if err != nil && !errors.Is(err, ErrRuleNotFound) {
		return nil, fmt.Errorf("pre-create lookup: %w", err)
	}

	created, err := c.api.CreateAlertPolicy(ctx, &monitoringpb.CreateAlertPolicyRequest{
		Name:        "projects/" + c.projectID,
		AlertPolicy: policy,
	})
	if err != nil {
		return nil, fmt.Errorf("create alert policy: %w", err)
	}
	return ruleResultFrom(created, in.RuleName), nil
}

// UpdateRule replaces the managed alert policy for an existing rule in place
// (preserving its resource name). It returns ErrRuleNotFound when no policy
// exists for the rule; creating one is CreateRule's job.
func (c *AlertClient) UpdateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	policy, err := ToAlertPolicy(in, c.cfg)
	if err != nil {
		return nil, err
	}

	existing, err := c.findByHash(ctx, ruleHash(in.Namespace, in.RuleName))
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) {
			return nil, ErrRuleNotFound
		}
		return nil, fmt.Errorf("update lookup: %w", err)
	}

	// Update in place: carry the existing resource name onto the new spec.
	policy.Name = existing.GetName()
	updated, err := c.api.UpdateAlertPolicy(ctx, &monitoringpb.UpdateAlertPolicyRequest{
		AlertPolicy: policy,
	})
	if err != nil {
		return nil, fmt.Errorf("update alert policy %q: %w", policy.Name, err)
	}
	return ruleResultFrom(updated, in.RuleName), nil
}

// FindRuleByName resolves a rule to its managed policy and namespace, given
// only the rule name. Because the hash label needs the namespace, this falls
// back to scanning managed policies by the rule-name label.
func (c *AlertClient) FindRuleByName(ctx context.Context, ruleName string) (*RuleResult, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	want := sanitizeLabelValue(ruleName)
	policies, err := c.api.ListAlertPolicies(ctx, &monitoringpb.ListAlertPoliciesRequest{
		Name:   "projects/" + c.projectID,
		Filter: fmt.Sprintf(`%s AND user_labels.%q="%s"`, managedFilter(), policyLabelRuleName, want),
	})
	if err != nil {
		return nil, "", fmt.Errorf("list alert policies: %w", err)
	}
	if len(policies) == 0 {
		return nil, "", ErrRuleNotFound
	}
	p := policies[0]
	ns := p.GetUserLabels()[policyLabelNamespace]
	return ruleResultFrom(p, ruleName), ns, nil
}

// DeleteRule removes the managed policy for a rule identified by name.
func (c *AlertClient) DeleteRule(ctx context.Context, ruleName string) (*RuleResult, error) {
	res, _, err := c.FindRuleByName(ctx, ruleName)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.api.DeleteAlertPolicy(ctx, &monitoringpb.DeleteAlertPolicyRequest{Name: res.BackendID}); err != nil {
		if isNotFound(err) {
			return nil, ErrRuleNotFound
		}
		return nil, fmt.Errorf("delete alert policy %q: %w", res.BackendID, err)
	}
	res.LastSynced = NowRFC3339()
	return res, nil
}

// findByHash returns the single managed policy carrying the given rule hash, or
// ErrRuleNotFound. The hash label uniquely identifies a (namespace, rule name).
func (c *AlertClient) findByHash(ctx context.Context, hash string) (*monitoringpb.AlertPolicy, error) {
	policies, err := c.api.ListAlertPolicies(ctx, &monitoringpb.ListAlertPoliciesRequest{
		Name:   "projects/" + c.projectID,
		Filter: fmt.Sprintf(`%s AND user_labels.%q="%s"`, managedFilter(), policyLabelRuleHash, hash),
	})
	if err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		return nil, ErrRuleNotFound
	}
	return policies[0], nil
}

// managedFilter selects only OpenChoreo-managed policies.
func managedFilter() string {
	return fmt.Sprintf(`user_labels.%q="%s"`, policyLabelManagedBy, policyLabelManagedByValue)
}

func ruleResultFrom(p *monitoringpb.AlertPolicy, ruleName string) *RuleResult {
	return &RuleResult{
		BackendID:  p.GetName(),
		LogicalID:  ruleName,
		LastSynced: NowRFC3339(),
	}
}

func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}
