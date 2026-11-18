// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QueryLanguage names what raw queries are written in.
const QueryLanguage = "OpenObserve search request"

var (
	// ErrRawQueryTimeout is returned when a raw query runs past its timeout.
	ErrRawQueryTimeout = errors.New("raw query timed out")
	// ErrRawQueryTooLarge is returned when the result exceeds the maximum response size.
	ErrRawQueryTooLarge = errors.New("raw query result exceeds the maximum response size")
)

// InvalidRawQueryError reports a raw query that can't be run, either because
// it isn't a search body or because OpenObserve rejected it.
type InvalidRawQueryError struct {
	Reason string
}

func (e *InvalidRawQueryError) Error() string {
	return "invalid " + QueryLanguage + ": " + e.Reason
}

// PartialResultError reports a search that OpenObserve answered only in part.
// OpenObserve does that for several reasons, such as a time range longer than
// the stream allows or a node that failed, and says which in function_error.
type PartialResultError struct {
	Reason string
}

func (e *PartialResultError) Error() string {
	return "OpenObserve returned only part of the result: " + e.Reason
}

// allowedTopLevelKeys and allowedQueryKeys are the search body keys a raw
// query may use. Anything else is rejected, so a new OpenObserve feature can't
// reach past the checks here until it has been reviewed. Left out on purpose:
// regions and clusters, which reach other clusters; search_type and
// search_event_context, which change how a search is accounted for;
// local_mode; and agent_options, which reshapes the response.
var allowedTopLevelKeys = map[string]bool{
	"query": true, "timeout": true, "encoding": true, "use_cache": true, "clear_cache": true,
}

// Left out on purpose: query_fn and uses_zo_fn, which run VRL functions;
// action_id, which runs an action; streaming_output and streaming_id, which
// belong to the streaming search API; and sampling_config.
var allowedQueryKeys = map[string]bool{
	"sql": true, "start_time": true, "end_time": true, "from": true, "size": true,
	"quick_mode": true, "query_type": true, "track_total_hits": true, "skip_wal": true,
	"sampling_ratio": true, "histogram_interval": true, "timezone": true,
}

// PrepareRawQuery checks a raw search body and returns the body to send: the
// body as written, with the timeout set to the configured one. The SQL, its
// time range and everything else stay as the caller wrote them.
func PrepareRawQuery(body []byte, timeout time.Duration) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || top == nil {
		return nil, &InvalidRawQueryError{Reason: "the request must be an OpenObserve search body (a JSON object)"}
	}
	if key, ok := firstDisallowedKey(top, allowedTopLevelKeys); ok {
		return nil, invalidRawQuery("%q is not allowed in a raw query", key)
	}

	var query map[string]json.RawMessage
	if raw, ok := top["query"]; !ok || json.Unmarshal(raw, &query) != nil || query == nil {
		return nil, invalidRawQuery("query must be an object with the SQL in query.sql")
	}
	if key, ok := firstDisallowedKey(query, allowedQueryKeys); ok {
		return nil, invalidRawQuery("%q is not allowed in a raw query", "query."+key)
	}
	var sql string
	if json.Unmarshal(query["sql"], &sql) != nil || strings.TrimSpace(sql) == "" {
		return nil, invalidRawQuery("query.sql must be a non-empty SQL string")
	}

	// The timeout is the adapter's: a body can't extend it. OpenObserve takes
	// it in whole seconds.
	top["timeout"] = json.RawMessage(strconv.FormatInt(int64(math.Ceil(timeout.Seconds())), 10))

	return json.Marshal(top)
}

func invalidRawQuery(format string, args ...any) error {
	return &InvalidRawQueryError{Reason: fmt.Sprintf(format, args...)}
}

// firstDisallowedKey returns the first key of obj, in sorted order, that
// allowed doesn't list.
func firstDisallowedKey(obj map[string]json.RawMessage, allowed map[string]bool) (string, bool) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowed[k] {
			return k, true
		}
	}
	return "", false
}

