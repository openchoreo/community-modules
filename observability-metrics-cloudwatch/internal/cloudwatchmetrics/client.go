// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package cloudwatchmetrics wraps the AWS SDK v2 CloudWatch and STS clients
// and exposes the metric-query and alarm-CRUD operations the OpenChoreo
// metrics adapter depends on.
package cloudwatchmetrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/smithy-go"
)

// Kubernetes pod label constants (also used to populate EMF dimensions).
const (
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
	LabelNamespace      = "openchoreo.dev/namespace"
)

// EMF dimension names emitted by the ADOT collector. They must stay in sync
// with the metric_declarations block in helm/values.yaml.
const (
	DimensionComponentUID   = "ComponentUID"
	DimensionProjectUID     = "ProjectUID"
	DimensionEnvironmentUID = "EnvironmentUID"
	DimensionNamespace      = "Namespace"
	DimensionInstanceName   = "InstanceName"
)

// Logical metric names the EMF exporter writes (set by metricstransform).
const (
	MetricPodCPUUsage      = "pod_cpu_usage"
	MetricPodMemoryUsage   = "pod_memory_usage"
	MetricPodCPURequest    = "pod_cpu_request"
	MetricPodCPULimit      = "pod_cpu_limit"
	MetricPodMemoryRequest = "pod_memory_request"
	MetricPodMemoryLimit   = "pod_memory_limit"
)

// Default metric namespace.
const DefaultMetricNamespace = "OpenChoreo/Metrics"

// ErrAlertNotFound is returned when the named alarm does not exist.
var ErrAlertNotFound = errors.New("alert not found")

type cloudwatchAPI interface {
	GetMetricData(context.Context, *cloudwatch.GetMetricDataInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
	ListMetrics(context.Context, *cloudwatch.ListMetricsInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListMetricsOutput, error)
	PutMetricAlarm(context.Context, *cloudwatch.PutMetricAlarmInput, ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricAlarmOutput, error)
	DescribeAlarms(context.Context, *cloudwatch.DescribeAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error)
	DeleteAlarms(context.Context, *cloudwatch.DeleteAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DeleteAlarmsOutput, error)
	ListTagsForResource(context.Context, *cloudwatch.ListTagsForResourceInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListTagsForResourceOutput, error)
}

type stsAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Config holds the static configuration for Client.
type Config struct {
	Region                     string
	InstanceName               string
	MetricNamespace            string
	AlarmActionARNs            []string
	OKActionARNs               []string
	InsufficientDataActionARNs []string
}

// Client wraps CloudWatch + STS for the metrics adapter.
type Client struct {
	cw                         cloudwatchAPI
	sts                        stsAPI
	instanceName               string
	metricNamespace            string
	alarmActionARNs            []string
	okActionARNs               []string
	insufficientDataActionARNs []string
	logger                     *slog.Logger
}

func NewClient(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	ns := cfg.MetricNamespace
	if strings.TrimSpace(ns) == "" {
		ns = DefaultMetricNamespace
	}
	return &Client{
		cw:                         cloudwatch.NewFromConfig(awsCfg),
		sts:                        sts.NewFromConfig(awsCfg),
		instanceName:               cfg.InstanceName,
		metricNamespace:            ns,
		alarmActionARNs:            cfg.AlarmActionARNs,
		okActionARNs:               cfg.OKActionARNs,
		insufficientDataActionARNs: cfg.InsufficientDataActionARNs,
		logger:                     logger,
	}, nil
}

// NewClientWithAWS lets tests inject pre-built AWS clients.
func NewClientWithAWS(cw cloudwatchAPI, stsClient stsAPI, cfg Config, logger *slog.Logger) *Client {
	ns := cfg.MetricNamespace
	if strings.TrimSpace(ns) == "" {
		ns = DefaultMetricNamespace
	}
	return &Client{
		cw:                         cw,
		sts:                        stsClient,
		instanceName:               cfg.InstanceName,
		metricNamespace:            ns,
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

// MetricNamespace returns the configured namespace.
func (c *Client) MetricNamespace() string {
	return c.metricNamespace
}

func tagMap(tags []cwtypes.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return m
}

func isAWSNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ResourceNotFoundException", "ResourceNotFound", "NotFound":
			return true
		}
	}
	return false
}
