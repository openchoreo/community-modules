package cloudwatch

import (
	"strings"
	"testing"
)

func TestBuildComponentQueryIncludesStructuredLogLevelFields(t *testing.T) {
	query := buildComponentQuery(ComponentLogsParams{
		Namespace: "default",
		LogLevels: []string{
			"ERROR",
			"WARN",
		},
	})

	for _, expected := range []string{
		"logLevel as logLevel",
		"level as level",
		"log_processed.logLevel as logProcessedLogLevel",
		"logLevel = \"ERROR\"",
		"log_processed.logLevel = \"ERROR\"",
		"@message like /(?i)ERROR/",
		"logLevel = \"WARN\"",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("expected query to contain %q, got:\n%s", expected, query)
		}
	}
}

func TestBuildWorkflowQueryFiltersStructuredLogLevelFields(t *testing.T) {
	query := buildWorkflowQuery(WorkflowLogsParams{
		Namespace: "default",
		LogLevels: []string{
			"ERROR",
		},
	})

	for _, expected := range []string{
		"logLevel = \"ERROR\"",
		"level = \"ERROR\"",
		"log_processed.logLevel = \"ERROR\"",
		"@message like /(?i)ERROR/",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("expected query to contain %q, got:\n%s", expected, query)
		}
	}
}

func TestNormaliseSortOrder(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"asc", "asc"},
		{"ASC", "asc"},
		{"desc", "desc"},
		{"", "desc"},
		{"junk", "desc"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			if got := normaliseSortOrder(test.input); got != test.want {
				t.Fatalf("normaliseSortOrder(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestInsightsLimit(t *testing.T) {
	if insightsLimit(0) != 1000 {
		t.Fatal("expected default limit 1000 for 0 input")
	}
	if insightsLimit(-1) != 1000 {
		t.Fatal("expected default limit 1000 for negative input")
	}
	if insightsLimit(50) != 50 {
		t.Fatal("expected limit 50 for valid input")
	}
	if insightsLimit(20000) != 10000 {
		t.Fatal("expected limit clamped to 10000 for large input")
	}
}

func TestEscapeInsights(t *testing.T) {
	if got := escapeInsights(`a"b\c`); got != `a\"b\\c` {
		t.Fatalf("escapeInsights() = %q", got)
	}
}

func TestEscapeInsightsRegexQuotesMeta(t *testing.T) {
	if got := escapeInsightsRegex("a.b*c"); got != `a\.b\*c` {
		t.Fatalf("escapeInsightsRegex() = %q", got)
	}
}

func TestAnnotationField(t *testing.T) {
	if got := annotationField("workflows.argoproj.io/node-name"); !strings.Contains(got, "annotations") {
		t.Fatalf("annotationField() = %q", got)
	}
}

func TestBuildComponentQueryMultipleComponents(t *testing.T) {
	q := buildComponentQuery(ComponentLogsParams{
		Namespace:     "default",
		ProjectID:     "p1",
		EnvironmentID: "e1",
		ComponentIDs:  []string{"c1", "c2"},
		SearchPhrase:  "needle",
		Limit:         42,
		SortOrder:     "asc",
	})
	for _, expected := range []string{
		`"c1"`,
		`"c2"`,
		`@message like "needle"`,
		"| sort @timestamp asc",
		"| limit 42",
		`"p1"`,
		`"e1"`,
	} {
		if !strings.Contains(q, expected) {
			t.Fatalf("expected %q in query, got\n%s", expected, q)
		}
	}
}

func TestBuildWorkflowQueryWithSearchPhrase(t *testing.T) {
	q := buildWorkflowQuery(WorkflowLogsParams{
		Namespace:       "default",
		WorkflowRunName: "wf-1",
		SearchPhrase:    "step",
	})
	if !strings.Contains(q, "wf-1") || !strings.Contains(q, "step") {
		t.Fatalf("unexpected query: %s", q)
	}
}

func TestWriteLogLevelFilterIgnoresEmpty(t *testing.T) {
	var b strings.Builder
	writeLogLevelFilter(&b, []string{"", "  "})
	if b.Len() != 0 {
		t.Fatalf("expected empty output for blank levels, got %q", b.String())
	}
}
