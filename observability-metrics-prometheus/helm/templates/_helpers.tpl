{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "observability-metrics-prometheus.validate" -}}
{{- $mode := (default "" .Values.global.installationMode) -}}
{{- $allowed := list "singleCluster" "multiClusterExporter" "multiClusterReceiver" -}}
{{- if not (has $mode $allowed) -}}
{{- fail (printf "global.installationMode must be one of [%s] (got %q)" (join ", " $allowed) $mode) -}}
{{- end -}}

{{- if eq $mode "multiClusterExporter" -}}
{{- $obsPlaneUrl := "" -}}
{{- with .Values.prometheusCustomizations }}{{ with .http }}{{ $obsPlaneUrl = .observabilityPlaneUrl }}{{ end }}{{ end -}}
{{- if not $obsPlaneUrl -}}
{{- fail "prometheusCustomizations.http.observabilityPlaneUrl is required when global.installationMode is set to \"multiClusterExporter\"." -}}
{{- end -}}
{{- $kps := index .Values "kube-prometheus-stack" -}}
{{- if and $kps $kps.prometheus $kps.prometheus.enabled -}}
{{- fail "kube-prometheus-stack.prometheus.enabled must be set to false when global.installationMode is \"multiClusterExporter\". The PrometheusAgent handles scraping in this mode." -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Render the full image reference for a module component, honoring the
global.imageRegistry override. When the override is set, it replaces the
registry host of the image repository (the first path segment containing
"." or ":" or equal to "localhost", per the container reference rules).
The override value may itself carry a path (e.g. registry.example.com/ghcr.io)
for path-preserving mirrors.

Usage: {{ include "observability-metrics-prometheus.image" (dict "image" .Values.<component>.image "context" .) }}
Parameters:
  - image: The component image block (repository, tag)
  - context: The chart root context (.)
*/}}
{{- define "observability-metrics-prometheus.image" -}}
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
