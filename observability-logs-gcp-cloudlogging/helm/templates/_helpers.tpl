{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Resolved name of the Secret holding the webhook shared secret. When the user
provides their own Secret via `sharedSecretRef.name`, use that; otherwise the
chart manages one named after the adapter.
*/}}
{{- define "logs-gcp-cloudlogging.webhookSecretName" -}}
{{- if .Values.adapter.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
logs-adapter-gcp-cloudlogging-webhook-token
{{- end -}}
{{- end -}}

{{/*
Validate required values and fail fast with a readable message.
Called once from templates/validate.yaml.
*/}}
{{- define "logs-gcp-cloudlogging.validate" -}}

{{- if .Values.adapter.enabled -}}

{{- if not .Values.gcp.projectId -}}
{{- fail "gcp.projectId is required. Example: --set gcp.projectId=my-gcp-project" -}}
{{- end -}}
{{- if not .Values.adapter.observerUrl -}}
{{- fail "adapter.observerUrl is required" -}}
{{- end -}}

{{- if .Values.adapter.webhookAuth.enabled -}}
{{- if not (or .Values.adapter.webhookAuth.sharedSecret .Values.adapter.webhookAuth.sharedSecretRef.name) -}}
{{- fail "adapter.webhookAuth requires either sharedSecret or sharedSecretRef.name when enabled" -}}
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

{{- if .Values.adapter.networkPolicy.enabled -}}
{{- if empty .Values.adapter.networkPolicy.observerNamespaceLabels -}}
{{- fail "adapter.networkPolicy.observerNamespaceLabels must be a non-empty map when adapter.networkPolicy is enabled; an empty namespaceSelector would match all namespaces" -}}
{{- end -}}
{{- if empty .Values.adapter.networkPolicy.observerPodLabels -}}
{{- fail "adapter.networkPolicy.observerPodLabels must be a non-empty map when adapter.networkPolicy is enabled; an empty podSelector would match all pods in the selected namespace(s)" -}}
{{- end -}}
{{- if and .Values.adapter.webhookRoute.enabled (empty .Values.adapter.networkPolicy.gatewayNamespaceLabels) -}}
{{- fail "adapter.networkPolicy.gatewayNamespaceLabels must be a non-empty map when networkPolicy and webhookRoute are both enabled; otherwise the NetworkPolicy silently drops gateway->adapter webhook traffic and alerts never forward" -}}
{{- end -}}
{{- end -}}

{{- end -}}{{/* end adapter.enabled */}}

{{- end -}}
