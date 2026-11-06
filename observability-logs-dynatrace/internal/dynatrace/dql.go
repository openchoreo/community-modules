// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"sort"
	"strings"
)

// dqlStringEscaper escapes the characters that would end or corrupt a DQL string literal.
var dqlStringEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
)

// str renders a DQL string literal. Every value that reaches a query from a request goes
// through here, so a caller cannot close the literal and append DQL of their own.
func str(value string) string {
	return `"` + dqlStringEscaper.Replace(value) + `"`
}

// field renders a field reference. Backticks let a name carry the dots, slashes and dashes
// the schema uses (k8s.namespace.name, k8s.object.label.openchoreo.dev/component-uid).
//
// Field names come from constants or from label keys that have already passed
// IsValidLabelKey, neither of which can contain a backtick; any that did is dropped rather
// than allowed to end the quoted name early.
func field(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "") + "`"
}

// eq matches one field against one value.
func eq(name, value string) string {
	return field(name) + " == " + str(value)
}

// anyOf ORs equality on one field across values. An empty slice is not a filter and yields
// "". Plain equality is used rather than in() so the predicate reads the same on every
// Grail version.
func anyOf(name string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, eq(name, v))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " or ") + ")"
}

// arrayHasAny matches records whose array field holds any of the values.
func arrayHasAny(name string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, "iAny("+field(name)+"[] == "+str(v)+")")
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " or ") + ")"
}

// containsText matches a substring of a string field.
func containsText(name, text string, caseSensitive bool) string {
	cs := "false"
	if caseSensitive {
		cs = "true"
	}
	return "contains(" + field(name) + ", " + str(text) + ", caseSensitive: " + cs + ")"
}

// present matches records that carry a non-empty value in the field.
func present(name string) string {
	return "isNotNull(" + field(name) + ") and " + field(name) + ` != ""`
}

// and joins non-empty conditions.
func and(conditions ...string) string {
	kept := make([]string, 0, len(conditions))
	for _, c := range conditions {
		if c != "" {
			kept = append(kept, c)
		}
	}
	return strings.Join(kept, " and ")
}

// sortedKeys returns a map's keys in order, so the same filters always produce the same
// query text: Go randomises map iteration, and a query that changes between calls cannot
// be compared in a test or matched in a log.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pipeline assembles a DQL query from a fetch, a filter and trailing commands.
type pipeline struct {
	commands []string
}

func fetchLogs() *pipeline {
	return &pipeline{commands: []string{"fetch logs"}}
}

// fetchLogsFrom reads only the named Grail bucket, which Grail can prune at scan time
// rather than filtering every record of every bucket.
func fetchLogsFrom(bucket string) *pipeline {
	if bucket == "" {
		return fetchLogs()
	}
	return &pipeline{commands: []string{"fetch logs, bucket: {" + str(bucket) + "}"}}
}

func (p *pipeline) filter(condition string) *pipeline {
	if condition != "" {
		p.commands = append(p.commands, "filter "+condition)
	}
	return p
}

func (p *pipeline) then(command string) *pipeline {
	p.commands = append(p.commands, command)
	return p
}

func (p *pipeline) String() string {
	return strings.Join(p.commands, "\n| ")
}

// sortDirection whitelists a sort direction; it sits outside any literal, where an
// arbitrary string would be executable.
func sortDirection(order string) string {
	if strings.EqualFold(order, "asc") {
		return "asc"
	}
	return "desc"
}
