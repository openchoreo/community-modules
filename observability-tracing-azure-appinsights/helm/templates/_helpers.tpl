{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Fail fast on missing required values. Called once from templates/validate.yaml.
*/}}
{{- define "tracing-azure-appinsights.validate" -}}

{{- if .Values.adapter.enabled -}}
{{- if not .Values.logAnalytics.workspaceId -}}
{{- fail "logAnalytics.workspaceId is required (workspace customerId GUID). Example: --set logAnalytics.workspaceId=00000000-0000-0000-0000-000000000000" -}}
{{- end -}}
{{- end -}}

{{- $mode := .Values.global.installationMode -}}
{{- if not (has $mode (list "singleCluster" "multiClusterExporter" "multiClusterReceiver")) -}}
{{- fail "global.installationMode must be one of singleCluster, multiClusterExporter, multiClusterReceiver" -}}
{{- end -}}

{{- if eq $mode "multiClusterExporter" -}}
{{- if not .Values.opentelemetryCollectorCustomizations.http.observabilityPlaneUrl -}}
{{- fail "opentelemetryCollectorCustomizations.http.observabilityPlaneUrl is required in multiClusterExporter mode" -}}
{{- end -}}
{{- end -}}

{{- end -}}
