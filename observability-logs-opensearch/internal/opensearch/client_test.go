// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestClient creates a Client that points at the given test server URL.
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	apiClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{serverURL},
		},
	})
	if err != nil {
		t.Fatalf("failed to create test client: %v", err)
	}
	return &Client{
		client: apiClient,
		logger: testLogger(),
	}
}

func TestCheckHealth(t *testing.T) {
	t.Run("green status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_cluster/health" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "green"})
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.CheckHealth(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("yellow status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_cluster/health" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "yellow"})
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.CheckHealth(context.Background())
		if err != nil {
			t.Fatalf("unexpected error for yellow status: %v", err)
		}
	})

	t.Run("red status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_cluster/health" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "red"})
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.CheckHealth(context.Background())
		if err == nil {
			t.Fatal("expected error for red status")
		}
		if !strings.Contains(err.Error(), "red") {
			t.Errorf("expected error to mention 'red', got: %v", err)
		}
	})

	t.Run("error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_cluster/health" {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, "cluster unavailable")
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.CheckHealth(context.Background())
		if err == nil {
			t.Fatal("expected error for non-200 response")
		}
	})
}

func TestSearch(t *testing.T) {
	t.Run("success with hits", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := map[string]interface{}{
				"took":      5,
				"timed_out": false,
				"hits": map[string]interface{}{
					"total": map[string]interface{}{
						"value":    2,
						"relation": "eq",
					},
					"hits": []map[string]interface{}{
						{
							"_id":    "hit-1",
							"_score": 1.5,
							"_source": map[string]interface{}{
								"log":        "test log 1",
								"@timestamp": "2025-06-15T10:00:00Z",
							},
						},
						{
							"_id":    "hit-2",
							"_score": 1.0,
							"_source": map[string]interface{}{
								"log":        "test log 2",
								"@timestamp": "2025-06-15T11:00:00Z",
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		query := map[string]interface{}{
			"size":  10,
			"query": map[string]interface{}{"match_all": map[string]interface{}{}},
		}

		result, err := client.Search(context.Background(), []string{"logs-*"}, query)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Hits.Total.Value != 2 {
			t.Errorf("expected 2 total hits, got %d", result.Hits.Total.Value)
		}
		if len(result.Hits.Hits) != 2 {
			t.Errorf("expected 2 hits, got %d", len(result.Hits.Hits))
		}
		if result.Took != 5 {
			t.Errorf("expected took=5, got %d", result.Took)
		}
	})

	t.Run("error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"bad request"}`)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.Search(context.Background(), []string{"logs-*"}, map[string]interface{}{})
		if err == nil {
			t.Fatal("expected error for bad request")
		}
	})
}

func TestSearchMonitorByName(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				resp := `{
					"hits": {
						"total": {"value": 1, "relation": "eq"},
						"hits": [{"_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890", "_source": {"name": "test-monitor"}}]
					}
				}`
				fmt.Fprint(w, resp)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		id, found, err := client.SearchMonitorByName(context.Background(), "test-monitor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found {
			t.Fatal("expected monitor to be found")
		}
		if id != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
			t.Errorf("expected id 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', got %q", id)
		}
	})

	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				resp := `{
					"hits": {
						"total": {"value": 0, "relation": "eq"},
						"hits": []
					}
				}`
				fmt.Fprint(w, resp)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, found, err := client.SearchMonitorByName(context.Background(), "nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Fatal("expected monitor not to be found")
		}
	})

	t.Run("error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, _, err := client.SearchMonitorByName(context.Background(), "test")
		if err == nil {
			t.Fatal("expected error for server error")
		}
	})
}

func TestCreateMonitor(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_plugins/_alerting/monitors" && r.Method == "POST" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				resp := `{"_id": "b2c3d4e5-f6a7-8901-bcde-f12345678901", "monitor": {"last_update_time": 1718444400000}}`
				fmt.Fprint(w, resp)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		id, lastUpdate, err := client.CreateMonitor(context.Background(), map[string]interface{}{"name": "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "b2c3d4e5-f6a7-8901-bcde-f12345678901" {
			t.Errorf("expected id 'b2c3d4e5-f6a7-8901-bcde-f12345678901', got %q", id)
		}
		if lastUpdate != 1718444400000 {
			t.Errorf("expected last_update_time 1718444400000, got %d", lastUpdate)
		}
	})

	t.Run("error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"bad request"}`)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, _, err := client.CreateMonitor(context.Background(), map[string]interface{}{"name": "test"})
		if err == nil {
			t.Fatal("expected error for bad request")
		}
	})
}

func TestGetMonitorByID(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				resp := `{"monitor": {"name": "test-monitor", "enabled": true}}`
				fmt.Fprint(w, resp)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		monitor, err := client.GetMonitorByID(context.Background(), "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if monitor["name"] != "test-monitor" {
			t.Errorf("expected name 'test-monitor', got %v", monitor["name"])
		}
	})

	t.Run("missing monitor field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"other": "data"}`)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.GetMonitorByID(context.Background(), "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
		if err == nil {
			t.Fatal("expected error for missing monitor field")
		}
		if !strings.Contains(err.Error(), "monitor object not found") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"not found"}`)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.GetMonitorByID(context.Background(), "nonexistent")
		if err == nil {
			t.Fatal("expected error for 404")
		}
	})
}

func TestUpdateMonitor(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "PUT" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				resp := `{"_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890", "monitor": {"last_update_time": 1718444400001}}`
				fmt.Fprint(w, resp)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		lastUpdate, err := client.UpdateMonitor(context.Background(), "a1b2c3d4-e5f6-7890-abcd-ef1234567890", map[string]interface{}{"name": "updated"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lastUpdate != 1718444400001 {
			t.Errorf("expected last_update_time 1718444400001, got %d", lastUpdate)
		}
	})

	t.Run("error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"internal error"}`)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.UpdateMonitor(context.Background(), "a1b2c3d4-e5f6-7890-abcd-ef1234567890", map[string]interface{}{})
		if err == nil {
			t.Fatal("expected error for server error")
		}
	})
}

func TestDeleteMonitor(t *testing.T) {
	t.Run("success 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "DELETE" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.DeleteMonitor(context.Background(), "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("success 204", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "DELETE" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.DeleteMonitor(context.Background(), "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"not found"}`)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		err := client.DeleteMonitor(context.Background(), "nonexistent")
		if err == nil {
			t.Fatal("expected error for 404")
		}
	})
}

func TestWriteAlertEntry(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			resp := map[string]interface{}{
				"_id":     "alert-entry-1",
				"_index":  "openchoreo-alerts",
				"result":  "created",
				"_shards": map[string]interface{}{"total": 2, "successful": 1, "failed": 0},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		id, err := client.WriteAlertEntry(context.Background(), map[string]interface{}{
			"ruleName":  "test-rule",
			"alertTime": "2025-06-15T10:00:00Z",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "alert-entry-1" {
			t.Errorf("expected id 'alert-entry-1', got %q", id)
		}
	})

	t.Run("error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"internal error"}`)
		}))
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.WriteAlertEntry(context.Background(), map[string]interface{}{})
		if err == nil {
			t.Fatal("expected error for server error")
		}
	})
}
