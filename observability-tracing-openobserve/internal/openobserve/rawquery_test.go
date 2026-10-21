// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const rawBody = `{"query":{"sql":"SELECT trace_id, duration FROM \"default\" WHERE span_status = 'ERROR' LIMIT 10",` +
	`"start_time":1792483200000000,"end_time":1792486800000000}}`

func TestPrepareRawQuery_KeepsTheBodyAndSetsTheTimeout(t *testing.T) {
	out, err := PrepareRawQuery([]byte(`{"query":{"sql":"SELECT 1","start_time":1792483200000000,`+
		`"end_time":1792486800000000,"size":50},"timeout":600}`), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"query":{"sql":"SELECT 1","start_time":1792483200000000,"end_time":1792486800000000,"size":50},"timeout":30}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestPrepareRawQuery_Rejects(t *testing.T) {
	for name, body := range map[string]string{
		"a JSON string":    `"SELECT 1"`,
		"not JSON":         `SELECT 1`,
		"no query":         `{"sql":"SELECT 1"}`,
		"no SQL":           `{"query":{"start_time":1}}`,
		"blank SQL":        `{"query":{"sql":"  "}}`,
		"SQL not a string": `{"query":{"sql":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PrepareRawQuery([]byte(body), time.Second)
			var invalid *InvalidRawQueryError
			if !errors.As(err, &invalid) || !strings.HasPrefix(err.Error(), "invalid OpenObserve search request: ") {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestRawSearch_SendsTheBodyAndReturnsTheResponse(t *testing.T) {
	const response = `{"took":12,"hits":[{"trace_id":"t1","duration":123456789012345678}],"total":1}`
	var gotPath, gotQuery, gotUser, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotUser, _, _ = r.BasicAuth()
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = io.WriteString(w, response)
	}))
	defer srv.Close()

	res, err := newTestClient(srv.URL).RawSearch(context.Background(), []byte(rawBody), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/default/_search" || gotQuery != "type=traces" || gotUser != "admin" {
		t.Errorf("request went to %s?%s as %s", gotPath, gotQuery, gotUser)
	}
	if gotBody != rawBody {
		t.Errorf("the body must be sent as is: %s", gotBody)
	}
	if string(res) != response {
		t.Errorf("the response must be OpenObserve's, unchanged: %s", res)
	}
}

func TestRawSearch_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{"SQL error", http.StatusBadRequest,
			`{"code":400,"message":"Search SQL not supported: sql parser error: Expected end of statement, found: FROMM"}`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				want := "invalid OpenObserve search request: Search SQL not supported: sql parser error: Expected end of statement, found: FROMM"
				if !errors.As(err, &invalid) || invalid.Error() != want {
					t.Errorf("got %v", err)
				}
			}},
		{"unknown stream", http.StatusBadRequest, `{"code":20002,"message":"Search stream not found: nope"}`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "Search stream not found: nope") {
					t.Errorf("got %v", err)
				}
			}},
		{"body OpenObserve can't decode", http.StatusUnprocessableEntity,
			"Failed to deserialize the JSON body into the target type: query: missing field `start_time` at line 1 column 51",
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				want := "invalid OpenObserve search request: Failed to deserialize the JSON body into the target type: " +
					"query: missing field `start_time` at line 1 column 51"
				if !errors.As(err, &invalid) || invalid.Error() != want {
					t.Errorf("got %v", err)
				}
			}},
		{"partial result", http.StatusOK, `{"took":30000,"hits":[],"is_partial":true}`,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrRawQueryTimeout) {
					t.Errorf("got %v", err)
				}
			}},
		{"too large", http.StatusOK, `{"took":1,"hits":["` + strings.Repeat("x", 2048) + `"]}`,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrRawQueryTooLarge) {
					t.Errorf("got %v", err)
				}
			}},
		{"an oversized rejection is still a rejection", http.StatusBadRequest,
			`{"code":400,"message":"` + strings.Repeat("x", 2048) + `"}`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if !errors.As(err, &invalid) || errors.Is(err, ErrRawQueryTooLarge) {
					t.Errorf("got %v", err)
				}
			}},
		{"server error", http.StatusInternalServerError, `{"code":500,"message":"boom"}`,
			func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "status 500") {
					t.Errorf("got %v", err)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			_, err := newTestClient(srv.URL).RawSearch(context.Background(), []byte(rawBody), 1024)
			tt.check(t, err)
		})
	}
}

func TestRawSearch_ContextDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := newTestClient(srv.URL).RawSearch(ctx, []byte(rawBody), 1024)
	if !errors.Is(err, ErrRawQueryTimeout) {
		t.Errorf("got %v", err)
	}
}
