package cloudwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudwatch "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type stubLogsAPI struct {
	order                    *[]string
	putMetricFilterInput     *cloudwatchlogs.PutMetricFilterInput
	putMetricFilterErr       error
	describeMetricFiltersOut *cloudwatchlogs.DescribeMetricFiltersOutput
	describeMetricFiltersErr error
	deleteMetricFilterInput  *cloudwatchlogs.DeleteMetricFilterInput
	deleteMetricFilterErr    error
}

func (s *stubLogsAPI) StartQuery(context.Context, *cloudwatchlogs.StartQueryInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error) {
	return nil, errors.New("unexpected StartQuery call")
}

func (s *stubLogsAPI) GetQueryResults(context.Context, *cloudwatchlogs.GetQueryResultsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	return nil, errors.New("unexpected GetQueryResults call")
}

func (s *stubLogsAPI) StopQuery(context.Context, *cloudwatchlogs.StopQueryInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StopQueryOutput, error) {
	return nil, errors.New("unexpected StopQuery call")
}

func (s *stubLogsAPI) PutMetricFilter(_ context.Context, in *cloudwatchlogs.PutMetricFilterInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutMetricFilterOutput, error) {
	s.putMetricFilterInput = in
	if s.order != nil {
		*s.order = append(*s.order, "put-metric-filter")
	}
	return &cloudwatchlogs.PutMetricFilterOutput{}, s.putMetricFilterErr
}

func (s *stubLogsAPI) DescribeMetricFilters(_ context.Context, in *cloudwatchlogs.DescribeMetricFiltersInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeMetricFiltersOutput, error) {
	_ = in
	if s.describeMetricFiltersOut == nil {
		return &cloudwatchlogs.DescribeMetricFiltersOutput{}, s.describeMetricFiltersErr
	}
	return s.describeMetricFiltersOut, s.describeMetricFiltersErr
}

func (s *stubLogsAPI) DeleteMetricFilter(_ context.Context, in *cloudwatchlogs.DeleteMetricFilterInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteMetricFilterOutput, error) {
	s.deleteMetricFilterInput = in
	if s.order != nil {
		*s.order = append(*s.order, "delete-metric-filter")
	}
	return &cloudwatchlogs.DeleteMetricFilterOutput{}, s.deleteMetricFilterErr
}

type stubAlarmsAPI struct {
	order                  *[]string
	describeAlarmsOuts     []*sdkcloudwatch.DescribeAlarmsOutput
	describeAlarmsErr      error
	describeAlarmsCalls    int
	putMetricAlarmInput    *sdkcloudwatch.PutMetricAlarmInput
	putMetricAlarmErr      error
	deleteAlarmsInput      *sdkcloudwatch.DeleteAlarmsInput
	deleteAlarmsErr        error
	listTagsForResourceOut *sdkcloudwatch.ListTagsForResourceOutput
	listTagsForResourceErr error
}

func (s *stubAlarmsAPI) DescribeAlarms(_ context.Context, _ *sdkcloudwatch.DescribeAlarmsInput, _ ...func(*sdkcloudwatch.Options)) (*sdkcloudwatch.DescribeAlarmsOutput, error) {
	s.describeAlarmsCalls++
	if s.describeAlarmsErr != nil {
		return nil, s.describeAlarmsErr
	}
	if len(s.describeAlarmsOuts) == 0 {
		return &sdkcloudwatch.DescribeAlarmsOutput{}, nil
	}
	idx := s.describeAlarmsCalls - 1
	if idx >= len(s.describeAlarmsOuts) {
		idx = len(s.describeAlarmsOuts) - 1
	}
	return s.describeAlarmsOuts[idx], nil
}

func (s *stubAlarmsAPI) PutMetricAlarm(_ context.Context, in *sdkcloudwatch.PutMetricAlarmInput, _ ...func(*sdkcloudwatch.Options)) (*sdkcloudwatch.PutMetricAlarmOutput, error) {
	s.putMetricAlarmInput = in
	if s.order != nil {
		*s.order = append(*s.order, "put-metric-alarm")
	}
	return &sdkcloudwatch.PutMetricAlarmOutput{}, s.putMetricAlarmErr
}

