// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import "strings"

// logLevelMarkers are the substrings a message is scanned for, in precedence order, with
// the level each yields. "warning" is absent because "warn" precedes it and is a prefix of
// it, so it could never be reached.
var logLevelMarkers = []struct{ marker, level string }{
	{"error", "ERROR"},
	{"fatal", "FATAL"},
	{"severe", "SEVERE"},
	{"warn", "WARN"},
	{"info", "INFO"},
	{"debug", "DEBUG"},
}

// defaultLogLevel is the level of a message that carries no marker at all.
const defaultLogLevel = "INFO"

// ExtractLogLevel reads a container log line's severity from its text.
//
// Fluent Bit emits no level for container logs, so a record's severity is only ever what
// its own message says. levelCondition is the query-side twin of this function and the
// two must agree, or a record could be filtered in as INFO and then displayed as ERROR.
func ExtractLogLevel(message string) string {
	lower := strings.ToLower(message)
	for _, m := range logLevelMarkers {
		if strings.Contains(lower, m.marker) {
			return m.level
		}
	}
	return defaultLogLevel
}

// levelCondition matches the records ExtractLogLevel would classify as the given level.
//
// Classification is first-match-wins down logLevelMarkers, so a level is selected by its
// own marker being present and every higher-precedence marker being absent: a line reading
// "INFO: retrying after ERROR" is an ERROR, and an INFO filter must not return it. Messages
// with no marker at all fall back to INFO, so the INFO filter selects those too.
func levelCondition(level string) string {
	level = strings.ToUpper(strings.TrimSpace(level))
	if level == "" {
		return ""
	}

	var own []string
	var higher []string
	for _, m := range logLevelMarkers {
		if m.level == level {
			own = append(own, m.marker)
			continue
		}
		if len(own) == 0 {
			higher = append(higher, m.marker)
		}
	}

	var clauses []string
	if len(own) > 0 {
		parts := []string{containsText(fContent, own[0], false)}
		for _, h := range higher {
			parts = append(parts, "not "+containsText(fContent, h, false))
		}
		clauses = append(clauses, "("+strings.Join(parts, " and ")+")")
	}

	if level == defaultLogLevel {
		absent := make([]string, 0, len(logLevelMarkers))
		for _, m := range logLevelMarkers {
			absent = append(absent, "not "+containsText(fContent, m.marker, false))
		}
		clauses = append(clauses, "("+strings.Join(absent, " and ")+")")
	}

	if len(clauses) == 0 {
		return ""
	}
	return "(" + strings.Join(clauses, " or ") + ")"
}

// levelsCondition ORs levelCondition across the requested levels.
func levelsCondition(levels []string) string {
	parts := make([]string, 0, len(levels))
	for _, l := range levels {
		if c := levelCondition(l); c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " or ") + ")"
}
