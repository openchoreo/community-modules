// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"fmt"
	"regexp"
	"strings"
)

// traceIDPattern matches a Cloud Trace trace ID: exactly 32 hex chars.
var traceIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

// spanIDPattern matches an OTLP span ID: 1-16 hex chars (the adapter formats
// v1 fixed64 span IDs as up to 16 hex chars).
var spanIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{1,16}$`)

// allZero reports whether s is all zeros, which is not a valid trace or span
// ID even though it is syntactically hex.
func allZero(s string) bool {
	return strings.Trim(s, "0") == ""
}

// ValidTraceID reports whether s is a well-formed, non-zero trace ID.
func ValidTraceID(s string) bool {
	return traceIDPattern.MatchString(s) && !allZero(s)
}

// ValidSpanID reports whether s is a well-formed, non-zero span ID.
func ValidSpanID(s string) bool {
	return spanIDPattern.MatchString(s) && !allZero(s)
}

// BuildScopeFilter renders the tenancy filter as exact-match `+KEY:VALUE`
// span-label terms. Namespace is always emitted; the handler guarantees it
// is non-empty. A scope value containing characters outside the allowed set
// is rejected rather than rewritten, so one scope can never be silently
// turned into another.
func BuildScopeFilter(p TracesParams) (string, error) {
	terms := make([]string, 0, 4)
	var err error
	if terms, err = appendLabelTerm(terms, LabelNamespace, p.Namespace); err != nil {
		return "", err
	}
	if terms, err = appendLabelTerm(terms, LabelComponentUID, normalizeUID(p.ComponentUID)); err != nil {
		return "", err
	}
	if terms, err = appendLabelTerm(terms, LabelProjectUID, normalizeUID(p.ProjectUID)); err != nil {
		return "", err
	}
	if terms, err = appendLabelTerm(terms, LabelEnvironmentUID, normalizeUID(p.EnvironmentUID)); err != nil {
		return "", err
	}
	return strings.Join(terms, " "), nil
}

func appendLabelTerm(terms []string, key, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return terms, nil
	}
	if !validFilterValue(value) {
		return nil, fmt.Errorf("invalid scope value for %q: %q", key, value)
	}
	return append(terms, "+"+key+":"+value), nil
}

// filterValueDisallowed matches any character outside the Kubernetes
// label-value alphabet. Cloud Trace filter terms are whitespace-separated
// with no documented value-escaping, so a value carrying such a character is
// rejected outright.
var filterValueDisallowed = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func validFilterValue(v string) bool {
	return !filterValueDisallowed.MatchString(v)
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
