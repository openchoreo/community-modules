// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package envresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const environmentsPathFmt = "/api/v1/namespaces/%s/environments"

// tokenResponse represents the OAuth2 token endpoint response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// environmentItem represents a single environment from the API response.
type environmentItem struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

// environmentsResponse represents the environments API response.
type environmentsResponse struct {
	Items []environmentItem `json:"items"`
}

// Resolver maintains an in-memory mapping of environment UIDs to names.
type Resolver struct {
	mu     sync.RWMutex
	uidMap map[string]string // environment-uid -> environment name
	logger *slog.Logger
}

// New creates a new Resolver.
func New(logger *slog.Logger) *Resolver {
	return &Resolver{
		uidMap: make(map[string]string),
		logger: logger,
	}
}

// LoadFromAPI fetches environments from the environments API using an OAuth2 token.
func (r *Resolver) LoadFromAPI(ctx context.Context, envAPIBaseURL, oauthTokenURL, clientID, clientSecret string) error {
	// Obtain access token via client credentials grant.
	token, err := r.fetchAccessToken(ctx, oauthTokenURL, clientID, clientSecret)
	if err != nil {
		return fmt.Errorf("failed to obtain access token: %w", err)
	}

	// Fetch environments.
	reqURL := envAPIBaseURL + fmt.Sprintf(environmentsPathFmt, "default")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create environments request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("environments request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read environments response body: %w", err)
	}

	r.logger.Info("environments response", slog.Int("status", resp.StatusCode), slog.String("body", string(body)))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("environments API returned status %d: %s", resp.StatusCode, string(body))
	}

	var envResp environmentsResponse
	if err := json.Unmarshal(body, &envResp); err != nil {
		return fmt.Errorf("failed to decode environments response: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.uidMap = make(map[string]string, len(envResp.Items))
	for _, item := range envResp.Items {
		uid := item.Metadata.UID
		name := item.Metadata.Name
		if uid != "" && name != "" {
			r.uidMap[uid] = name
			r.logger.Info("registered environment", slog.String("name", name), slog.String("uid", uid))
		}
	}

	r.logger.Info("loaded environments", slog.Int("count", len(r.uidMap)))
	return nil
}

// fetchAccessToken obtains an OAuth2 access token using client credentials grant.
func (r *Resolver) fetchAccessToken(ctx context.Context, tokenURL, clientID, clientSecret string) (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"scope":         {"openid email profile"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return tokenResp.AccessToken, nil
}

// ResolveUID returns the environment name for a given UID, or the UID itself if not found.
func (r *Resolver) ResolveUID(uid string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name, ok := r.uidMap[uid]; ok {
		return name
	}
	return uid
}

// GetUIDMap returns a copy of the current UID-to-name mapping.
func (r *Resolver) GetUIDMap() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := make(map[string]string, len(r.uidMap))
	for k, v := range r.uidMap {
		m[k] = v
	}
	return m
}
