package cloudwatch

import (
	"strings"
	"testing"
	"time"

	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

func TestBuildAlertResourceNamesAreStableAndSafe(t *testing.T) {
	first := BuildAlertResourceNames("payments", "high-error-rate")
	second := BuildAlertResourceNames("payments", "high-error-rate")
	otherNamespace := BuildAlertResourceNames("other", "high-error-rate")

	if first != second {
		t.Fatalf("expected identical inputs to be stable, got %#v vs %#v", first, second)
	}
	if first == otherNamespace {
		t.Fatalf("expected namespace to affect resource names, got %#v vs %#v", first, otherNamespace)
	}
	if !strings.HasPrefix(first.AlarmName, "oc-logs-alert-") {
		t.Fatalf("unexpected alarm name %q", first.AlarmName)
	}
	if !strings.Contains(first.AlarmName, ".rn.") {
		t.Fatalf("expected base64url alarm name format, got %q", first.AlarmName)
	}
	if !strings.HasPrefix(first.MetricName, "oc_logs_alert_") {
		t.Fatalf("unexpected metric name %q", first.MetricName)
	}
	if len(first.AlarmName) > 255 || len(first.MetricName) > 255 {
		t.Fatalf("resource names too long: %#v", first)
	}

	namespace, name, err := ParseAlertIdentityFromAlarmName(first.AlarmName)
	if err != nil {
		t.Fatalf("ParseAlertIdentityFromAlarmName() error = %v", err)
	}
	if namespace != "payments" || name != "high-error-rate" {
		t.Fatalf("unexpected parsed identity: namespace=%q name=%q", namespace, name)
	}
}

func TestMapComparisonOperator(t *testing.T) {
	tests := []struct {
		input string
		want  cwtypes.ComparisonOperator
		ok    bool
	}{
		{input: "gt", want: cwtypes.ComparisonOperatorGreaterThanThreshold, ok: true},
		{input: "gte", want: cwtypes.ComparisonOperatorGreaterThanOrEqualToThreshold, ok: true},
		{input: "lt", want: cwtypes.ComparisonOperatorLessThanThreshold, ok: true},
		{input: "lte", want: cwtypes.ComparisonOperatorLessThanOrEqualToThreshold, ok: true},
		{input: "eq"},
		{input: "neq"},
		{input: "wat"},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := MapComparisonOperator(test.input)
			if test.ok {
				if err != nil {
					t.Fatalf("MapComparisonOperator() error = %v", err)
				}
				if got != test.want {
					t.Fatalf("MapComparisonOperator() = %q, want %q", got, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", test.input)
			}
		})
	}
}

func TestComputePeriodAndEvaluationPeriods(t *testing.T) {
	tests := []struct {
		name      string
		window    time.Duration
		interval  time.Duration
		wantP     int32
		wantEval  int32
		expectErr bool
	}{
		{
			name:     "exact division",
			window:   5 * time.Minute,
			interval: time.Minute,
			wantP:    60,
			wantEval: 5,
		},
		{
			name:     "rounds evaluation periods up",
			window:   5*time.Minute + 30*time.Second,
			interval: 2 * time.Minute,
			wantP:    120,
			wantEval: 3,
		},
		{
			name:      "rejects sub minute interval",
			window:    time.Minute,
			interval:  30 * time.Second,
			expectErr: true,
		},
		{
			name:      "rejects non multiple of minute",
			window:    2 * time.Minute,
			interval:  90 * time.Second,
			expectErr: true,
		},
		{
			name:      "rejects sub hourly alarm over one day",
			window:    25 * time.Hour,
			interval:  30 * time.Minute,
			expectErr: true,
		},
		{
			name:      "rejects hourly alarm over seven days",
			window:    8 * 24 * time.Hour,
			interval:  time.Hour,
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotP, gotEval, err := ComputePeriodAndEvaluationPeriods(test.window, test.interval)
			if test.expectErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ComputePeriodAndEvaluationPeriods() error = %v", err)
			}
			if gotP != test.wantP || gotEval != test.wantEval {
				t.Fatalf("ComputePeriodAndEvaluationPeriods() = (%d, %d), want (%d, %d)", gotP, gotEval, test.wantP, test.wantEval)
			}
		})
	}
}

func TestParseDurationStrict(t *testing.T) {
	if _, err := ParseDurationStrict(""); err == nil {
		t.Fatal("expected empty duration to fail")
	}

	got, err := ParseDurationStrict("90s")
	if err != nil {
		t.Fatalf("ParseDurationStrict() error = %v", err)
	}
	if got != 90*time.Second {
		t.Fatalf("ParseDurationStrict() = %s, want 90s", got)
	}
}

