{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Render the full image reference for a module component, honoring the
global.imageRegistry override. When the override is set, it replaces the
registry host of the image repository (the first path segment containing
"." or ":" or equal to "localhost", per the container reference rules).
The override value may itself carry a path (e.g. registry.example.com/ghcr.io)
for path-preserving mirrors.

Usage: {{ include "observability-logs-opensearch.image" (dict "image" .Values.adapter.image "context" .) }}
Parameters:
  - image: The component image block (repository, tag)
  - context: The chart root context (.)
*/}}
{{- define "observability-logs-opensearch.image" -}}
{{- $repo := .image.repository -}}
{{- with .context.Values.global.imageRegistry -}}
{{- $parts := splitList "/" $repo -}}
{{- $first := first $parts -}}
{{- if and (gt (len $parts) 1) (or (contains "." $first) (contains ":" $first) (eq $first "localhost")) -}}
{{- $repo = join "/" (rest $parts) -}}
{{- end -}}
{{- $repo = printf "%s/%s" . $repo -}}
{{- end -}}
{{- printf "%s:%s" $repo (.image.tag | default .context.Chart.AppVersion) -}}
{{- end }}
