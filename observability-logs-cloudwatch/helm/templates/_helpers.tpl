{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Render the log group path the adapter reads from / the setup Job writes to.
*/}}
{{- define "logs-cloudwatch.logGroupPrefix" -}}
{{- trimSuffix "/" .Values.logGroupPrefix -}}
{{- end -}}

{{/*
Cluster name and AWS region are owned by the amazon-cloudwatch-observability
subchart values (see values.yaml) and read from there by every component in
this chart. Centralising via these helpers keeps a single source of truth
and avoids drift between parent and subchart.
*/}}
{{- define "logs-cloudwatch.clusterName" -}}
{{- (index .Values "amazon-cloudwatch-observability").clusterName -}}
{{- end -}}

{{- define "logs-cloudwatch.region" -}}
{{- (index .Values "amazon-cloudwatch-observability").region -}}
{{- end -}}

{{/*
Validate required values and fail fast with a readable message.
*/}}
{{- define "logs-cloudwatch.validate" -}}
{{- if not (include "logs-cloudwatch.clusterName" .) -}}
{{- fail "amazon-cloudwatch-observability.clusterName is required. Example: --set amazon-cloudwatch-observability.clusterName=openchoreo-dev" -}}
{{- end -}}
{{- if not (include "logs-cloudwatch.region" .) -}}
{{- fail "amazon-cloudwatch-observability.region is required. Example: --set amazon-cloudwatch-observability.region=us-east-1" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookAuth.enabled (not (or .Values.adapter.alerting.webhookAuth.sharedSecret .Values.adapter.alerting.webhookAuth.sharedSecretRef.name)) -}}
{{- fail "adapter.alerting.webhookAuth requires either sharedSecret or sharedSecretRef.name when enabled" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookIngress.enabled (not .Values.adapter.alerting.webhookAuth.enabled) -}}
{{- fail "adapter.alerting.webhookIngress requires adapter.alerting.webhookAuth.enabled=true so the public webhook is not exposed without header auth" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookIngress.enabled (not .Values.adapter.alerting.webhookIngress.host) -}}
{{- fail "adapter.alerting.webhookIngress.host is required when webhookIngress is enabled" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookIngress.enabled (not .Values.adapter.alerting.webhookIngress.tls.secretName) -}}
{{- fail "adapter.alerting.webhookIngress.tls.secretName is required when webhookIngress is enabled" -}}
{{- end -}}
{{- if and .Values.adapter.enabled .Values.adapter.networkPolicy.enabled -}}
{{- if empty .Values.adapter.networkPolicy.observerNamespaceLabels -}}
{{- fail "adapter.networkPolicy.observerNamespaceLabels must be a non-empty map when adapter.networkPolicy is enabled; an empty namespaceSelector would match all namespaces" -}}
{{- end -}}
{{- if empty .Values.adapter.networkPolicy.observerPodLabels -}}
{{- fail "adapter.networkPolicy.observerPodLabels must be a non-empty map when adapter.networkPolicy is enabled; an empty podSelector would match all pods in the selected namespace(s)" -}}
{{- end -}}
{{- end -}}
{{- $validRetentions := list 1 3 5 7 14 30 60 90 120 150 180 365 400 545 731 1096 1827 2192 2557 2922 3288 3653 -}}
{{- if not (has (.Values.containerLogs.retentionDays | int) $validRetentions) -}}
{{- fail (printf "containerLogs.retentionDays=%v is not a valid CloudWatch retention. Allowed values: 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653" .Values.containerLogs.retentionDays) -}}
{{- end -}}
{{- end -}}

{{- define "logs-cloudwatch.webhookSecretName" -}}
{{- if .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
logs-adapter-cloudwatch-webhook-token
{{- end -}}
{{- end -}}
