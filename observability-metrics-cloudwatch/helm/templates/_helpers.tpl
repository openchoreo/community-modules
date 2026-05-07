{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "metrics-cloudwatch.clusterName" -}}
{{- .Values.clusterName -}}
{{- end -}}

{{- define "metrics-cloudwatch.region" -}}
{{- .Values.region -}}
{{- end -}}

{{- define "metrics-cloudwatch.metricNamespace" -}}
{{- default "OpenChoreo/Metrics" .Values.metrics.namespace -}}
{{- end -}}

{{- define "metrics-cloudwatch.logGroup" -}}
{{- if .Values.metrics.logGroup -}}
{{- .Values.metrics.logGroup -}}
{{- else -}}
{{- printf "/aws/openchoreo/%s/metrics" (include "metrics-cloudwatch.clusterName" .) -}}
{{- end -}}
{{- end -}}

{{- define "metrics-cloudwatch.logRetentionServiceAccountName" -}}
{{- $metrics := .Values.metrics | default dict -}}
{{- $logRetention := (get $metrics "logRetention") | default dict -}}
{{- $serviceAccount := (get $logRetention "serviceAccount") | default dict -}}
{{- $create := true -}}
{{- if hasKey $serviceAccount "create" -}}{{- $create = get $serviceAccount "create" -}}{{- end -}}
{{- $name := get $serviceAccount "name" | default "" -}}
{{- if $create -}}
{{- default "metrics-cloudwatch-log-retention" $name -}}
{{- else -}}
{{- default "default" $name -}}
{{- end -}}
{{- end -}}

{{- define "metrics-cloudwatch.webhookSecretName" -}}
{{- if .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
metrics-adapter-cloudwatch-webhook-token
{{- end -}}
{{- end -}}

{{- define "metrics-cloudwatch.validate" -}}
{{- if not (include "metrics-cloudwatch.clusterName" .) -}}
{{- fail "clusterName is required. Example: --set clusterName=openchoreo-dev" -}}
{{- end -}}
{{- if not (include "metrics-cloudwatch.region" .) -}}
{{- fail "region is required. Example: --set region=us-east-1" -}}
{{- end -}}
{{- $metrics := .Values.metrics | default dict -}}
{{- $logRetention := (get $metrics "logRetention") | default dict -}}
{{- $logRetentionEnabled := true -}}
{{- if hasKey $logRetention "enabled" -}}{{- $logRetentionEnabled = get $logRetention "enabled" -}}{{- end -}}
{{- if $logRetentionEnabled -}}
{{- $retention := int .Values.metrics.logRetentionDays -}}
{{- $validRetentions := list 1 3 5 7 14 30 60 90 120 150 180 365 400 545 731 1096 1827 2192 2557 2922 3288 3653 -}}
{{- if not (has $retention $validRetentions) -}}
{{- fail "metrics.logRetentionDays must be one of: 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653" -}}
{{- end -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookAuth.enabled (not (or .Values.adapter.alerting.webhookAuth.sharedSecret .Values.adapter.alerting.webhookAuth.sharedSecretRef.name)) -}}
{{- fail "adapter.alerting.webhookAuth requires either sharedSecret or sharedSecretRef.name when enabled" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookRoute.enabled (not .Values.adapter.alerting.webhookAuth.enabled) -}}
{{- fail "adapter.alerting.webhookRoute requires adapter.alerting.webhookAuth.enabled=true so the public webhook is not exposed without header auth" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookRoute.enabled (not .Values.adapter.alerting.webhookRoute.parentRef.name) -}}
{{- fail "adapter.alerting.webhookRoute.parentRef.name is required when webhookRoute is enabled" -}}
{{- end -}}
{{- if and .Values.adapter.enabled .Values.adapter.networkPolicy.enabled -}}
{{- if empty .Values.adapter.networkPolicy.observerNamespaceLabels -}}
{{- fail "adapter.networkPolicy.observerNamespaceLabels must not be empty when networkPolicy is enabled" -}}
{{- end -}}
{{- if empty .Values.adapter.networkPolicy.observerPodLabels -}}
{{- fail "adapter.networkPolicy.observerPodLabels must not be empty when networkPolicy is enabled" -}}
{{- end -}}
{{- end -}}
{{- end -}}
