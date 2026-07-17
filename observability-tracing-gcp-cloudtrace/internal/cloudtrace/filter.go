// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"regexp"
	"strings"
)

// idPattern matches W3C trace/span IDs as hex strings. Inputs that fail this
// never reach a query.
var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{1,64}$`)

func ValidID(s string) bool {
	return idPattern.MatchString(s)
}

// BuildScopeFilter renders the tenancy filter as exact-match `+KEY:VALUE`
// span-label terms. Namespace is always emitted; the handler guarantees it
// is non-empty.
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

// filterValueAllowed strips everything outside the Kubernetes label-value
// alphabet so a value can never introduce or alter filter terms.
var filterValueAllowed = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitizeFilterValue(v string) string {
	return filterValueAllowed.ReplaceAllString(strings.TrimSpace(v), "")
}

// matchesScope reports whether a span's labels satisfy the scope, for
// endpoints that fetch a trace by ID and cannot pass a filter expression.
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
