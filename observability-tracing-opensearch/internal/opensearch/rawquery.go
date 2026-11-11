// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// QueryLanguage names the query language raw queries are written in.
const QueryLanguage = "OpenSearch query DSL"

var (
	// ErrRawQueryTimeout is returned when a raw query runs past its timeout.
	ErrRawQueryTimeout = errors.New("raw query timed out")
	// ErrRawQueryTooLarge is returned when the result exceeds the maximum response size.
	ErrRawQueryTooLarge = errors.New("raw query result exceeds the maximum response size")
)

// InvalidRawQueryError reports a raw query that can't be run, either because
// it uses something raw queries don't allow or because OpenSearch rejected it.
type InvalidRawQueryError struct {
	Reason string
}

func (e *InvalidRawQueryError) Error() string {
	return "invalid " + QueryLanguage + ": " + e.Reason
}

func invalidRawQuery(format string, args ...any) error {
	return &InvalidRawQueryError{Reason: fmt.Sprintf(format, args...)}
}

// allowedTopLevelKeys are the search body keys a raw query may use. Anything
// else is rejected, so a new OpenSearch feature can't bypass the checks below
// until it has been reviewed.
var allowedTopLevelKeys = map[string]bool{
	"query": true, "aggs": true, "aggregations": true, "size": true, "from": true,
	"sort": true, "_source": true, "fields": true, "docvalue_fields": true,
	"script_fields": true, "runtime_mappings": true, "post_filter": true,
	"highlight": true, "collapse": true, "track_total_hits": true,
	"search_after": true, "min_score": true, "terminate_after": true,
	"timeout": true, "explain": true, "version": true, "profile": true,
}

// PrepareRawQuery checks a raw search body and returns the body to send: the
// body as written, with the timeout set to the configured one. The time range,
// size and everything else stay as the caller wrote them.
func PrepareRawQuery(body []byte, timeout time.Duration) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || top == nil {
		return nil, invalidRawQuery("the request must be an OpenSearch search body (a JSON object)")
	}

	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowedTopLevelKeys[k] {
			return nil, invalidRawQuery("%q is not allowed in a raw query", k)
		}
	}

	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, invalidRawQuery("the request is not valid JSON")
	}
	if path, ok := findKey(tree, "", isIndexReference); ok {
		return nil, invalidRawQuery("%s: raw queries can only read the trace indices", path)
	}
	for _, aggsKey := range []string{"aggs", "aggregations"} {
		if aggs, ok := top[aggsKey]; ok {
			if path, found := findGlobalAggregation(aggs, aggsKey); found {
				return nil, invalidRawQuery("%s: global aggregations are not allowed, as they ignore the query", path)
			}
		}
	}

	// The timeout is the adapter's: a body can't extend it.
	top["timeout"] = json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("%dms", timeout.Milliseconds())))

	return json.Marshal(top)
}

// RawSearch runs a prepared raw query against the indices matching
// indexPattern and returns OpenSearch's response as is. It reads at most
// maxResponseBytes of the response, and asks OpenSearch to cancel the search
// when the timeout passes.
func (c *Client) RawSearch(
	ctx context.Context, indexPattern string, body []byte, timeout time.Duration, maxResponseBytes int64,
) (json.RawMessage, error) {
	if indexPattern == "" {
		return nil, fmt.Errorf("an index pattern must be specified")
	}
	params := url.Values{}
	params.Set("ignore_unavailable", "true")
	params.Set("allow_no_indices", "true")
	params.Set("cancel_after_time_interval", fmt.Sprintf("%dms", timeout.Milliseconds()))
	path := "/" + indexPattern + "/_search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrRawQueryTimeout
		}
		return nil, fmt.Errorf("raw search request failed: %w", err)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrRawQueryTimeout
		}
		return nil, fmt.Errorf("failed to read raw search response: %w", err)
	}
	if res.StatusCode == http.StatusUnauthorized {
		// The read-only user is missing or its password is out of date: a
		// configuration problem, not a problem with the query.
		return nil, fmt.Errorf("OpenSearch rejected the raw query user's credentials: %s",
			openSearchErrorReason(data, res.StatusCode))
	}
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return nil, &InvalidRawQueryError{Reason: openSearchErrorReason(data, res.StatusCode)}
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("raw search failed with status %d: %s", res.StatusCode, openSearchErrorReason(data, res.StatusCode))
	}
	// Only a result is held to the maximum response size; an error answer
	// keeps its own meaning whatever its length.
	if int64(len(data)) > maxResponseBytes {
		return nil, ErrRawQueryTooLarge
	}

	var meta struct {
		TimedOut bool `json:"timed_out"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse raw search response: %w", err)
	}
	if meta.TimedOut {
		// OpenSearch returns what it found so far; a partial result is never
		// passed on as if it were complete.
		return nil, ErrRawQueryTimeout
	}
	return data, nil
}

// openSearchErrorReason extracts the root cause from an OpenSearch error body.
func openSearchErrorReason(data []byte, status int) string {
	var e struct {
		Error struct {
			Type      string `json:"type"`
			Reason    string `json:"reason"`
			RootCause []struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"root_cause"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		if len(e.Error.RootCause) > 0 && e.Error.RootCause[0].Reason != "" {
			return e.Error.RootCause[0].Type + ": " + e.Error.RootCause[0].Reason
		}
		if e.Error.Reason != "" {
			return e.Error.Type + ": " + e.Error.Reason
		}
	}
	return fmt.Sprintf("OpenSearch returned status %d", status)
}

// isIndexReference matches the keys that make a query read documents from
// another index: terms lookups, more_like_this documents, indexed shapes and
// percolate all name the index with "index" or "_index".
func isIndexReference(key string) bool {
	return key == "index" || key == "_index"
}

// findKey walks a decoded JSON tree and returns the path of the first object
// key that matches.
func findKey(node any, path string, match func(string) bool) (string, bool) {
	switch v := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			childPath := joinPath(path, k)
			if match(k) {
				return childPath, true
			}
			if p, ok := findKey(v[k], childPath, match); ok {
				return p, true
			}
		}
	case []any:
		for i, item := range v {
			if p, ok := findKey(item, fmt.Sprintf("%s[%d]", path, i), match); ok {
				return p, true
			}
		}
	}
	return "", false
}

// findGlobalAggregation walks an aggregations object, including nested
// sub-aggregations, for a global aggregation.
func findGlobalAggregation(raw json.RawMessage, path string) (string, bool) {
	var aggs map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &aggs); err != nil {
		return "", false
	}
	names := make([]string, 0, len(aggs))
	for name := range aggs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		def := aggs[name]
		aggPath := joinPath(path, name)
		if _, ok := def["global"]; ok {
			return joinPath(aggPath, "global"), true
		}
		for _, sub := range []string{"aggs", "aggregations"} {
			if nested, ok := def[sub]; ok {
				if p, found := findGlobalAggregation(nested, joinPath(aggPath, sub)); found {
					return p, true
				}
			}
		}
	}
	return "", false
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
