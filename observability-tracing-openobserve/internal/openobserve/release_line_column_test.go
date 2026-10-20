// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func unknownFieldServer(message string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":20004,"message":"` + message + `"}`))
	}))
}

func TestGetTraces_NoReleaseLineColumnYet(t *testing.T) {
	server := unknownFieldServer("Search field not found: No field named service_openchoreo_dev_release_line_uid.")
	defer server.Close()

	result, err := newTestClient(server.URL).GetTraces(context.Background(), TracesQueryParams{
		Scope: Scope{ReleaseLineID: testReleaseLineID},
	})
	if err != nil {
		t.Fatalf("expected no error before any span carries a line, got: %v", err)
	}
	if len(result.Traces) != 0 || result.Total != 0 {
		t.Fatalf("expected empty result, got %+v", result)
	}
}

func TestExecuteSearchQuery_OtherUnknownFieldStillFails(t *testing.T) {
	server := unknownFieldServer("Search field not found: No field named reference_parent_span_id.")
	defer server.Close()

	_, err := newTestClient(server.URL).executeSearchQuery(context.Background(), []byte(`{}`))
	if err == nil || errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected an unknown field other than the release line column to fail, got %v", err)
	}
}
