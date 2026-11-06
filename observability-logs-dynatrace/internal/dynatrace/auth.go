// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultOAuthTokenURL is the Dynatrace SSO token endpoint for SaaS environments.
const DefaultOAuthTokenURL = "https://sso.dynatrace.com/sso/oauth2/token"

// DefaultOAuthScope is the smallest scope set the adapter's queries need.
const DefaultOAuthScope = "storage:logs:read storage:buckets:read"

// tokenRefreshMargin renews an OAuth token this long before it expires, so a query never
// starts with a token that lapses mid-flight.
const tokenRefreshMargin = 60 * time.Second

// TokenSource yields the bearer token sent with each query.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a Dynatrace platform token, which does not expire on its own.
type StaticToken string

// Token returns the platform token.
func (t StaticToken) Token(context.Context) (string, error) {
	if t == "" {
		return "", fmt.Errorf("platform token is empty")
	}
	return string(t), nil
}

// OAuthConfig configures the client-credentials grant against Dynatrace SSO.
type OAuthConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	// Resource is the optional account or environment URN, e.g. urn:dtaccount:<uuid>.
	Resource string
}

// OAuthTokenSource exchanges OAuth client credentials for access tokens and caches them
// until shortly before they expire.
type OAuthTokenSource struct {
	cfg        OAuthConfig
	httpClient *http.Client
	now        func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewOAuthTokenSource returns a token source for the client-credentials grant.
func NewOAuthTokenSource(cfg OAuthConfig, httpClient *http.Client) *OAuthTokenSource {
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultOAuthTokenURL
	}
	if cfg.Scope == "" {
		cfg.Scope = DefaultOAuthScope
	}
	if httpClient == nil {
		httpClient = newNoRedirectClient(15 * time.Second)
	}
	return &OAuthTokenSource{cfg: cfg, httpClient: httpClient, now: time.Now}
}

// Token returns a cached access token, fetching a new one when it is missing or close
// to expiry.
func (s *OAuthTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && s.now().Before(s.expires.Add(-tokenRefreshMargin)) {
		return s.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.cfg.ClientID},
		"client_secret": {s.cfg.ClientSecret},
		"scope":         {s.cfg.Scope},
	}
	if s.cfg.Resource != "" {
		form.Set("resource", s.cfg.Resource)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call token endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(body)
		if len(msg) > maxErrorBody {
			msg = msg[:maxErrorBody]
		}
		return "", fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(msg))
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("token response carried no access_token")
	}

	s.token = parsed.AccessToken
	s.expires = s.now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	return s.token, nil
}
