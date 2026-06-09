// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

var ErrNotFound = errors.New("alert rule not found")

type Client struct {
	rules    *armmonitor.ScheduledQueryRulesClient
	groups   *armmonitor.ActionGroupsClient
	cfg      TranslatorConfig
	resGroup string
	logger   *slog.Logger
}

type Config struct {
	SubscriptionID string
	ResourceGroup  string
	Region         string

	WorkspaceResourceID string
	ActionGroupID       string

	DefaultEvaluationFrequency string
	DefaultWindowSize          string
}

func NewClient(cred azcore.TokenCredential, cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.SubscriptionID == "" {
		return nil, errors.New("subscriptionID is required")
	}
	if cfg.ResourceGroup == "" {
		return nil, errors.New("resourceGroup is required")
	}
	rules, err := armmonitor.NewScheduledQueryRulesClient(cfg.SubscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduledQueryRulesClient: %w", err)
	}
	groups, err := armmonitor.NewActionGroupsClient(cfg.SubscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("actionGroupsClient: %w", err)
	}
	return &Client{
		rules:  rules,
		groups: groups,
		cfg: TranslatorConfig{
			Region:                     cfg.Region,
			WorkspaceResourceID:        cfg.WorkspaceResourceID,
			ActionGroupID:              cfg.ActionGroupID,
			DefaultEvaluationFrequency: cfg.DefaultEvaluationFrequency,
			DefaultWindowSize:          cfg.DefaultWindowSize,
		},
		resGroup: cfg.ResourceGroup,
		logger:   logger,
	}, nil
}

func (c *Client) VerifyActionGroup(ctx context.Context) error {
	name, err := parseActionGroupName(c.cfg.ActionGroupID)
	if err != nil {
		return err
	}
	if _, err := c.groups.Get(ctx, c.resGroup, name, nil); err != nil {
		return fmt.Errorf("action group %q in rg %q: %w", name, c.resGroup, err)
	}
	return nil
}

type RuleResult struct {
	BackendID   string // ARM ID of the rule
	LogicalID   string // OpenChoreo (namespace, ruleName) → derived Azure name
	LastSynced  string // RFC3339 timestamp
	DisplayName string
}

func (c *Client) CreateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	res, err := ToScheduledQueryRule(in, c.cfg)
	if err != nil {
		return nil, err
	}
	name := DeriveAzureName(in.Namespace, in.RuleName)

	resp, err := c.rules.CreateOrUpdate(ctx, c.resGroup, name, *res, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateOrUpdate %q: %w", name, err)
	}
	return ruleResultFrom(resp.ScheduledQueryRuleResource, name), nil
}

func (c *Client) GetRule(ctx context.Context, namespace, ruleName string) (*RuleResult, error) {
	name := DeriveAzureName(namespace, ruleName)
	resp, err := c.rules.Get(ctx, c.resGroup, name, nil)
	if err != nil {
		if isNotFoundError(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("Get %q: %w", name, err)
	}
	return ruleResultFrom(resp.ScheduledQueryRuleResource, name), nil
}

func (c *Client) FindRuleByName(ctx context.Context, ruleName string) (*RuleResult, string, error) {
	pager := c.rules.NewListByResourceGroupPager(c.resGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("list rules: %w", err)
		}
		for _, rule := range page.Value {
			if rule == nil || rule.Tags == nil {
				continue
			}
			tagVal, ok := rule.Tags["openchoreo-rule-name"]
			if !ok || tagVal == nil || *tagVal != ruleName {
				continue
			}
			name := ""
			if rule.Name != nil {
				name = *rule.Name
			}
			namespace := ""
			if nsTag, ok := rule.Tags["openchoreo-namespace"]; ok && nsTag != nil {
				namespace = *nsTag
			}
			return ruleResultFrom(*rule, name), namespace, nil
		}
	}
	return nil, "", ErrNotFound
}

func (c *Client) UpdateRule(ctx context.Context, in RuleInput) (*RuleResult, error) {
	return c.CreateRule(ctx, in)
}

func (c *Client) DeleteRule(ctx context.Context, namespace, ruleName string) error {
	name := DeriveAzureName(namespace, ruleName)
	if _, err := c.rules.Delete(ctx, c.resGroup, name, nil); err != nil {
		if isNotFoundError(err) {
			return ErrNotFound
		}
		return fmt.Errorf("Delete %q: %w", name, err)
	}
	return nil
}

func (c *Client) DeleteRuleByAzureName(ctx context.Context, azureName string) error {
	if _, err := c.rules.Delete(ctx, c.resGroup, azureName, nil); err != nil {
		if isNotFoundError(err) {
			return ErrNotFound
		}
		return fmt.Errorf("Delete %q: %w", azureName, err)
	}
	return nil
}

func ruleResultFrom(res armmonitor.ScheduledQueryRuleResource, logicalID string) *RuleResult {
	r := &RuleResult{
		LogicalID:  logicalID,
		LastSynced: NowRFC3339(),
	}
	if res.ID != nil {
		r.BackendID = *res.ID
	}
	if res.Properties != nil && res.Properties.DisplayName != nil {
		r.DisplayName = *res.Properties.DisplayName
	}
	return r
}

// parseActionGroupName extracts the resource name from an ARM ID:
//
//	/subscriptions/<sub>/resourceGroups/<rg>/providers/microsoft.insights/actionGroups/<name>
func parseActionGroupName(armID string) (string, error) {
	if !strings.Contains(strings.ToLower(armID), "/actiongroups/") {
		return "", fmt.Errorf("actionGroupID %q is not a valid Action Group ARM ID", armID)
	}
	// Use Azure's resource-ID parser.
	id, err := arm.ParseResourceID(armID)
	if err != nil {
		return "", fmt.Errorf("parse actionGroupID: %w", err)
	}
	return id.Name, nil
}

func isNotFoundError(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}
