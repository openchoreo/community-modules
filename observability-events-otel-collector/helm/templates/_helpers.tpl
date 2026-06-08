{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Base name for namespaced resources (Deployment, ConfigMap, SA, PVC, Service).
*/}}
{{- define "events-collector.fullname" -}}
{{- default "events-collector" .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Name for cluster-scoped resources (ClusterRole/Binding). Namespace-suffixed so
two installs in different namespaces don't collide on a cluster-wide object.
*/}}
{{- define "events-collector.clusterScopedName" -}}
{{- printf "%s-%s" (include "events-collector.fullname" .) .Release.Namespace | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "events-collector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "events-collector.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "events-collector.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: openchoreo
{{ include "events-collector.selectorLabels" . }}
{{- end -}}

{{- define "events-collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "events-collector.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Comma-separated service extensions: health_check, file_storage when persistence
is on, plus every extraExtensions key (auto-wired, no separate list to maintain).
*/}}
{{- define "events-collector.serviceExtensions" -}}
{{- $exts := list "health_check" -}}
{{- if .Values.persistence.enabled -}}{{- $exts = append $exts "file_storage" -}}{{- end -}}
{{- range $name, $cfg := .Values.extraExtensions -}}{{- $exts = append $exts $name -}}{{- end -}}
{{- join ", " $exts -}}
{{- end -}}
