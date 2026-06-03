{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Resolved name of the Secret holding the webhook shared secret. When the user
provides their own Secret via `sharedSecretRef.name`, use that; otherwise the
chart manages one named after the adapter.
*/}}
{{- define "logs-azure-loganalytics.webhookSecretName" -}}
{{- if .Values.adapter.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
logs-adapter-azure-loganalytics-webhook-token
{{- end -}}
{{- end -}}

{{/*
Validate required values and fail fast with a readable message.
Called once from templates/validate.yaml.
*/}}
{{- define "logs-azure-loganalytics.validate" -}}

{{- if .Values.adapter.enabled -}}

{{- if not .Values.azure.subscriptionId -}}
{{- fail "azure.subscriptionId is required. Example: --set azure.subscriptionId=00000000-0000-0000-0000-000000000000" -}}
{{- end -}}
{{- if not .Values.azure.resourceGroup -}}
{{- fail "azure.resourceGroup is required. Example: --set azure.resourceGroup=my-rg" -}}
{{- end -}}
{{- if not .Values.azure.region -}}
{{- fail "azure.region is required. Example: --set azure.region=eastus2" -}}
{{- end -}}
{{- if not .Values.logAnalytics.workspaceId -}}
{{- fail "logAnalytics.workspaceId is required (workspace customerId GUID)" -}}
{{- end -}}
{{- if not .Values.logAnalytics.workspaceResourceId -}}
{{- fail "logAnalytics.workspaceResourceId is required (full ARM ID of the workspace)" -}}
{{- end -}}
{{- if not .Values.actionGroup.id -}}
{{- fail "actionGroup.id is required (ARM ID of the pre-existing Action Group)" -}}
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
{{- end -}}

{{- end -}}{{/* end adapter.enabled */}}

{{- end -}}
