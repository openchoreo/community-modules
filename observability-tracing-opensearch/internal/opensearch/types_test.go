// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"io"
	"testing"
)

func TestBuildSearchBody(t *testing.T) {
	query := map[string]interface{}{
		"size": 10,
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
	}

	reader, err := buildSearchBody(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	bodyStr := string(body)
	if bodyStr == "" {
		t.Error("expected non-empty body")
	}
	if len(bodyStr) < 10 {
		t.Errorf("expected longer body, got %q", bodyStr)
	}
}

func TestBuildSearchBody_EmptyQuery(t *testing.T) {
	query := map[string]interface{}{}

	reader, err := buildSearchBody(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if string(body) != "{}" {
		t.Errorf("expected '{}', got %q", string(body))
	}
}

func TestBuildSearchBody_NilQuery(t *testing.T) {
	reader, err := buildSearchBody(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if string(body) != "null" {
		t.Errorf("expected 'null', got %q", string(body))
	}
}
