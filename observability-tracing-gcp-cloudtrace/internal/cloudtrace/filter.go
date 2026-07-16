// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"regexp"
	"strings"
)

// The Cloud Trace v1 ListTraces filter is a whitespace-separated list of
// terms; `+KEY:VALUE` is an exact, case-sensitive match on a span label. A
// trace matches when any of its spans matches every term, which is the right
// granularity here: the collector stamps the OpenChoreo labels on every span
// it exports, so scoping on "any span" and "all spans" coincide for in-mesh
// spans. See https://cloud.google.com/trace/docs/trace-filters.

// idPattern matches W3C trace/span IDs as hex strings. Inputs that fail this
// never reach a query.
var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{1,64}$`)

func ValidID(s string) bool {
	return idPattern.MatchString(s)
}

// BuildScopeFilter renders the tenancy filter terms. Namespace is always
// emitted; the handler guarantees it is non-empty.
func BuildScopeFilter(p TracesParams) string {
	terms := make([]string, 0, 4)
	terms = appendLabelTerm(terms, LabelNamespace, p.Namespace)
	terms = appendLabelTerm(terms, LabelComponentUID, normalizeUID(p.ComponentUID))
	terms = appendLabelTerm(terms, LabelProjectUID, normalizeUID(p.ProjectUID))
	terms = appendLabelTerm(terms, LabelEnvironmentUID, normalizeUID(p.EnvironmentUID))
	return strings.Join(terms, " ")
}

func appendLabelTerm(terms []string, key, value string) []string {
	value = sanitizeFilterValue(value)
	if value == "" {
		return terms
	}
	return append(terms, "+"+key+":"+value)
}

// filterValuePattern is the set of characters allowed in a filter value.
// Kubernetes label values (the only values that reach this) are limited to
// alphanumerics, '-', '_' and '.'; everything else is stripped so a value
// can never introduce new filter terms (whitespace) or alter term semantics.
var filterValueAllowed = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitizeFilterValue(v string) string {
	return filterValueAllowed.ReplaceAllString(strings.TrimSpace(v), "")
}

// matchesScope reports whether the labels of any span satisfy the scope, for
// endpoints that fetch a trace by ID and cannot pass a filter expression.
// The same any-span semantics as ListTraces keep the two paths consistent.
func matchesScope(labels map[string]string, p TracesParams) bool {
	if labels[LabelNamespace] != p.Namespace {
		return false
	}
	if uid := normalizeUID(p.ComponentUID); uid != "" && labels[LabelComponentUID] != uid {
		return false
	}
	if uid := normalizeUID(p.ProjectUID); uid != "" && labels[LabelProjectUID] != uid {
		return false
	}
	if uid := normalizeUID(p.EnvironmentUID); uid != "" && labels[LabelEnvironmentUID] != uid {
		return false
	}
	return true
}
