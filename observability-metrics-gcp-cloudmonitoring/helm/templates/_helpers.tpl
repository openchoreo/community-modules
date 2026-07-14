{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Resolved name of the Secret holding the webhook shared secret. When the user
provides their own Secret via `sharedSecretRef.name`, use that; otherwise the
chart manages one named after the adapter.
*/}}
{{- define "metrics-gcp-cloudmonitoring.webhookSecretName" -}}
{{- if .Values.adapter.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
metrics-adapter-gcp-cloudmonitoring-webhook-token
{{- end -}}
{{- end -}}

{{/*
Validate required values and fail fast with a readable message.
Called once from templates/validate.yaml.
*/}}
{{- define "metrics-gcp-cloudmonitoring.validate" -}}

{{- if .Values.adapter.enabled -}}

{{- if not .Values.gcp.projectId -}}
{{- fail "gcp.projectId is required. Example: --set gcp.projectId=my-gcp-project" -}}
{{- end -}}

{{/* Alerting is enabled only when both observerUrl and a notification channel
are set; when webhookAuth is on it needs a secret. Metrics-only installs need
none of these. */}}
{{- if .Values.adapter.webhookAuth.enabled -}}
{{- if and .Values.notificationChannel.id (not (or .Values.adapter.webhookAuth.sharedSecret .Values.adapter.webhookAuth.sharedSecretRef.name)) -}}
{{- fail "adapter.webhookAuth requires either sharedSecret or sharedSecretRef.name when enabled and a notification channel is configured" -}}
{{- end -}}
{{- if and .Values.adapter.webhookAuth.sharedSecret (lt (len .Values.adapter.webhookAuth.sharedSecret) 16) -}}
{{- fail "adapter.webhookAuth.sharedSecret must be at least 16 characters" -}}
{{- end -}}
{{- end -}}

{{- if and .Values.adapter.webhookRoute.enabled (not .Values.adapter.webhookAuth.enabled) -}}
{{- fail "adapter.webhookRoute requires adapter.webhookAuth.enabled=true so the public webhook is not exposed without auth" -}}
{{- end -}}
{{- if and .Values.adapter.webhookRoute.enabled (not .Values.adapter.webhookRoute.parentRef.name) -}}
{{- fail "adapter.webhookRoute.parentRef.name is required when webhookRoute is enabled" -}}
{{- end -}}

{{- end -}}{{/* end adapter.enabled */}}

{{- end -}}
