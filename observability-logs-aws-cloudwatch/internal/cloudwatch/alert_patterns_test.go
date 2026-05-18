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

func TestNormaliseUserFilterFragmentJSONEqualityVariants(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid escaped backslash", `$.log = "\\path\\to\\file"`, false},
		{"unescaped backslash", `$.log = "raw\zope"`, true},
		{"trailing backslash", `$.log = "ends\\"`, false}, // \\ is a valid escape sequence
		{"unsupported selector char", `$.log!!! = "x"`, true},
		{"missing quotes on rhs", `$.log = unquoted`, true},
		{"newline rejected", "$.log = \"a\nb\"", true},
		{"selector without prefix", `log = "x"`, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := normaliseUserFilterFragment(c.input)
			if c.wantErr && err == nil {
				t.Fatalf("expected error for %q", c.input)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("normaliseUserFilterFragment(%q) error = %v", c.input, err)
			}
		})
	}
}

func TestNormaliseUserFilterFragmentRejectsRegexQuotesAndBackslash(t *testing.T) {
	if _, err := normaliseUserFilterFragment(`%"oops%`); err == nil {
		t.Fatal("expected double-quote in regex to be rejected")
	}
	if _, err := normaliseUserFilterFragment(`%back\slash%`); err == nil {
		t.Fatal("expected backslash in regex to be rejected")
	}
}

func TestBuildAlertFilterPatternHonoursMaxLengthAfterAssembly(t *testing.T) {
	// Build a long but individually-valid pattern that overflows when combined.
	long := strings.Repeat("a", MaxFilterPatternLen+100)
	params := LogAlertParams{
		Namespace:    "payments",
		ComponentUID: long,
	}
	if _, err := BuildAlertFilterPattern(params); err == nil {
		t.Fatal("expected oversize pattern after assembly to fail")
	}
}

func TestBuildAlertFilterPatternRequiresAtLeastOneCondition(t *testing.T) {
	if _, err := BuildAlertFilterPattern(LogAlertParams{Namespace: "payments"}); err == nil {
		t.Fatal("expected error when no scope or query is provided")
	}
}

func TestNormaliseUserFilterFragmentReservedTokens(t *testing.T) {
	for _, raw := range []string{"foo | bar", "fields x", "stats avg", "sort y", "parse z", "filter q", "display d", "back`tick"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := normaliseUserFilterFragment(raw); err == nil {
				t.Fatalf("expected reserved token %q to be rejected", raw)
			}
		})
	}
}

func TestHasUnescapedBackslashTable(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{`no slashes`, false},
		{`safe \\ pair`, false},
		{`safe \" quote`, false},
		{`safe \n newline`, false},
		{`safe \t tab`, false},
		{`safe \r return`, false},
		{`safe \/ slash`, false},
		{`bad \z escape`, true},
		{`trailing \`, true},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			if got := hasUnescapedBackslash(c.input); got != c.want {
				t.Fatalf("hasUnescapedBackslash(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestIsJSONPathChar(t *testing.T) {
	for _, ok := range []rune{'a', 'Z', '0', '.', '_', '-', '[', ']', '"'} {
		if !isJSONPathChar(ok) {
			t.Fatalf("isJSONPathChar(%q) = false, want true", ok)
		}
	}
	for _, bad := range []rune{'!', '@', '#', '$', '*'} {
		if isJSONPathChar(bad) {
			t.Fatalf("isJSONPathChar(%q) = true, want false", bad)
		}
	}
}

func TestValidateUnquotedTokenRejectsSpaces(t *testing.T) {
	if err := validateUnquotedToken("two words"); err == nil {
		t.Fatal("expected spaces to be rejected")
	}
}
