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
{{- end -}}

{{- define "logs-cloudwatch.webhookSecretName" -}}
{{- if .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
logs-adapter-cloudwatch-webhook-token
{{- end -}}
{{- end -}}
