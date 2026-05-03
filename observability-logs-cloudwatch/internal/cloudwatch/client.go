// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package cloudwatch wraps the AWS SDK v2 Logs Insights API and maps
// OpenChoreo Observer log-query parameters onto CloudWatch queries.
package cloudwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type logsAPI interface {
	StartQuery(context.Context, *cloudwatchlogs.StartQueryInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error)
	GetQueryResults(context.Context, *cloudwatchlogs.GetQueryResultsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error)
	StopQuery(context.Context, *cloudwatchlogs.StopQueryInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StopQueryOutput, error)
	PutMetricFilter(context.Context, *cloudwatchlogs.PutMetricFilterInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutMetricFilterOutput, error)
	DescribeMetricFilters(context.Context, *cloudwatchlogs.DescribeMetricFiltersInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeMetricFiltersOutput, error)
	DeleteMetricFilter(context.Context, *cloudwatchlogs.DeleteMetricFilterInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteMetricFilterOutput, error)
}

type alarmsAPI interface {
	DescribeAlarms(context.Context, *cloudwatch.DescribeAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error)
	PutMetricAlarm(context.Context, *cloudwatch.PutMetricAlarmInput, ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricAlarmOutput, error)
	DeleteAlarms(context.Context, *cloudwatch.DeleteAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DeleteAlarmsOutput, error)
	ListTagsForResource(context.Context, *cloudwatch.ListTagsForResourceInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListTagsForResourceOutput, error)
}

type stsAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Client wraps CloudWatch Logs Insights queries scoped to a single EKS/K8s cluster.
type Client struct {
	logs                       logsAPI
	alarms                     alarmsAPI
	sts                        stsAPI
	clusterName                string
	logGroupPrefix             string
	queryTimeout               time.Duration
	pollEvery                  time.Duration
	alertMetricNamespace       string
	alarmActionARNs            []string
	okActionARNs               []string
	insufficientDataActionARNs []string
	logger                     *slog.Logger
}

// Config holds static configuration for the Client.
type Config struct {
	Region                     string
	ClusterName                string
	LogGroupPrefix             string
	QueryTimeout               time.Duration
	PollEvery                  time.Duration
	AlertMetricNamespace       string
	AlarmActionARNs            []string
	OKActionARNs               []string
	InsufficientDataActionARNs []string
}

// NewClient creates a CloudWatch Logs client using the default AWS credentials chain
// (env vars, shared config, EC2 instance-profile, IRSA, Pod Identity).
func NewClient(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Client{
		logs:                       cloudwatchlogs.NewFromConfig(awsCfg),
		alarms:                     cloudwatch.NewFromConfig(awsCfg),
		sts:                        sts.NewFromConfig(awsCfg),
		clusterName:                cfg.ClusterName,
		logGroupPrefix:             strings.TrimRight(cfg.LogGroupPrefix, "/"),
		queryTimeout:               cfg.QueryTimeout,
		pollEvery:                  cfg.PollEvery,
		alertMetricNamespace:       cfg.AlertMetricNamespace,
		alarmActionARNs:            cfg.AlarmActionARNs,
		okActionARNs:               cfg.OKActionARNs,
		insufficientDataActionARNs: cfg.InsufficientDataActionARNs,
		logger:                     logger,
	}, nil
}

// NewClientWithAWS builds a Client from already-constructed AWS clients. Useful for tests.
func NewClientWithAWS(logs logsAPI, alarms alarmsAPI, stsClient stsAPI, cfg Config, logger *slog.Logger) *Client {
	return &Client{
		logs:                       logs,
		alarms:                     alarms,
		sts:                        stsClient,
		clusterName:                cfg.ClusterName,
		logGroupPrefix:             strings.TrimRight(cfg.LogGroupPrefix, "/"),
		queryTimeout:               cfg.QueryTimeout,
		pollEvery:                  cfg.PollEvery,
		alertMetricNamespace:       cfg.AlertMetricNamespace,
		alarmActionARNs:            cfg.AlarmActionARNs,
		okActionARNs:               cfg.OKActionARNs,
		insufficientDataActionARNs: cfg.InsufficientDataActionARNs,
		logger:                     logger,
	}
}

// Ping verifies AWS credentials via sts:GetCallerIdentity.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return err
}

