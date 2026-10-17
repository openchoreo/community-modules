// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExtractLogLevel(t *testing.T) {
	for in, want := range map[string]string{
		"INFO: retrying after ERROR": "ERROR",
		"a warning happened":         "WARN",
		"debug details":              "DEBUG",
		"Fatal crash":                "FATAL",
		"nothing to see":             "INFO",
		"":                           "INFO",
	} {
		if got := ExtractLogLevel(in); got != want {
			t.Errorf("ExtractLogLevel(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestLevelConditionPrecedence(t *testing.T) {
	warn := levelCondition("warn")
	for _, want := range []string{
		`contains(` + "`content`" + `, "warn", caseSensitive: false)`,
		`not contains(` + "`content`" + `, "error", caseSensitive: false)`,
		`not contains(` + "`content`" + `, "fatal", caseSensitive: false)`,
		`not contains(` + "`content`" + `, "severe", caseSensitive: false)`,
	} {
		if !strings.Contains(warn, want) {
			t.Errorf("WARN condition missing %q:\n%s", want, warn)
		}
	}
	if strings.Contains(warn, `"info"`) {
		t.Errorf("WARN must not exclude lower levels:\n%s", warn)
	}

	info := levelCondition("INFO")
	if strings.Count(info, " or ") != 1 || !strings.Contains(info, `not contains(`+"`content`"+`, "debug", caseSensitive: false)`) {
		t.Errorf("INFO must also select unmarked messages:\n%s", info)
	}

	if levelCondition("") != "" || levelCondition("TRACE") != "" {
		t.Error("unknown or empty levels should not filter")
	}
}

func TestLevelsCondition(t *testing.T) {
	if levelsCondition(nil) != "" {
		t.Error("no levels should not filter")
	}
	if c := levelsCondition([]string{"ERROR", "WARN"}); !strings.HasPrefix(c, "((") || !strings.Contains(c, ") or (") {
		t.Errorf("unexpected: %s", c)
	}
	if c := levelsCondition([]string{"ERROR"}); c != levelCondition("ERROR") {
		t.Errorf("single level should not be re-wrapped: %s", c)
	}
}

func TestInt64Field(t *testing.T) {
	r := map[string]any{"s": "12", "f": 3.0, "frac": 1.5, "n": json.Number("7"), "sf": "4.0", "bad": "x", "b": true}
	for key, want := range map[string]int64{"s": 12, "f": 3, "n": 7, "sf": 4} {
		if got, ok := int64Field(r, key); !ok || got != want {
			t.Errorf("int64Field(%s) = %d %v, want %d", key, got, ok, want)
		}
	}
	for _, key := range []string{"frac", "bad", "b", "missing"} {
		if _, ok := int64Field(r, key); ok {
			t.Errorf("int64Field(%s) should fail", key)
		}
	}
}

func TestTimeField(t *testing.T) {
	r := map[string]any{"ok": "2026-01-01T00:00:00.5+01:00", "bad": "yesterday", "num": 1.0}
	if got := timeField(r, "ok"); !got.Equal(time.Date(2025, 12, 31, 23, 0, 0, 5e8, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("unexpected %v", got)
	}
	if !timeField(r, "bad").IsZero() || !timeField(r, "num").IsZero() {
		t.Error("invalid timestamps should be zero")
	}
}

func TestSplitLabels(t *testing.T) {
	got := splitLabels([]string{"a=1", "b=x=y", "=nokey", "novalue"})
	if len(got) != 2 || got["a"] != "1" || got["b"] != "x=y" {
		t.Errorf("unexpected %v", got)
	}
	if splitLabels(nil) != nil || splitLabels([]string{"bad"}) != nil {
		t.Error("expected nil")
	}
}

func TestStringSlice(t *testing.T) {
	got := stringSlice(map[string]any{"a": []any{"x", 1.0, "y"}}, "a")
	if len(got) != 2 {
		t.Errorf("unexpected %v", got)
	}
	if stringSlice(map[string]any{"a": "x"}, "a") != nil {
		t.Error("non-array should be nil")
	}
}

func TestIsValidLabelKey(t *testing.T) {
	for _, k := range []string{"app", "openchoreo.dev/component-uid", "app.kubernetes.io/name", "A_b.c"} {
		if !IsValidLabelKey(k) {
			t.Errorf("%q should be valid", k)
		}
	}
	for _, k := range []string{"", "a=b", "x`y", "-a", "a/", "/a", strings.Repeat("a", 64), strings.Repeat("a", 254) + "/b"} {
		if IsValidLabelKey(k) {
			t.Errorf("%q should be invalid", k)
		}
	}
}

func TestResolveTimelineInterval(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		requested string
		window    time.Duration
		want      string
	}{
		{"15m", time.Hour, "15m"},
		{"", time.Hour, "1m"},
		{"", 24 * time.Hour, "24m"},
		{"1m", 30 * 24 * time.Hour, "1d"},
		{"1m", 1000 * 24 * time.Hour, "1w"},
		{"1m", 4000 * 24 * time.Hour, "2w"},
		{"2h", 3 * time.Hour, "2h"},
	}
	for _, c := range cases {
		got, err := ResolveTimelineInterval(c.requested, start, start.Add(c.window))
		if err != nil || got != c.want {
			t.Errorf("ResolveTimelineInterval(%q, %v) = %q %v, want %q", c.requested, c.window, got, err, c.want)
		}
	}
	for _, bad := range []string{"x", "0m", "5s", "m", "10001m"} {
		if _, err := ResolveTimelineInterval(bad, start, start.Add(time.Hour)); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if _, err := ResolveTimelineInterval("1m", start, start); err == nil {
		t.Error("empty window should be rejected")
	}
}

func TestBuildAuditTimelineRejectsEmptyWindow(t *testing.T) {
	start := time.Now()
	if _, err := buildAuditTimeline("1m", start, start, nil); err == nil {
		t.Error("expected error")
	}
	if _, err := buildAuditTimeline("bad", start, start.Add(time.Hour), nil); err == nil {
		t.Error("expected error")
	}
}
