// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/openobserve"
)

type LogsHandler struct {
	client *openobserve.Client
	logger *slog.Logger
}

func NewLogsHandler(client *openobserve.Client, logger *slog.Logger) *LogsHandler {
	return &LogsHandler{
		client: client,
		logger: logger,
	}
}

// Processes the request received by POST /api/v1/logs/query API. It decodes the request body to determine the type 
// of log query and then calls the appropriate client method to fetch logs from OpenObserve. Finally, it encodes the 
// response as JSON and sends it back to the client.
func (h *LogsHandler) HandleLogsQuery(w http.ResponseWriter, r *http.Request) {
	var rawRequest struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"-"`
	}

	// Read the full body so we can decode it twice
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("Failed to read request body", slog.Any("error", err))
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if err := json.Unmarshal(body, &rawRequest); err != nil {
		h.logger.Error("Failed to decode request body", slog.Any("error", err))
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var result interface{}

	switch rawRequest.Type {
	case "component":  // Logs of a component deployed into OpenChoreo
		var params openobserve.ComponentLogsParams
		if err := json.Unmarshal(body, &params); err != nil {
			h.logger.Error("Failed to decode component logs params", slog.Any("error", err))
			http.Error(w, "Invalid component logs params", http.StatusBadRequest)
			return
		}
		res, err := h.client.GetComponentLogs(r.Context(), params)
		if err != nil {
			h.logger.Error("Failed to get component logs", slog.Any("error", err))
			http.Error(w, "Failed to fetch component logs", http.StatusInternalServerError)
			return
		}
		result = res
	default:
		h.logger.Error("Unknown log query type", slog.String("type", rawRequest.Type))
		http.Error(w, fmt.Sprintf("Unknown log query type: %s. Supported types are \"component\"", rawRequest.Type), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("Failed to encode response", slog.Any("error", err))
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func (h *LogsHandler) HandleCreateAlert(w http.ResponseWriter, r *http.Request) {
	ruleName := r.PathValue("ruleName")
	if ruleName == "" {
		h.logger.Error("Rule name is required")
		http.Error(w, "Rule name is required", http.StatusBadRequest)
		return
	}

	var params openobserve.LogAlertParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		h.logger.Error("Failed to decode request body", slog.Any("error", err))
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	params.Name = ruleName

	if err := h.client.CreateAlert(r.Context(), params); err != nil {
		h.logger.Error("Failed to create alert", slog.Any("error", err))
		http.Error(w, "Failed to create alert", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Alert created successfully"})
}

func (h *LogsHandler) HandleDeleteAlert(w http.ResponseWriter, r *http.Request) {
	ruleName := r.PathValue("ruleName")
	if ruleName == "" {
		h.logger.Error("Rule name is required")
		http.Error(w, "Rule name is required", http.StatusBadRequest)
		return
	}

	if err := h.client.DeleteAlert(r.Context(), ruleName); err != nil {
		h.logger.Error("Failed to delete alert", slog.Any("error", err))
		http.Error(w, "Failed to delete alert", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Alert deleted successfully"})
}
