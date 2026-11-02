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
//
// A release line matches on its UID alone, since its spans don't carry the
// component UID. Without one, the component UID matches its main line. Filter
// terms cannot be negated, so the main line cannot also exclude traces by a
// release line label: it relies on release line spans not carrying the
// component UID label. AllReleaseLines is left to BuildScopeFilters.
func BuildScopeFilter(p TracesParams) (string, error) {
	if uid := normalizeUID(p.ReleaseLineUID); uid != "" {
		return buildScopeFilter(p, LabelReleaseLineUID, uid)
	}
	return buildScopeFilter(p, LabelComponentUID, normalizeUID(p.ComponentUID))
}

// BuildScopeFilters renders the filters whose traces together make up the
// scope. Filter terms cannot be ORed, and a release line's spans carry the
// component UID under another label than the main line's, so every line of a
// component takes one filter per label. Any other scope takes
// BuildScopeFilter's single filter.
func BuildScopeFilters(p TracesParams) ([]string, error) {
	componentUID := normalizeUID(p.ComponentUID)
	if !p.AllReleaseLines || componentUID == "" || normalizeUID(p.ReleaseLineUID) != "" {
		filter, err := BuildScopeFilter(p)
		if err != nil {
			return nil, err
		}
		return []string{filter}, nil
	}
	filters := make([]string, 0, 2)
	for _, key := range []string{LabelComponentUID, LabelReleaseLineComponentUID} {
		filter, err := buildScopeFilter(p, key, componentUID)
		if err != nil {
			return nil, err
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

// buildScopeFilter renders the scope with lineKey:lineValue as its component
// or release line term.
func buildScopeFilter(p TracesParams, lineKey, lineValue string) (string, error) {
	terms := make([]string, 0, 4)
	var err error
	if terms, err = appendLabelTerm(terms, LabelNamespace, p.Namespace); err != nil {
		return "", err
	}
	if terms, err = appendLabelTerm(terms, lineKey, lineValue); err != nil {
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
	if !matchesLineScope(labels, p) {
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

// matchesLineScope places a span on the scope's component lines. A release
// line matches on its UID alone, and the component UID alone keeps its main
// line's spans, those without a release line UID. A release line's spans carry
// the component UID under another label than the main line's, so every line
// matches either label.
func matchesLineScope(labels map[string]string, p TracesParams) bool {
	componentUID, lineUID := normalizeUID(p.ComponentUID), normalizeUID(p.ReleaseLineUID)
	switch {
	case lineUID != "":
		return labels[LabelReleaseLineUID] == lineUID
	case componentUID == "":
		return true
	case p.AllReleaseLines:
		return labels[LabelComponentUID] == componentUID || labels[LabelReleaseLineComponentUID] == componentUID
	}
	_, onReleaseLine := labels[LabelReleaseLineUID]
	return labels[LabelComponentUID] == componentUID && !onReleaseLine
}

// TracesReleaseLineApplied reports whether QueryTraces matches the scope by
// release line: one release line, or every line of a component. A component's
// main line alone is not, since its filter cannot exclude release line traces.
func TracesReleaseLineApplied(p TracesParams) bool {
	if normalizeUID(p.ReleaseLineUID) != "" {
		return true
	}
	return p.AllReleaseLines && normalizeUID(p.ComponentUID) != ""
}

// spanInView reports whether QuerySpans returns a span of a trace in scope.
// Other components' spans are kept, and the scope's component keeps only the
// spans of the requested line. A release line without a component keeps its
// own spans alone.
func spanInView(labels map[string]string, p TracesParams) bool {
	componentUID := normalizeUID(p.ComponentUID)
	if componentUID == "" {
		lineUID := normalizeUID(p.ReleaseLineUID)
		return lineUID == "" || labels[LabelReleaseLineUID] == lineUID
	}
	if labels[LabelComponentUID] != componentUID && labels[LabelReleaseLineComponentUID] != componentUID {
		return true
	}
	return matchesLineScope(labels, p)
}

// SpansReleaseLineApplied reports whether QuerySpans matches the scope by
// release line, which it does for any component or release line since it
// checks each span's labels itself.
func SpansReleaseLineApplied(p TracesParams) bool {
	return normalizeUID(p.ReleaseLineUID) != "" || normalizeUID(p.ComponentUID) != ""
}