func (s *stubAlarmsAPI) DeleteAlarms(_ context.Context, in *sdkcloudwatch.DeleteAlarmsInput, _ ...func(*sdkcloudwatch.Options)) (*sdkcloudwatch.DeleteAlarmsOutput, error) {
	s.deleteAlarmsInput = in
	if s.order != nil {
		*s.order = append(*s.order, "delete-alarms")
	}
	return &sdkcloudwatch.DeleteAlarmsOutput{}, s.deleteAlarmsErr
}

func (s *stubAlarmsAPI) ListTagsForResource(_ context.Context, _ *sdkcloudwatch.ListTagsForResourceInput, _ ...func(*sdkcloudwatch.Options)) (*sdkcloudwatch.ListTagsForResourceOutput, error) {
	if s.listTagsForResourceOut == nil {
		return &sdkcloudwatch.ListTagsForResourceOutput{}, s.listTagsForResourceErr
	}
	return s.listTagsForResourceOut, s.listTagsForResourceErr
}

type stubSTSAPI struct{}

func (s *stubSTSAPI) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{}, nil
}

func newTestClient(logs logsAPI, alarms alarmsAPI) *Client {
	return NewClientWithAWS(logs, alarms, &stubSTSAPI{}, Config{
		ClusterName:          "test-cluster",
		LogGroupPrefix:       "/aws/containerinsights",
		AlertMetricNamespace: defaultMetricNamespace,
		QueryTimeout:         30 * time.Second,
		PollEvery:            100 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func validAlertParams() LogAlertParams {
	return LogAlertParams{
		Name:           "high-error-rate",
		Namespace:      "payments",
		ProjectUID:     "proj-1",
		EnvironmentUID: "env-1",
		ComponentUID:   "comp-1",
		SearchPattern:  "ERROR",
		Operator:       "gt",
		Threshold:      5,
		Window:         5 * time.Minute,
		Interval:       time.Minute,
		Enabled:        true,
	}
}

func TestCreateAlertCreatesFilterThenAlarm(t *testing.T) {
	order := []string{}
	logs := &stubLogsAPI{order: &order}
	alarms := &stubAlarmsAPI{
		order: &order,
		describeAlarmsOuts: []*sdkcloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmArn: aws.String("arn:aws:cloudwatch:eu-north-1:123456789012:alarm:oc-logs-alert-test"),
			}},
		}},
	}
	client := newTestClient(logs, alarms)

	arn, err := client.CreateAlert(context.Background(), validAlertParams())
	if err != nil {
		t.Fatalf("CreateAlert() error = %v", err)
	}
	if arn == "" {
		t.Fatal("expected non-empty ARN")
	}
	if got, want := order, []string{"put-metric-filter", "put-metric-alarm"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected call order: got %v want %v", got, want)
	}
	if logs.putMetricFilterInput == nil {
		t.Fatal("expected PutMetricFilter input to be captured")
	}
	if alarms.putMetricAlarmInput == nil {
		t.Fatal("expected PutMetricAlarm input to be captured")
	}
}

func TestCreateAlertRollsBackFilterWhenAlarmCreationFails(t *testing.T) {
	order := []string{}
	logs := &stubLogsAPI{order: &order}
	alarms := &stubAlarmsAPI{
		order:             &order,
		putMetricAlarmErr: errors.New("alarm boom"),
	}
	client := newTestClient(logs, alarms)

	_, err := client.CreateAlert(context.Background(), validAlertParams())
	if err == nil {
		t.Fatal("expected CreateAlert() to fail")
	}
	if logs.deleteMetricFilterInput == nil {
		t.Fatal("expected DeleteMetricFilter rollback")
	}
	if got, want := order, []string{"put-metric-filter", "put-metric-alarm", "delete-metric-filter"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("unexpected call order: got %v want %v", got, want)
	}
}

func TestGetAlertReturnsErrAlertNotFoundWhenResourcesMissing(t *testing.T) {
	client := newTestClient(&stubLogsAPI{}, &stubAlarmsAPI{})

	if _, err := client.GetAlert(context.Background(), "payments", "high-error-rate"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("GetAlert() error = %v, want ErrAlertNotFound", err)
	}
}

