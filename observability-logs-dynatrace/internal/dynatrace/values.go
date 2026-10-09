// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// stringField returns the string at key, or "" when absent or of another type.
func stringField(record map[string]any, key string) string {
	if v, ok := record[key].(string); ok {
		return v
	}
	return ""
}

// stringSlice returns the strings in an array field, skipping non-string entries.
func stringSlice(record map[string]any, key string) []string {
	raw, ok := record[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// int64Field reads a numeric field. Grail serializes long values as JSON strings, since a
// long does not fit a JSON number losslessly, so both forms are accepted.
func int64Field(record map[string]any, key string) (int64, bool) {
	switch v := record[key].(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n, true
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f != math.Trunc(f) {
			return 0, false
		}
		return int64(f), true
	}
	return 0, false
}

// timeField reads a timestamp, which Grail returns as an RFC 3339 string with up to
// nanosecond precision.
func timeField(record map[string]any, key string) time.Time {
	s, ok := record[key].(string)
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// splitLabels turns the "key=value" label array back into a map. The first "=" splits:
// a label key cannot contain one, while a value may.
func splitLabels(entries []string) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	labels := make(map[string]string, len(entries))
	for _, e := range entries {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			continue
		}
		labels[k] = v
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// labelKeyPattern is the Kubernetes label key grammar: an optional DNS-subdomain prefix
// followed by "/", then a name of alphanumerics with "-", "_" and "." allowed inside.
var labelKeyPattern = regexp.MustCompile(
	`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?` +
		`[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)

// IsValidLabelKey reports whether a key is a well-formed Kubernetes label key.
//
// A label filter is matched as the literal "key=value", so a key holding "=" could shift
// where the value begins and select labels the caller did not name. Keys outside the
// grammar are refused rather than dropped: dropping one would widen the query.
func IsValidLabelKey(key string) bool {
	if key == "" || len(key) > 317 {
		return false
	}
	if !labelKeyPattern.MatchString(key) {
		return false
	}
	name := key
	if slash := strings.IndexByte(key, '/'); slash >= 0 {
		if slash > 253 {
			return false
		}
		name = key[slash+1:]
	}
	return len(name) <= 63
}
