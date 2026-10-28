// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	executePath = "/platform/storage/query/v1/query:execute"
	pollPath    = "/platform/storage/query/v1/query:poll"

	// Query states reported by the Grail query API.
	stateSucceeded  = "SUCCEEDED"
	stateRunning    = "RUNNING"
	stateNotStarted = "NOT_STARTED"

	// pollWait is how long one poll request asks Grail to hold the connection open while
	// the query runs, so a slow query is waited on server-side rather than busy-polled.
	pollWait = 5 * time.Second

	// maxErrorBody bounds how much of a failed response is carried into an error message.
	maxErrorBody = 2048
)

// Config holds what the client needs to reach a Dynatrace environment.
type Config struct {
	// PlatformURL is the environment's platform API base URL, e.g.
	// https://abc12345.apps.dynatrace.com.
	PlatformURL string

	// ContainerLogsSource is the log.source value Fluent Bit stamps on container logs.
	ContainerLogsSource string
	// AuditLogsSource is the log.source value Fluent Bit stamps on audit records.
	AuditLogsSource string
	// AuditBucket, when set, restricts audit queries to one Grail bucket.
	AuditBucket string

	// QueryTimeout bounds the whole execute-and-poll cycle of one query.
	QueryTimeout time.Duration
}

// Client runs DQL queries against the Grail query API.
type Client struct {
	baseURL     string
	tokens      TokenSource
	httpClient  *http.Client
	logger      *slog.Logger
	timeout     time.Duration
	logsSource  string
	auditSource string
	auditBucket string
}

// Returns an HTTP client that answers a redirect with the 3xx response
// instead of following it.
func newNoRedirectClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// NewClient returns a client for the given environment.
func NewClient(cfg Config, tokens TokenSource, httpClient *http.Client, logger *slog.Logger) *Client {
	if httpClient == nil {
		httpClient = newNoRedirectClient(60 * time.Second)
	}
	timeout := cfg.QueryTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:     strings.TrimRight(cfg.PlatformURL, "/"),
		tokens:      tokens,
		httpClient:  httpClient,
		logger:      logger,
		timeout:     timeout,
		logsSource:  cfg.ContainerLogsSource,
		auditSource: cfg.AuditLogsSource,
		auditBucket: cfg.AuditBucket,
	}
}

// QueryRequest is one DQL query over a time window.
type QueryRequest struct {
	Query string
	// Start and End bound the window as the query's default timeframe, so `fetch` needs no
	// from/to of its own. Start is inclusive and End exclusive.
	Start time.Time
	End   time.Time
	// MaxRecords caps the records Grail returns. Zero leaves Grail's default in place.
	MaxRecords int
}

// QueryResult is the records a query returned.
type QueryResult struct {
	Records []map[string]any
	Took    time.Duration
}

type executeRequest struct {
	Query                      string `json:"query"`
	DefaultTimeframeStart      string `json:"defaultTimeframeStart,omitempty"`
	DefaultTimeframeEnd        string `json:"defaultTimeframeEnd,omitempty"`
	Timezone                   string `json:"timezone"`
	Locale                     string `json:"locale"`
	MaxResultRecords           int    `json:"maxResultRecords,omitempty"`
	RequestTimeoutMilliseconds int64  `json:"requestTimeoutMilliseconds"`
}

type queryResponse struct {
	State        string `json:"state"`
	RequestToken string `json:"requestToken"`
	Result       *struct {
		Records []map[string]any `json:"records"`
	} `json:"result"`
}

type errorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Details struct {
			ErrorMessage string `json:"errorMessage"`
		} `json:"details"`
	} `json:"error"`
}

// Query runs one DQL query to completion and returns its records.
//
// Grail answers either with the result, or with a request token when the query outlives
// the initial request; in that case the token is polled until the query settles or the
// client's query timeout expires.
func (c *Client) Query(ctx context.Context, req QueryRequest) (*QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	started := time.Now()
	c.logger.Debug("Executing DQL query", slog.String("query", req.Query),
		slog.Time("start", req.Start), slog.Time("end", req.End))

	body := executeRequest{
		Query:                      req.Query,
		Timezone:                   "UTC",
		Locale:                     "en_US",
		MaxResultRecords:           req.MaxRecords,
		RequestTimeoutMilliseconds: pollWait.Milliseconds(),
	}
	if !req.Start.IsZero() {
		body.DefaultTimeframeStart = req.Start.UTC().Format(time.RFC3339Nano)
	}
	if !req.End.IsZero() {
		body.DefaultTimeframeEnd = req.End.UTC().Format(time.RFC3339Nano)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query request: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, c.baseURL+executePath, payload)
	if err != nil {
		return nil, err
	}

	for resp.State == stateRunning || resp.State == stateNotStarted {
		if resp.RequestToken == "" {
			return nil, fmt.Errorf("dynatrace query is %s but returned no request token", resp.State)
		}
		pollURL := c.baseURL + pollPath + "?" + url.Values{
			"request-token":                {resp.RequestToken},
			"request-timeout-milliseconds": {fmt.Sprint(pollWait.Milliseconds())},
		}.Encode()
		resp, err = c.do(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, err
		}
	}

	if resp.State != stateSucceeded {
		return nil, fmt.Errorf("dynatrace query finished in state %q", resp.State)
	}

	result := &QueryResult{Records: []map[string]any{}, Took: time.Since(started)}
	if resp.Result != nil && resp.Result.Records != nil {
		result.Records = resp.Result.Records
	}
	return result, nil
}

// Ping runs a minimal logs query so that a missing scope or a bad token fails at startup
// rather than on the first user request.
func (c *Client) Ping(ctx context.Context) error {
	end := time.Now()
	_, err := c.Query(ctx, QueryRequest{
		Query:      "fetch logs | limit 1 | fields timestamp",
		Start:      end.Add(-time.Minute),
		End:        end,
		MaxRecords: 1,
	})
	return err
}

func (c *Client) do(ctx context.Context, method, endpoint string, payload []byte) (*queryResponse, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain dynatrace access token: %w", err)
	}

	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call dynatrace query API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read dynatrace response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, apiError(resp.StatusCode, respBody)
	}

	var parsed queryResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse dynatrace response: %w", err)
	}
	return &parsed, nil
}

// APIError is a non-success answer from the Dynatrace API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("dynatrace API returned status %d: %s", e.StatusCode, e.Message)
}

// IsAPIError reports whether err is an APIError with the given status code.
func IsAPIError(err error, statusCode int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == statusCode
}

func apiError(status int, body []byte) error {
	var env errorEnvelope
	msg := ""
	if json.Unmarshal(body, &env) == nil {
		msg = env.Error.Message
		if env.Error.Details.ErrorMessage != "" {
			msg += ": " + env.Error.Details.ErrorMessage
		}
	}
	if msg == "" {
		msg = string(body)
		if len(msg) > maxErrorBody {
			msg = msg[:maxErrorBody]
		}
	}
	return &APIError{StatusCode: status, Message: strings.TrimSpace(msg)}
}