func TestGetAlertReconstructsDetailFromAlarmAndTags(t *testing.T) {
	names := BuildAlertResourceNames("payments", "high-error-rate")
	logs := &stubLogsAPI{
		describeMetricFiltersOut: &cloudwatchlogs.DescribeMetricFiltersOutput{
			MetricFilters: []cwltypes.MetricFilter{{
				FilterName:    aws.String(names.MetricFilterName),
				FilterPattern: aws.String(`{ ($.kubernetes.labels.* = "env-1") && ($.log = "*ERROR*") }`),
			}},
		},
	}
	alarms := &stubAlarmsAPI{
		describeAlarmsOuts: []*sdkcloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName:          aws.String(names.AlarmName),
				AlarmArn:           aws.String("arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
				Threshold:          aws.Float64(7),
				Period:             aws.Int32(60),
				EvaluationPeriods:  aws.Int32(5),
				ActionsEnabled:     aws.Bool(true),
			}},
		}},
		listTagsForResourceOut: &sdkcloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagRuleName), Value: aws.String("high-error-rate")},
				{Key: aws.String(TagRuleNamespace), Value: aws.String("payments")},
				{Key: aws.String(TagSearchPattern), Value: aws.String("ERROR")},
				{Key: aws.String(TagOperator), Value: aws.String("gt")},
				{Key: aws.String(TagProjectUID), Value: aws.String("proj-1")},
			},
		},
	}
	client := newTestClient(logs, alarms)

	got, err := client.GetAlert(context.Background(), "payments", "high-error-rate")
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if got.Name != "high-error-rate" || got.Namespace != "payments" {
		t.Fatalf("unexpected alert identity: %#v", got)
	}
	if got.SearchPattern != "ERROR" || got.Operator != "gt" {
		t.Fatalf("unexpected alert fields: %#v", got)
	}
	if got.Window != 5*time.Minute || got.Interval != time.Minute {
		t.Fatalf("unexpected durations: %#v", got)
	}
}

func TestGetAlertFindsNamespaceSensitiveAlarmByRuleNameOnly(t *testing.T) {
	names := BuildAlertResourceNames("payments", "high-error-rate")
	logs := &stubLogsAPI{
		describeMetricFiltersOut: &cloudwatchlogs.DescribeMetricFiltersOutput{
			MetricFilters: []cwltypes.MetricFilter{{
				FilterName:    aws.String(names.MetricFilterName),
				FilterPattern: aws.String(`{ ($.kubernetes.labels.* = "env-1") && ($.log = "*ERROR*") }`),
			}},
		},
	}
	alarms := &stubAlarmsAPI{
		describeAlarmsOuts: []*sdkcloudwatch.DescribeAlarmsOutput{
			{
				MetricAlarms: []cwtypes.MetricAlarm{{
					AlarmName:  aws.String(names.AlarmName),
					AlarmArn:   aws.String("arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test"),
					MetricName: aws.String(names.MetricName),
				}},
			},
			{
				MetricAlarms: []cwtypes.MetricAlarm{{
					AlarmName:          aws.String(names.AlarmName),
					AlarmArn:           aws.String("arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test"),
					MetricName:         aws.String(names.MetricName),
					ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
					Threshold:          aws.Float64(7),
					Period:             aws.Int32(60),
					EvaluationPeriods:  aws.Int32(5),
					ActionsEnabled:     aws.Bool(true),
				}},
			},
		},
		listTagsForResourceOut: &sdkcloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagSearchPattern), Value: aws.String("ERROR")},
				{Key: aws.String(TagOperator), Value: aws.String("gt")},
			},
		},
	}
	client := newTestClient(logs, alarms)

	got, err := client.GetAlert(context.Background(), "", "high-error-rate")
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if got.Name != "high-error-rate" || got.Namespace != "payments" {
		t.Fatalf("unexpected alert identity: %#v", got)
	}
}

func TestDeleteAlertReturnsErrAlertNotFoundWhenAlarmMissing(t *testing.T) {
	client := newTestClient(&stubLogsAPI{}, &stubAlarmsAPI{})

	if _, err := client.DeleteAlert(context.Background(), "payments", "high-error-rate"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("DeleteAlert() error = %v, want ErrAlertNotFound", err)
	}
}
