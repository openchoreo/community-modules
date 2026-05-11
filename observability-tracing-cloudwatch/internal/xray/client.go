// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package xray

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/xray"
)

type xrayAPI interface {
	GetTraceSummaries(context.Context, *xray.GetTraceSummariesInput, ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error)
	BatchGetTraces(context.Context, *xray.BatchGetTracesInput, ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error)
}

type stsAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

type Scope struct {
	Namespace     string
	ProjectID     string
	ComponentID   string
	EnvironmentID string
}

type TracesQueryParams struct {
	StartTime         time.Time
	EndTime           time.Time
	Limit             int
	SortOrder         string
	Scope             Scope
	TraceID           string
	SpanID            string
	IncludeAttributes bool
}

type TraceEntry struct {
	TraceID      string
	TraceName    string
	SpanCount    int
	RootSpanID   string
	RootSpanName string
	RootSpanKind string
	StartTime    time.Time
	EndTime      time.Time
	DurationNs   int64
	HasErrors    bool
}

type TracesResult struct {
	Traces []TraceEntry
	Total  int
	TookMs int
}

type SpanEntry struct {
	SpanID             string
	SpanName           string
	SpanKind           string
	StartTime          time.Time
	EndTime            time.Time
	DurationNs         int64
	ParentSpanID       string
	Status             string
	Attributes         map[string]interface{}
	ResourceAttributes map[string]interface{}
}

type SpansResult struct {
	Spans  []SpanEntry
	Total  int
	TookMs int
}

type SpanAttribute struct {
	Key   string
	Value string
}

type SpanDetail struct {
	SpanID             string
	SpanName           string
	SpanKind           string
	StartTime          time.Time
	EndTime            time.Time
	DurationNs         int64
	ParentSpanID       string
	Status             string
	Attributes         []SpanAttribute
	ResourceAttributes []SpanAttribute
}

type SpanDetailResult struct {
	Span SpanDetail
}

type Config struct {
	Region string
}

type Client struct {
	xray   xrayAPI
	sts    stsAPI
	logger *slog.Logger
}

func NewClient(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Client{
		xray:   xray.NewFromConfig(awsCfg),
		sts:    sts.NewFromConfig(awsCfg),
		logger: logger,
	}, nil
}

func NewClientWithAWS(xrayClient xrayAPI, stsClient stsAPI, logger *slog.Logger) *Client {
	return &Client{
		xray:   xrayClient,
		sts:    stsClient,
		logger: logger,
	}
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return err
}
