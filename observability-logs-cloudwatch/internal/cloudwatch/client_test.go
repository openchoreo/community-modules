package cloudwatch

import "testing"

func TestExtractLogLevelMatchesOpenObserveFallback(t *testing.T) {
	tests := []struct {
		name     string
		log      string
		expected string
	}{
		{name: "error", log: "2025-01-01 ERROR: something failed", expected: "ERROR"},
		{name: "warn", log: "[WARN] potential issue", expected: "WARN"},
		{name: "warning", log: "WARNING: deprecated function", expected: "WARN"},
		{name: "debug", log: "DEBUG: verbose output", expected: "DEBUG"},
		{name: "info", log: "INFO: service started", expected: "INFO"},
		{name: "fatal", log: "FATAL: cannot continue", expected: "FATAL"},
		{name: "severe", log: "SEVERE: critical problem", expected: "SEVERE"},
		{name: "plain", log: "just a regular log message", expected: "INFO"},
		{name: "empty", log: "", expected: "INFO"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractLogLevel(test.log); got != test.expected {
				t.Fatalf("extractLogLevel(%q) = %q, want %q", test.log, got, test.expected)
			}
		})
	}
}

func TestLogLevelFromRowPrefersStructuredFields(t *testing.T) {
	tests := []struct {
		name     string
		row      map[string]string
		expected string
	}{
		{
			name: "top-level logLevel",
			row: map[string]string{
				"@message": "INFO fallback should not win",
				"logLevel": "ERROR",
			},
			expected: "ERROR",
		},
		{
			name: "merged processed logLevel",
			row: map[string]string{
				"@message":             "INFO fallback should not win",
				"logProcessedLogLevel": "WARN",
			},
			expected: "WARN",
		},
		{
			name: "fallback extraction",
			row: map[string]string{
				"@message": "DEBUG fallback",
			},
			expected: "DEBUG",
		},
		{
			name: "default fallback",
			row: map[string]string{
				"@message": "plain message",
			},
			expected: "INFO",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := logLevelFromRow(test.row); got != test.expected {
				t.Fatalf("logLevelFromRow() = %q, want %q", got, test.expected)
			}
		})
	}
}
