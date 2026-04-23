// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"errors"
	"fmt"
	"strings"
)

// MaxFilterPatternLen is the CloudWatch Logs hard limit on metric filter pattern length.
const MaxFilterPatternLen = 1024

// messageField is the JSON path to the raw log line as emitted by the upstream
// Fluent Bit `cloudwatch_logs` output. Plain-text user fragments are matched
// against this field so the overall pattern stays valid CloudWatch JSON
// metric-filter syntax (which does not permit bare `"token"` predicates).
const messageField = "$.log"

// BuildAlertFilterPattern composes a JSON metric-filter pattern combining
// OpenChoreo scope labels and the user-provided query fragment.
func BuildAlertFilterPattern(p LogAlertParams) (string, error) {
	userFragment, err := normaliseUserFilterFragment(p.SearchPattern)
	if err != nil {
		return "", err
	}

	scope := buildScopeFragment(p)
	parts := make([]string, 0, 2)
	if scope != "" {
		parts = append(parts, scope)
	}
	if userFragment != "" {
		parts = append(parts, userFragment)
	}
	if len(parts) == 0 {
		return "", errors.New("invalid: no scope or user query available to build filter pattern")
	}

	pattern := "{ " + strings.Join(parts, " && ") + " }"
	if len(pattern) > MaxFilterPatternLen {
		return "", fmt.Errorf("invalid: filter pattern length %d exceeds CloudWatch limit of %d", len(pattern), MaxFilterPatternLen)
	}
	return pattern, nil
}

// buildScopeFragment emits an AND-combined list of JSON selectors scoping the
// alert to a single (environment, component) pair. This matches the
// observability-logs-openobserve adapter, whose generateAlertConfig scopes by
// environment_uid + component_uid only (see queries.go:229). Namespace and
// project_uid are intentionally omitted — they're preserved on the alarm tags
// for round-tripping on GET, but are not part of the match predicate.
//
// CloudWatch stores the Kubernetes labels under keys containing characters
// such as `/` that metric-filter property selectors cannot address directly.
// Match the label values through the labels-object wildcard instead. The
// OpenChoreo environment/component UIDs are UUIDs, so matching by value is
// sufficiently specific for alert scoping.
func buildScopeFragment(p LogAlertParams) string {
	var conds []string
	if p.EnvironmentUID != "" {
		conds = append(conds, jsonEqStringSelector("$.kubernetes.labels.*", p.EnvironmentUID))
	}
	if p.ComponentUID != "" {
		conds = append(conds, jsonEqStringSelector("$.kubernetes.labels.*", p.ComponentUID))
	}
	if len(conds) == 0 {
		return ""
	}
	return strings.Join(conds, " && ")
}

func jsonEqStringSelector(path, value string) string {
	return fmt.Sprintf(`(%s = "%s")`, path, escapeJSONLiteral(value))
}

// escapeJSONLiteral escapes a user value for inclusion in a CloudWatch JSON
// filter pattern double-quoted literal.
func escapeJSONLiteral(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

// logsInsightsReservedTokens is a best-effort list of Logs Insights keywords we
// reject to surface the syntax mismatch early.
var logsInsightsReservedTokens = []string{
	"|", "`", " filter ", " fields ", " stats ", " parse ", " sort ", " display ",
}

// normaliseUserFilterFragment validates and normalises a user-provided
// `source.query` fragment into a CloudWatch metric-filter JSON pattern clause.
func normaliseUserFilterFragment(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}
	if strings.ContainsAny(q, "\n\r") {
		return "", errors.New("invalid: source.query must not contain newline characters")
	}
	if len(q) > MaxFilterPatternLen {
		return "", fmt.Errorf("invalid: source.query length %d exceeds %d", len(q), MaxFilterPatternLen)
	}
	lower := " " + strings.ToLower(q) + " "
	for _, tok := range logsInsightsReservedTokens {
		if strings.Contains(lower, tok) {
			return "", fmt.Errorf("invalid: source.query uses Logs Insights syntax %q — CloudWatch metric filters expect filter-pattern syntax", strings.TrimSpace(tok))
		}
	}
	// Reject unbalanced quotes.
	if strings.Count(q, `"`)%2 != 0 {
		return "", errors.New("invalid: source.query has unbalanced double quotes")
	}

	// Case 1: JSON equality clause, e.g. `$.log = "*timeout*"`.
	if strings.HasPrefix(q, "$.") && strings.Contains(q, "=") {
		return validateJSONEqFragment(q)
	}

	// Case 2: regex wrapped in %…%. CloudWatch JSON filter patterns accept
	// regex only as the right-hand side of a string selector, so wrap the
	// user's regex against the raw message field.
	if strings.HasPrefix(q, "%") && strings.HasSuffix(q, "%") && len(q) >= 2 {
		body := q[1 : len(q)-1]
		if strings.ContainsAny(body, "()") {
			return "", errors.New("invalid: regex filter pattern cannot contain parentheses ( or )")
		}
		if strings.ContainsAny(body, `"\`) {
			return "", errors.New(`invalid: regex filter pattern cannot contain " or \`)
		}
		return fmt.Sprintf(`(%s = "%s")`, messageField, q), nil
	}

	// Case 3: double-quoted exact phrase — match as substring against the
	// raw message field with wildcards on both sides.
	if strings.HasPrefix(q, `"`) && strings.HasSuffix(q, `"`) && len(q) >= 2 {
		inner := q[1 : len(q)-1]
		return fmt.Sprintf(`(%s = "*%s*")`, messageField, escapeJSONLiteral(inner)), nil
	}

	// Case 4: single unquoted token — allow a narrow safe character set and
	// translate to a substring match against the raw message field.
	if err := validateUnquotedToken(q); err != nil {
		return "", err
	}
	return fmt.Sprintf(`(%s = "*%s*")`, messageField, escapeJSONLiteral(q)), nil
}

func validateJSONEqFragment(q string) (string, error) {
	eq := strings.Index(q, "=")
	if eq < 0 {
		return "", errors.New("invalid: JSON-equality fragment requires =")
	}
	left := strings.TrimSpace(q[:eq])
	right := strings.TrimSpace(q[eq+1:])
	if !strings.HasPrefix(left, "$.") {
		return "", errors.New("invalid: JSON selector must start with $.")
	}
	for _, c := range left[2:] {
		if !isJSONPathChar(c) {
			return "", fmt.Errorf("invalid: JSON selector contains unsupported character %q", c)
		}
	}
	// The right-hand side must be a double-quoted literal.
	if !(strings.HasPrefix(right, `"`) && strings.HasSuffix(right, `"`) && len(right) >= 2) {
		return "", errors.New("invalid: right-hand side of JSON-equality fragment must be a double-quoted string")
	}
	// Reject unescaped backslashes outside a known escape sequence.
	inner := right[1 : len(right)-1]
	if hasUnescapedBackslash(inner) {
		return "", errors.New("invalid: JSON string literal contains an unescaped backslash")
	}
	return left + " = " + right, nil
}

func isJSONPathChar(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '.' || c == '_' || c == '-' || c == '[' || c == ']' || c == '"':
		return true
	}
	return false
}

func validateUnquotedToken(q string) error {
	for _, c := range q {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("_-:/;,#=@.", c):
		default:
			return fmt.Errorf("invalid: source.query contains unsupported character %q (quote the phrase or use regex %%…%%)", c)
		}
	}
	return nil
}

func hasUnescapedBackslash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 >= len(s) {
				return true
			}
			next := s[i+1]
			switch next {
			case '\\', '"', 'n', 't', 'r', '/':
				i++
				continue
			default:
				return true
			}
		}
	}
	return false
}