func TestValidateAlertParamsRejectsOverlongGeneratedAlarmName(t *testing.T) {
	params := LogAlertParams{
		Name:      strings.Repeat("b", 100),
		Namespace: strings.Repeat("a", 100),
		Operator:  "gt",
		Window:    time.Minute,
		Interval:  time.Minute,
	}

	err := ValidateAlertParams(params)
	if err == nil {
		t.Fatal("expected overlong generated alarm name to fail validation")
	}
	if !strings.Contains(err.Error(), "generated alarm name length") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReverseMapOperatorAllVariants(t *testing.T) {
	tests := []struct {
		input cwtypes.ComparisonOperator
		want  string
	}{
		{cwtypes.ComparisonOperatorGreaterThanThreshold, "gt"},
		{cwtypes.ComparisonOperatorGreaterThanOrEqualToThreshold, "gte"},
		{cwtypes.ComparisonOperatorLessThanThreshold, "lt"},
		{cwtypes.ComparisonOperatorLessThanOrEqualToThreshold, "lte"},
		{cwtypes.ComparisonOperator("unknown"), ""},
	}
	for _, test := range tests {
		t.Run(string(test.input), func(t *testing.T) {
			if got := ReverseMapOperator(test.input); got != test.want {
				t.Fatalf("ReverseMapOperator(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseAlertIdentityRejectsBadInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing prefix", "foo-bar"},
		{"too few parts", "oc-logs-alert-ns.cGF5bWVudHM"},
		{"wrong shape", "oc-logs-alert-x.y.z.q.r"},
		{"empty hash", "oc-logs-alert-ns.YQ.rn.Yg."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := ParseAlertIdentityFromAlarmName(test.input); err == nil {
				t.Fatalf("expected error for %q", test.input)
			}
		})
	}
}

func TestParseAlertIdentityRejectsBadBase64(t *testing.T) {
	if _, _, err := ParseAlertIdentityFromAlarmName("oc-logs-alert-ns.???.rn.???.deadbeef0000"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestValidateAlertParamsRejectsMissingFields(t *testing.T) {
	cases := []LogAlertParams{
		{Namespace: "ns", Operator: "gt", Window: time.Minute, Interval: time.Minute},
		{Name: "rule", Operator: "gt", Window: time.Minute, Interval: time.Minute},
		{Name: "rule", Namespace: "ns", Operator: "gt"},                                    // missing window/interval
		{Name: "rule", Namespace: "ns", Operator: "gt", Window: 30 * time.Second, Interval: time.Minute}, // window < interval
		{Name: "rule", Namespace: "ns", Operator: "??", Window: time.Minute, Interval: time.Minute},     // bad operator
	}
	for i, p := range cases {
		if err := ValidateAlertParams(p); err == nil {
			t.Fatalf("case %d: expected error for %#v", i, p)
		}
	}
}

func TestValidateAlertParamsAcceptsHappyPath(t *testing.T) {
	if err := ValidateAlertParams(LogAlertParams{
		Name:      "rule",
		Namespace: "ns",
		Operator:  "gt",
		Window:    5 * time.Minute,
		Interval:  time.Minute,
	}); err != nil {
		t.Fatalf("ValidateAlertParams() error = %v", err)
	}
}

func TestParseDurationStrictRejectsInvalid(t *testing.T) {
	if _, err := ParseDurationStrict("???"); err == nil {
		t.Fatal("expected invalid duration to fail")
	}
}

func TestFormatDurationRoundTrips(t *testing.T) {
	got := FormatDuration(90 * time.Second)
	if d, err := ParseDurationStrict(got); err != nil || d != 90*time.Second {
		t.Fatalf("FormatDuration(90s) = %q, parsed = %s err = %v", got, d, err)
	}
}

func TestEncodeDecodeAlertIdentitySegment(t *testing.T) {
	encoded := encodeAlertIdentitySegment("payments")
	decoded, err := decodeAlertIdentitySegment(encoded)
	if err != nil {
		t.Fatalf("decodeAlertIdentitySegment() error = %v", err)
	}
	if decoded != "payments" {
		t.Fatalf("round-trip mismatch: %q", decoded)
	}
	if _, err := decodeAlertIdentitySegment("???"); err == nil {
		t.Fatal("expected bad encoding to fail")
	}
}

func TestComputePeriodAndEvaluationPeriodsCapsHourlyOK(t *testing.T) {
	period, eval, err := ComputePeriodAndEvaluationPeriods(6*time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("ComputePeriodAndEvaluationPeriods() error = %v", err)
	}
	if period != 3600 || eval != 6 {
		t.Fatalf("unexpected period/eval = %d/%d", period, eval)
	}
}

func TestValidateAlertParamsAlsoRejectsOversizedMetricName(t *testing.T) {
	// The metric name format is `oc_logs_alert_<12 hex chars>`, so the 255-char
	// metric name validation is only reachable indirectly via the alarm name path.
	// Use the namespace+name combo that produces an oversized alarm name to
	// confirm the validation surfaces a useful error.
	err := ValidateAlertParams(LogAlertParams{
		Name:      strings.Repeat("a", 200),
		Namespace: strings.Repeat("b", 200),
		Operator:  "gt",
		Window:    time.Minute,
		Interval:  time.Minute,
	})
	if err == nil {
		t.Fatal("expected oversized identifiers to fail")
	}
}