// applicationLogGroup returns the CloudWatch log group where Fluent Bit ships container
// application logs in the Container Insights schema.
func (c *Client) applicationLogGroup() string {
	return fmt.Sprintf("%s/%s/application", c.logGroupPrefix, c.clusterName)
}

// ComponentLogsParams captures the component-scoped query options.
type ComponentLogsParams struct {
	Namespace     string
	ProjectID     string
	EnvironmentID string
	ComponentIDs  []string
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
	SortOrder     string
	SearchPhrase  string
	LogLevels     []string
}

// ComponentLogsEntry is one CloudWatch record normalised for the Observer response.
type ComponentLogsEntry struct {
	Timestamp       time.Time
	Log             string
	LogLevel        string
	Namespace       string
	PodName         string
	PodNamespace    string
	ContainerName   string
	ComponentUID    string
	ComponentName   string
	EnvironmentUID  string
	EnvironmentName string
	ProjectUID      string
	ProjectName     string
}

// ComponentLogsResult wraps the entries and query metadata.
type ComponentLogsResult struct {
	Logs       []ComponentLogsEntry
	TotalCount int
	Took       int
}

// WorkflowLogsParams captures workflow-scoped query options.
type WorkflowLogsParams struct {
	Namespace       string
	WorkflowRunName string
	StartTime       time.Time
	EndTime         time.Time
	Limit           int
	SortOrder       string
	SearchPhrase    string
	LogLevels       []string
}

// WorkflowLogsEntry is one workflow log record.
type WorkflowLogsEntry struct {
	Timestamp time.Time
	Log       string
}

// WorkflowLogsResult wraps workflow entries and metadata.
type WorkflowLogsResult struct {
	Logs       []WorkflowLogsEntry
	TotalCount int
	Took       int
}

// GetComponentLogs runs a Logs Insights query for component-scoped logs.
func (c *Client) GetComponentLogs(ctx context.Context, params ComponentLogsParams) (*ComponentLogsResult, error) {
	started := time.Now()
	query := buildComponentQuery(params)

	c.logger.Debug("CloudWatch Logs Insights component query",
		slog.String("logGroup", c.applicationLogGroup()),
		slog.String("query", query),
	)

	rows, err := c.runQuery(ctx, c.applicationLogGroup(), query, params.StartTime, params.EndTime)
	if err != nil {
		return nil, err
	}

	entries := make([]ComponentLogsEntry, 0, len(rows))
	for _, row := range rows {
		entry := ComponentLogsEntry{
			Log:             row["@message"],
			Namespace:       row["namespace"],
			PodName:         row["podName"],
			PodNamespace:    row["namespace"],
			ContainerName:   row["containerName"],
			ComponentUID:    row["componentUid"],
			ComponentName:   row["componentName"],
			EnvironmentUID:  row["environmentUid"],
			EnvironmentName: row["environmentName"],
			ProjectUID:      row["projectUid"],
			ProjectName:     row["projectName"],
			LogLevel:        logLevelFromRow(row),
		}
		if ts, err := parseInsightsTimestamp(row["@timestamp"]); err == nil {
			entry.Timestamp = ts
		}
		entries = append(entries, entry)
	}

	return &ComponentLogsResult{
		Logs:       entries,
		TotalCount: len(entries),
		Took:       int(time.Since(started).Milliseconds()),
	}, nil
}

// GetWorkflowLogs runs a Logs Insights query for workflow-scoped logs.
func (c *Client) GetWorkflowLogs(ctx context.Context, params WorkflowLogsParams) (*WorkflowLogsResult, error) {
	started := time.Now()
	query := buildWorkflowQuery(params)

	c.logger.Debug("CloudWatch Logs Insights workflow query",
		slog.String("logGroup", c.applicationLogGroup()),
		slog.String("query", query),
	)

	rows, err := c.runQuery(ctx, c.applicationLogGroup(), query, params.StartTime, params.EndTime)
	if err != nil {
		return nil, err
	}

	entries := make([]WorkflowLogsEntry, 0, len(rows))
	for _, row := range rows {
		entry := WorkflowLogsEntry{Log: row["@message"]}
		if ts, err := parseInsightsTimestamp(row["@timestamp"]); err == nil {
			entry.Timestamp = ts
		}
		entries = append(entries, entry)
	}

	return &WorkflowLogsResult{
		Logs:       entries,
		TotalCount: len(entries),
		Took:       int(time.Since(started).Milliseconds()),
	}, nil
}

