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