// RawSearch runs a prepared search body through OpenObserve's search API for
// traces and returns OpenObserve's response as is. The organization is the
// adapter's own and the search type is traces, so the query can only read
// trace streams. It reads at most maxResponseBytes of the response.
func (c *Client) RawSearch(ctx context.Context, body []byte, maxResponseBytes int64) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/%s/_search?type=traces", c.baseURL, c.org)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.token)

	// The request context carries the raw query deadline, so the client used
	// for the adapter's own queries, with its fixed timeout, is not used here.
	resp, err := c.rawHTTPClient.Do(req)
	if err != nil {
		return nil, rawSearchFailure(ctx, fmt.Errorf("raw search request failed: %w", err))
	}
	// Closing a body that wasn't read to the end drops the connection, so
	// OpenObserve stops sending the rest of an oversized result.
	defer func() { _ = resp.Body.Close() }()

	// Only a result is held to the maximum response size. An error answer is
	// read up to a cap of its own, so its message is passed on whatever the
	// configured size, and a large error page isn't read in full.
	readLimit := maxResponseBytes + 1
	if resp.StatusCode != http.StatusOK {
		readLimit = maxErrorBodyBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, readLimit))
	if err != nil {
		return nil, rawSearchFailure(ctx, fmt.Errorf("failed to read raw search response: %w", err))
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// The raw query user is missing or its password is out of date: a
		// configuration problem, not a problem with the query.
		return nil, fmt.Errorf("OpenObserve rejected the raw query user's credentials (status %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusRequestTimeout:
		// OpenObserve stopped the search at the timeout the adapter set.
		return nil, ErrRawQueryTimeout
	// OpenObserve answers 400 for a query it can't run and 422 for a body it
	// can't decode, such as one without start_time. Its message is passed on
	// for every other 4xx too, such as a 429 for too many searches, as the
	// OpenSearch module does.
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return nil, &InvalidRawQueryError{Reason: openObserveErrorMessage(data, resp.StatusCode)}
	case resp.StatusCode != http.StatusOK:
		c.logger.Error("OpenObserve returned an error for a raw query",
			slog.Int("statusCode", resp.StatusCode), slog.String("body", string(data)))
		return nil, fmt.Errorf("openobserve returned status %d", resp.StatusCode)
	}
	if int64(len(data)) > maxResponseBytes {
		return nil, ErrRawQueryTooLarge
	}

	var meta struct {
		IsPartial     bool            `json:"is_partial"`
		FunctionError json.RawMessage `json:"function_error"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse raw search response: %w", err)
	}
	if meta.IsPartial {
		// A partial result is never passed on as if it were complete.
		return nil, &PartialResultError{Reason: partialResultReason(meta.FunctionError)}
	}
	return data, nil
}

// rawSearchFailure turns a failed request into ErrRawQueryTimeout when the raw
// query deadline passed, and keeps the context's error in the chain when the
// caller went away, so that can be told apart from a failure.
func rawSearchFailure(ctx context.Context, err error) error {
	// The context is read once, so a deadline that passes in between can't be
	// taken for a failure.
	ctxErr := ctx.Err()
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return ErrRawQueryTimeout
	case ctxErr != nil && !errors.Is(err, ctxErr):
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	return err
}

// partialResultReason joins OpenObserve's function_error, a list of messages,
// or a single one in some versions.
func partialResultReason(raw json.RawMessage) string {
	var messages []string
	if json.Unmarshal(raw, &messages) != nil {
		var message string
		if json.Unmarshal(raw, &message) == nil {
			messages = []string{message}
		}
	}
	reason := strings.TrimSpace(strings.Join(messages, "; "))
	if reason == "" {
		return "OpenObserve didn't say why"
	}
	if len(reason) > maxErrorMessageLength {
		reason = reason[:maxErrorMessageLength] + "…"
	}
	return reason
}

// openObserveErrorMessage extracts the message from an OpenObserve error body,
// which is JSON for most errors and plain text for a body it can't decode.
func openObserveErrorMessage(data []byte, status int) string {
	var e struct {
		Message     string `json:"message"`
		ErrorDetail string `json:"error_detail"`
	}
	if json.Unmarshal(data, &e) == nil && e.Message != "" {
		if e.ErrorDetail != "" && e.ErrorDetail != e.Message {
			return e.Message + ": " + e.ErrorDetail
		}
		return e.Message
	}
	if text := strings.TrimSpace(string(data)); text != "" {
		if len(text) > maxErrorMessageLength {
			text = text[:maxErrorMessageLength] + "…"
		}
		return text
	}
	return fmt.Sprintf("OpenObserve returned status %d", status)
}

// maxErrorMessageLength caps a plain-text OpenObserve error passed on.
const maxErrorMessageLength = 1024

// maxErrorBodyBytes caps how much of an OpenObserve error answer is read.
// Error bodies are small JSON objects or short text, well under this.
const maxErrorBodyBytes = 64 << 10
