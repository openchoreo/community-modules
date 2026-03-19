{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "observability-tracing-opensearch.validate" -}}
{{- $mode := (default "" .Values.global.installationMode) -}}
{{- $allowed := list "singleCluster" "multiClusterExporter" "multiClusterReceiver" -}}
{{- if not (has $mode $allowed) -}}
{{- fail (printf "global.installationMode must be one of [%s] (got %q)" (join ", " $allowed) $mode) -}}
{{- end -}}

{{- if eq $mode "multiClusterExporter" -}}
{{- if not .Values.opentelemetryCollectorCustomizations.http.observabilityPlaneUrl -}}
{{- fail "opentelemetryCollectorCustomizations.http.observabilityPlaneUrl is required when global.installationMode is set to \"multiClusterExporter\"." -}}
{{- end -}}
{{- end -}}
{{- end -}}
