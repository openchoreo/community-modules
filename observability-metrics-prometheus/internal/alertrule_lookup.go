// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
)

var prometheusRuleResource = schema.GroupResource{Group: "monitoring.coreos.com", Resource: "prometheusrules"}

// findRule returns the PrometheusRule of the alert rule with the given namespace and name, and
// whether it is a legacy PrometheusRule named after the rule alone. A legacy PrometheusRule is
// only returned when it was built from this namespace's rule. Without a namespace, it falls back
// to the first PrometheusRule built from a rule with that name, for observers that predate
// namespaced rule identity. It returns a NotFound error when there is no such PrometheusRule.
func (h *MetricsHandler) findRule(ctx context.Context, namespace, name string) (*monitoringv1.PrometheusRule, bool, error) {
	if namespace == "" {
		return h.findRuleByName(ctx, name)
	}

	rule, err := h.findCurrentRule(ctx, namespace, name)
	if err == nil {
		return rule, false, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	legacy, err := h.findOwnedLegacyRule(ctx, namespace, name)
	if err != nil {
		return nil, false, err
	}
	return legacy, true, nil
}

// findCurrentRule returns the PrometheusRule named after the alert rule's namespace and name. It
// returns a NotFound error when there is none, or when that name is held by a PrometheusRule built
// from another rule, as a legacy PrometheusRule whose rule is named like the generated name is.
func (h *MetricsHandler) findCurrentRule(ctx context.Context, namespace, name string) (*monitoringv1.PrometheusRule, error) {
	objectName := prometheus.ObjectName(namespace, name)
	rule, err := h.k8sClient.GetPrometheusRule(ctx, objectName)
	if err != nil {
		return nil, err
	}
	if !isRuleOf(rule, namespace, name) {
		ownerNamespace, ownerName := prometheus.RuleIdentity(rule)
		h.logger.Warn("PrometheusRule name of an alert rule is held by another alert rule",
			"rule_namespace", namespace, "rule_name", name, "object_name", objectName,
			"holder_rule_namespace", ownerNamespace, "holder_rule_name", ownerName)
		return nil, apierrors.NewNotFound(prometheusRuleResource, objectName)
	}
	return rule, nil
}

// isRuleOf reports whether a PrometheusRule was built from the alert rule with the given namespace
// and name.
func isRuleOf(rule *monitoringv1.PrometheusRule, namespace, name string) bool {
	ns, n := prometheus.RuleIdentity(rule)
	return ns == namespace && n == name
}

// findOwnedLegacyRule returns the legacy PrometheusRule, named after the rule alone, of the alert
// rule with the given namespace and name. It returns a NotFound error when there is none, or when
// the legacy PrometheusRule was built from another namespace's rule.
func (h *MetricsHandler) findOwnedLegacyRule(ctx context.Context, namespace, name string) (*monitoringv1.PrometheusRule, error) {
	legacy, err := h.k8sClient.GetPrometheusRule(ctx, name)
	if err != nil {
		return nil, err
	}
	if !isRuleOf(legacy, namespace, name) {
		return nil, apierrors.NewNotFound(prometheusRuleResource, name)
	}
	return legacy, nil
}

// removeOwnedLegacyRule deletes a legacy PrometheusRule this namespace's rule left next to its
// current one, as an interrupted migration or a module downgrade does. Both would evaluate the rule.
func (h *MetricsHandler) removeOwnedLegacyRule(ctx context.Context, namespace, name string) error {
	if namespace == "" {
		return nil
	}
	legacy, err := h.findOwnedLegacyRule(ctx, namespace, name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := h.k8sClient.DeletePrometheusRule(ctx, legacy.Name); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	h.logger.Info("Leftover legacy PrometheusRule deleted", "rule_namespace", namespace, "rule_name", name,
		"legacy_name", legacy.Name)
	return nil
}

// findRuleByName returns a PrometheusRule built from a rule with the given name, in any namespace.
// A PrometheusRule named like the rule is only taken as its legacy PrometheusRule when it was built
// from a rule with that name, since another rule's current PrometheusRule can carry that name too.
func (h *MetricsHandler) findRuleByName(ctx context.Context, name string) (*monitoringv1.PrometheusRule, bool, error) {
	legacy, err := h.k8sClient.GetPrometheusRule(ctx, name)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	}
	if err == nil {
		if _, n := prometheus.RuleIdentity(legacy); n == name {
			return legacy, true, nil
		}
	}

	rules, err := h.k8sClient.ListPrometheusRules(ctx)
	if err != nil {
		return nil, false, err
	}
	for i := range rules {
		if _, n := prometheus.RuleIdentity(&rules[i]); n == name {
			return &rules[i], false, nil
		}
	}
	return nil, false, apierrors.NewNotFound(prometheusRuleResource, name)
}

// deleteRule deletes the PrometheusRules of the alert rule with the given namespace and name: the
// current one and, with a namespace, a legacy one this namespace owns. It returns the UID of the
// first deleted PrometheusRule and a NotFound error when there was none.
func (h *MetricsHandler) deleteRule(ctx context.Context, namespace, name string) (string, error) {
	rules, err := h.ruleObjects(ctx, namespace, name)
	if err != nil {
		return "", fmt.Errorf("failed to get alert rule: %w", err)
	}
	if len(rules) == 0 {
		return "", apierrors.NewNotFound(prometheusRuleResource, name)
	}

	// Each PrometheusRule is deleted by name, so one whose deletion is still pending, as a finalizer
	// keeps it, cannot stand in for the other.
	for _, rule := range rules {
		if err := h.k8sClient.DeletePrometheusRule(ctx, rule.Name); err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to delete alert rule: %w", err)
		}
	}
	return string(rules[0].UID), nil
}

// ruleObjects returns the PrometheusRules of the alert rule with the given namespace and name: the
// current one first, then a legacy one this namespace owns. Without a namespace, it returns the
// PrometheusRule findRuleByName finds.
func (h *MetricsHandler) ruleObjects(ctx context.Context, namespace, name string) ([]*monitoringv1.PrometheusRule, error) {
	if namespace == "" {
		rule, _, err := h.findRuleByName(ctx, name)
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []*monitoringv1.PrometheusRule{rule}, nil
	}

	var rules []*monitoringv1.PrometheusRule
	current, err := h.findCurrentRule(ctx, namespace, name)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		rules = append(rules, current)
	}
	legacy, err := h.findOwnedLegacyRule(ctx, namespace, name)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		rules = append(rules, legacy)
	}
	return rules, nil
}
