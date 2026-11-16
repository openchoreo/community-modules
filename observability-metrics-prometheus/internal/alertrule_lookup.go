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

	rule, err := h.k8sClient.GetPrometheusRule(ctx, prometheus.ObjectName(namespace, name))
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

// findOwnedLegacyRule returns the legacy PrometheusRule, named after the rule alone, of the alert
// rule with the given namespace and name. It returns a NotFound error when there is none, or when
// the legacy PrometheusRule was built from another namespace's rule.
func (h *MetricsHandler) findOwnedLegacyRule(ctx context.Context, namespace, name string) (*monitoringv1.PrometheusRule, error) {
	legacy, err := h.k8sClient.GetPrometheusRule(ctx, name)
	if err != nil {
		return nil, err
	}
	if ns, n := prometheus.RuleIdentity(legacy); ns != namespace || n != name {
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
func (h *MetricsHandler) findRuleByName(ctx context.Context, name string) (*monitoringv1.PrometheusRule, bool, error) {
	legacy, err := h.k8sClient.GetPrometheusRule(ctx, name)
	if err == nil {
		return legacy, true, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
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
	maxDeletes := 1
	if namespace != "" {
		maxDeletes = 2
	}

	backendID, deleted := "", false
	for range maxDeletes {
		rule, _, err := h.findRule(ctx, namespace, name)
		if apierrors.IsNotFound(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to get alert rule: %w", err)
		}
		if err := h.k8sClient.DeletePrometheusRule(ctx, rule.Name); err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to delete alert rule: %w", err)
		}
		if !deleted {
			backendID, deleted = string(rule.UID), true
		}
	}

	if !deleted {
		return "", apierrors.NewNotFound(prometheusRuleResource, name)
	}
	return backendID, nil
}
