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
	// ReleaseLineUID narrows the query to one release line of the component.
	ReleaseLineUID string
	// AllReleaseLines covers every line of the component instead of its default line.
	AllReleaseLines bool
	Start           time.Time
	End             time.Time
	// Step is a normalized OpenCost step (e.g. "1d"). Empty means accumulate
	// the whole window into a single set.
	Step string
}

// BuildFilter renders an OpenCost v2 filter expression. This deployment ignores
// the legacy filterNamespaces/filterLabels params, so the unified filter
// language is used: label lookups are label[key]:"value", clauses are
// AND-joined with " + " and OR-joined with " | ".
//
// The component UID alone keeps the default line's pods, which carry no release
// line label. A release line's pods carry the component UID under another label
// than the default line's, so allLines matches either and covers every line. A
// release line matches on its UID alone.
func BuildFilter(namespace, envUID, projectUID, componentUID, releaseLineUID string, allLines bool) string {
	clauses := []string{
		labelClause(LabelNamespace, namespace),
		labelClause(LabelEnvironmentUID, envUID),
	}
	if projectUID != "" {
		clauses = append(clauses, labelClause(LabelProjectUID, projectUID))
	}
	switch {
	case releaseLineUID != "":
		clauses = append(clauses, labelClause(LabelReleaseLineUID, releaseLineUID))
	case componentUID != "" && allLines:
		clauses = append(clauses, "("+labelClause(LabelComponentUID, componentUID)+" | "+
			labelClause(LabelReleaseLineComponentUID, componentUID)+")")
	case componentUID != "":
		clauses = append(clauses, labelClause(LabelComponentUID, componentUID), labelClause(LabelReleaseLineUID, ""))
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
