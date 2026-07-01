// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AllocationQuery describes a single allocation lookup against OpenCost.
type AllocationQuery struct {
	Namespace      string
	EnvironmentUID string
	ProjectUID     string
	ComponentUID   string
	Start          time.Time
	End            time.Time
	// Step is a normalized OpenCost step (e.g. "1d"). Empty means accumulate
	// the whole window into a single set.
	Step string
}

// BuildFilter renders an OpenCost v2 filter expression. This deployment ignores
// the legacy filterNamespaces/filterLabels params, so the unified filter
// language is used: label lookups are label[key]:"value" and clauses are
// AND-joined with " + ".
func BuildFilter(namespace, envUID, projectUID, componentUID string) string {
	clauses := []string{
		labelClause(LabelNamespace, namespace),
		labelClause(LabelEnvironmentUID, envUID),
	}
	if projectUID != "" {
		clauses = append(clauses, labelClause(LabelProjectUID, projectUID))
	}
	if componentUID != "" {
		clauses = append(clauses, labelClause(LabelComponentUID, componentUID))
	}
	return strings.Join(clauses, " + ")
}

func labelClause(key, value string) string {
	return fmt.Sprintf("label[%s]:%q", key, value)
}

// BuildWindow renders the RFC3339 start,end pair OpenCost expects.
func BuildWindow(start, end time.Time) string {
	return start.UTC().Format(time.RFC3339) + "," + end.UTC().Format(time.RFC3339)
}

// NormalizeGranularity converts the adapter granularity (<count>[hdw]) into an
// OpenCost step. OpenCost has no week unit, so weeks are expanded to days.
func NormalizeGranularity(g string) (string, error) {
	if g == "" {
		return "", nil
	}
	unit := g[len(g)-1]
	count, err := strconv.Atoi(g[:len(g)-1])
	if err != nil || count <= 0 {
		return "", fmt.Errorf("invalid granularity %q", g)
	}
	switch unit {
	case 'h':
		return fmt.Sprintf("%dh", count), nil
	case 'd':
		return fmt.Sprintf("%dd", count), nil
	case 'w':
		return fmt.Sprintf("%dd", count*7), nil
	default:
		return "", fmt.Errorf("invalid granularity unit in %q", g)
	}
}