// runQuery starts a Logs Insights query and polls until it reaches a terminal state.
// Returns one map per result row, keyed by aliased field name.
func (c *Client) runQuery(ctx context.Context, logGroup, query string, startTime, endTime time.Time) ([]map[string]string, error) {
	if startTime.IsZero() || endTime.IsZero() {
		return nil, errors.New("startTime and endTime are required")
	}
	if !endTime.After(startTime) {
		return nil, fmt.Errorf("endTime (%s) must be after startTime (%s)", endTime, startTime)
	}

	startOut, err := c.logs.StartQuery(ctx, &cloudwatchlogs.StartQueryInput{
		LogGroupName: aws.String(logGroup),
		StartTime:    aws.Int64(startTime.Unix()),
		EndTime:      aws.Int64(endTime.Unix()),
		QueryString:  aws.String(query),
	})
	if err != nil {
		return nil, fmt.Errorf("start_query: %w", err)
	}
	queryID := aws.ToString(startOut.QueryId)

	deadline := time.Now().Add(c.queryTimeout)
	for {
		if time.Now().After(deadline) {
			// Best-effort cancel so we stop paying for a query we've abandoned.
			_, _ = c.logs.StopQuery(context.Background(), &cloudwatchlogs.StopQueryInput{QueryId: aws.String(queryID)})
			return nil, fmt.Errorf("query %s timed out after %s", queryID, c.queryTimeout)
		}

		res, err := c.logs.GetQueryResults(ctx, &cloudwatchlogs.GetQueryResultsInput{QueryId: aws.String(queryID)})
		if err != nil {
			return nil, fmt.Errorf("get_query_results: %w", err)
		}

		switch res.Status {
		case cwltypes.QueryStatusComplete:
			return mapQueryRows(res.Results), nil
		case cwltypes.QueryStatusFailed, cwltypes.QueryStatusCancelled, cwltypes.QueryStatusTimeout:
			return nil, fmt.Errorf("query %s ended with status %s", queryID, res.Status)
		case cwltypes.QueryStatusRunning, cwltypes.QueryStatusScheduled, cwltypes.QueryStatusUnknown:
			// keep polling
		}

		select {
		case <-ctx.Done():
			_, _ = c.logs.StopQuery(context.Background(), &cloudwatchlogs.StopQueryInput{QueryId: aws.String(queryID)})
			return nil, ctx.Err()
		case <-time.After(c.pollEvery):
		}
	}
}

func mapQueryRows(results [][]cwltypes.ResultField) []map[string]string {
	out := make([]map[string]string, 0, len(results))
	for _, row := range results {
		m := make(map[string]string, len(row))
		for _, f := range row {
			// CloudWatch returns an internal `@ptr` field for every row; drop it.
			if aws.ToString(f.Field) == "@ptr" {
				continue
			}
			m[aws.ToString(f.Field)] = aws.ToString(f.Value)
		}
		out = append(out, m)
	}
	return out
}

// parseInsightsTimestamp parses the @timestamp format returned by CloudWatch Logs Insights.
// The service formats timestamps as "YYYY-MM-DD HH:MM:SS.sss" in UTC.
func parseInsightsTimestamp(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	layouts := []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp format: %q", v)
}

func logLevelFromRow(row map[string]string) string {
	for _, key := range []string{
		"logLevel",
		"level",
		"severity",
		"severityText",
		"severity_text",
		"logProcessedLogLevel",
		"logProcessedLevel",
		"logProcessedSeverity",
		"logProcessedSeverityText",
		"logProcessedSeverity_text",
	} {
		if value := strings.TrimSpace(row[key]); value != "" {
			return value
		}
	}
	return extractLogLevel(row["@message"])
}

// extractLogLevel matches the OpenObserve adapter's fallback behaviour: prefer
// an explicit structured logLevel field when present, otherwise infer a common
// level token from the log body and default to INFO.
func extractLogLevel(msg string) string {
	upper := strings.ToUpper(msg)
	for _, level := range []string{"ERROR", "FATAL", "SEVERE", "WARN", "WARNING", "INFO", "DEBUG"} {
		if strings.Contains(upper, level) {
			if level == "WARNING" {
				return "WARN"
			}
			return level
		}
	}
	return "INFO"
}
