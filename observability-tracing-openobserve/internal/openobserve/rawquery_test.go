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
		"a JSON string":       `"SELECT 1"`,
		"not JSON":            `SELECT 1`,
		"no query":            `{"sql":"SELECT 1"}`,
		"no SQL":              `{"query":{"start_time":1}}`,
		"blank SQL":           `{"query":{"sql":"  "}}`,
		"SQL not a string":    `{"query":{"sql":1}}`,
		"query not an object": `{"query":"SELECT 1"}`,
		// Keys outside the allowlist, at the top level and in query.
		"other regions":          `{"query":{"sql":"SELECT 1"},"regions":["us"]}`,
		"other clusters":         `{"query":{"sql":"SELECT 1"},"clusters":["eu"]}`,
		"a search type":          `{"query":{"sql":"SELECT 1"},"search_type":"dashboards"}`,
		"agent options":          `{"query":{"sql":"SELECT 1"},"agent_options":{"output_format":"csv"}}`,
		"an unknown key":         `{"query":{"sql":"SELECT 1"},"pit":{}}`,
		"a VRL function":         `{"query":{"sql":"SELECT 1","query_fn":"."}}`,
		"saved functions in SQL": `{"query":{"sql":"SELECT 1","uses_zo_fn":true}}`,
		"an action":              `{"query":{"sql":"SELECT 1","action_id":"a1"}}`,
		"streaming output":       `{"query":{"sql":"SELECT 1","streaming_output":true}}`,
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

func TestPrepareRawQuery_AllowsTheReviewedKeys(t *testing.T) {
	body := `{"query":{"sql":"SELECT 1","start_time":1,"end_time":2,"from":0,"size":10,` +
		`"quick_mode":false,"query_type":"logs","track_total_hits":true,"skip_wal":false,` +
		`"sampling_ratio":0.5,"histogram_interval":60,"timezone":"UTC"},` +
		`"encoding":"","use_cache":false,"clear_cache":false}`
	if _, err := PrepareRawQuery([]byte(body), time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRawQuery_KeepsLargeNumbersExact(t *testing.T) {
	out, err := PrepareRawQuery([]byte(`{"query":{"sql":"SELECT 1","start_time":1792483200000000123}}`), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "1792483200000000123") {
		t.Errorf("number lost precision: %s", out)
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
		{"OpenObserve stopped the search at the timeout", http.StatusRequestTimeout,
			`{"code":20010,"message":"Search query timed out","error_detail":"flight->search: search timeout"}`,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrRawQueryTimeout) {
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
		{"credentials rejected", http.StatusUnauthorized, `Unauthorized Access`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if err == nil || errors.As(err, &invalid) || !strings.Contains(err.Error(), "credentials") {
					t.Errorf("got %v", err)
				}
			}},
		{"access refused", http.StatusForbidden, `{"code":403,"message":"Unauthorized Access"}`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if err == nil || errors.As(err, &invalid) || !strings.Contains(err.Error(), "credentials") {
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

func TestRawSearch_StopsReadingAnOversizedResult(t *testing.T) {
	// The result is read as it arrives, so the size limit applies before the
	// whole result is in memory: the adapter stops reading, and OpenObserve
	// stops sending, long before the result ends.
	const resultSize = 256 << 20
	chunk := []byte(strings.Repeat("x", 64<<10))
	sent := make(chan int64, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var n int64
		defer func() { sent <- n }()
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, `{"took":1,"hits":["`); err != nil {
			return
		}
		for n < resultSize {
			written, err := w.Write(chunk)
			n += int64(written)
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).RawSearch(context.Background(), []byte(rawBody), 1024)
	if !errors.Is(err, ErrRawQueryTooLarge) {
		t.Fatalf("got %v", err)
	}
	select {
	case n := <-sent:
		if n >= resultSize {
			t.Errorf("the whole %d-byte result was read; the read must stop at the size limit", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OpenObserve kept sending after the adapter stopped reading")
	}
}
