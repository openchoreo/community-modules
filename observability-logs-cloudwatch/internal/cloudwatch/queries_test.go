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
