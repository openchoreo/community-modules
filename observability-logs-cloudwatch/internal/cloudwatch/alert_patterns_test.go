package cloudwatch

import (
	"strings"
	"testing"
)

func TestBuildAlertFilterPattern(t *testing.T) {
	params := LogAlertParams{
		Namespace:      "payments",
		ProjectUID:     "proj-1",
		EnvironmentUID: "env-1",
		ComponentUID:   "comp-1",
		SearchPattern:  "ERROR",
	}

	pattern, err := BuildAlertFilterPattern(params)
	if err != nil {
		t.Fatalf("BuildAlertFilterPattern() error = %v", err)
	}

	for _, expected := range []string{
		`($.kubernetes.labels.* = "env-1")`,
		`($.kubernetes.labels.* = "comp-1")`,
		`($.log = "*ERROR*")`,
		"&&",
	} {
		if !strings.Contains(pattern, expected) {
			t.Fatalf("expected pattern to contain %q, got %q", expected, pattern)
		}
	}
	for _, unexpected := range []string{
		"openchoreo.dev/namespace",
		"openchoreo.dev/project-uid",
	} {
		if strings.Contains(pattern, unexpected) {
			t.Fatalf("expected pattern NOT to contain %q, got %q", unexpected, pattern)
		}
	}
}

func TestBuildAlertFilterPatternEscapesScopeValues(t *testing.T) {
	params := LogAlertParams{
		Namespace:     "payments",
		ComponentUID:  `comp"alpha\prod`,
		SearchPattern: `"payment failed"`,
	}

	pattern, err := BuildAlertFilterPattern(params)
	if err != nil {
		t.Fatalf("BuildAlertFilterPattern() error = %v", err)
	}
	if !strings.Contains(pattern, `comp\"alpha\\prod`) {
		t.Fatalf("expected escaped componentUID in pattern, got %q", pattern)
	}
}

func TestNormaliseUserFilterFragmentVariants(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "token", input: "ERROR", want: `($.log = "*ERROR*")`},
		{name: "quoted phrase", input: `"payment failed"`, want: `($.log = "*payment failed*")`},
		{name: "json equality", input: `$.log = "timeout"`, want: `$.log = "timeout"`},
		{name: "regex", input: `%timeout.*%`, want: `($.log = "%timeout.*%")`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normaliseUserFilterFragment(test.input)
			if err != nil {
				t.Fatalf("normaliseUserFilterFragment() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("normaliseUserFilterFragment() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormaliseUserFilterFragmentRejectsUnsupportedSyntax(t *testing.T) {
	tests := []string{
		`fields @message | filter @message like /ERROR/`,
		`"unterminated`,
		"two words",
		"%foo(bar)%",
		"line1\nline2",
		`$.log = timeout`,
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := normaliseUserFilterFragment(input); err == nil {
				t.Fatalf("expected error for %q", input)
			}
		})
	}
}

func TestBuildAlertFilterPatternRejectsOversizedPattern(t *testing.T) {
	params := LogAlertParams{
		Namespace:     "payments",
		SearchPattern: strings.Repeat("A", MaxFilterPatternLen),
	}

	if _, err := BuildAlertFilterPattern(params); err == nil {
		t.Fatal("expected oversized pattern error")
	}
}
