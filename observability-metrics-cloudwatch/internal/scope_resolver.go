// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/cloudwatchmetrics"
)

const (
	k8sSATokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	k8sCACertPath  = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

type ScopeResolutionParams struct {
	Namespace      string
	Component      string
	Project        string
	Environment    string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
}

type ScopeResolutionResult struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
}

type scopeResolver interface {
	Resolve(context.Context, ScopeResolutionParams) (ScopeResolutionResult, bool, error)
}

type KubernetesScopeResolver struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewKubernetesScopeResolver(logger *slog.Logger) (*KubernetesScopeResolver, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, nil
	}
	tokenBytes, err := os.ReadFile(k8sSATokenPath)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	caBytes, err := os.ReadFile(k8sCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("parse service account CA")
	}
	return &KubernetesScopeResolver{
		baseURL: "https://" + host + ":" + port,
		token:   strings.TrimSpace(string(tokenBytes)),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			},
		},
		logger: logger,
	}, nil
}

func (r *KubernetesScopeResolver) Resolve(ctx context.Context, p ScopeResolutionParams) (ScopeResolutionResult, bool, error) {
	if r == nil {
		return ScopeResolutionResult{}, false, nil
	}
	selectorParts := make([]string, 0, 4)
	addSelector := func(key, val string) {
		if strings.TrimSpace(val) != "" {
			selectorParts = append(selectorParts, key+"="+val)
		}
	}
	addSelector(cloudwatchmetrics.LabelNamespace, p.Namespace)
	addSelector("openchoreo.dev/component", p.Component)
	addSelector("openchoreo.dev/project", p.Project)
	addSelector("openchoreo.dev/environment", p.Environment)
	addSelector(cloudwatchmetrics.LabelComponentUID, p.ComponentUID)
	addSelector(cloudwatchmetrics.LabelProjectUID, p.ProjectUID)
	addSelector(cloudwatchmetrics.LabelEnvironmentUID, p.EnvironmentUID)
	if len(selectorParts) == 0 {
		return ScopeResolutionResult{}, false, nil
	}

	u := r.baseURL + "/api/v1/pods?labelSelector=" + url.QueryEscape(strings.Join(selectorParts, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ScopeResolutionResult{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return ScopeResolutionResult{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return ScopeResolutionResult{}, false, fmt.Errorf("kubernetes pods lookup returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pods podList
	if err := json.NewDecoder(resp.Body).Decode(&pods); err != nil {
		return ScopeResolutionResult{}, false, err
	}
	for _, pod := range pods.Items {
		labels := pod.Metadata.Labels
		componentUID := firstNonEmpty(p.ComponentUID, labels[cloudwatchmetrics.LabelComponentUID])
		environmentUID := firstNonEmpty(p.EnvironmentUID, labels[cloudwatchmetrics.LabelEnvironmentUID])
		if componentUID == "" || environmentUID == "" || pod.Metadata.Namespace == "" {
			continue
		}
		res := ScopeResolutionResult{
			Namespace:      pod.Metadata.Namespace,
			ComponentUID:   componentUID,
			ProjectUID:     firstNonEmpty(p.ProjectUID, labels[cloudwatchmetrics.LabelProjectUID]),
			EnvironmentUID: environmentUID,
		}
		r.logger.Debug("Resolved metrics scope",
			slog.String("namespace", p.Namespace),
			slog.String("component", p.Component),
			slog.String("resolvedNamespace", res.Namespace),
			slog.String("componentUid", res.ComponentUID),
			slog.String("environmentUid", res.EnvironmentUID),
		)
		return res, true, nil
	}
	return ScopeResolutionResult{}, false, nil
}

func firstNonEmpty(vals ...string) string {
	for _, val := range vals {
		if strings.TrimSpace(val) != "" {
			return val
		}
	}
	return ""
}

type podList struct {
	Items []struct {
		Metadata struct {
			Namespace string            `json:"namespace"`
			Labels    map[string]string `json:"labels"`
		} `json:"metadata"`
	} `json:"items"`
}
