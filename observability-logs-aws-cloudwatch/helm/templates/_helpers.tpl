{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Render the log group path prefix.
*/}}
{{- define "logs-aws-cloudwatch.logGroupPrefix" -}}
{{- trimSuffix "/" .Values.global.logGroupPrefix -}}
{{- end -}}

{{/*
AWS region sourced from the upstream subchart values block.
*/}}
{{- define "logs-aws-cloudwatch.region" -}}
{{- (index .Values "amazon-cloudwatch-observability").region -}}
{{- end -}}

{{/*
Resolved application log group name for the adapter and setup Job.
*/}}
{{- define "logs-aws-cloudwatch.logGroupName" -}}
{{- if .Values.global.applicationLogGroupName -}}
{{- .Values.global.applicationLogGroupName -}}
{{- else -}}
{{- printf "%s/application" (include "logs-aws-cloudwatch.logGroupPrefix" .) -}}
{{- end -}}
{{- end -}}

{{/*
Log group where the observability-events-otel-collector ships enriched Kubernetes
events. Mirrors the application log group under the shared prefix.
*/}}
{{- define "logs-aws-cloudwatch.eventsLogGroupName" -}}
{{- if .Values.events.logGroupName -}}
{{- .Values.events.logGroupName -}}
{{- else -}}
{{- printf "%s/events" (include "logs-aws-cloudwatch.logGroupPrefix" .) -}}
{{- end -}}
{{- end -}}

{{/*
Validate required values and fail fast with a readable message.
Called once from templates/validate.yaml.
*/}}
{{- define "logs-aws-cloudwatch.validate" -}}

{{/* --- region: always required when any component is active --- */}}
{{- if or .Values.adapter.enabled .Values.cloudWatchAgent.enabled .Values.setup.enabled .Values.awsCredentials.create -}}
{{- if not (include "logs-aws-cloudwatch.region" .) -}}
{{- fail "amazon-cloudwatch-observability.region is required. Example: --set amazon-cloudwatch-observability.region=us-east-1" -}}
{{- end -}}
{{- if not .Values.global.logGroupPrefix -}}
{{- fail "global.logGroupPrefix is required. Example: --set global.logGroupPrefix=/aws/containerinsights" -}}
{{- end -}}
{{- end -}}

{{/* --- adapter-scoped validations --- */}}
{{- if .Values.adapter.enabled -}}

{{- if and .Values.adapter.alerting.webhookAuth.enabled (not (or .Values.adapter.alerting.webhookAuth.sharedSecret .Values.adapter.alerting.webhookAuth.sharedSecretRef.name)) -}}
{{- fail "adapter.alerting.webhookAuth requires either sharedSecret or sharedSecretRef.name when enabled" -}}
{{- end -}}

{{- if .Values.adapter.alerting.enabled -}}
{{- range $field := list "alarmActionArns" "okActionArns" "insufficientDataActionArns" -}}
{{- $arns := index $.Values.adapter.alerting $field -}}
{{- if gt (len $arns) 5 -}}
{{- fail (printf "adapter.alerting.%s has %d entries; CloudWatch alarms allow at most 5 actions per state" $field (len $arns)) -}}
{{- end -}}
{{- range $i, $arn := $arns -}}
{{- if not (hasPrefix "arn:aws:" $arn) -}}
{{- fail (printf "adapter.alerting.%s[%d]=%q is not a valid AWS ARN; expected prefix \"arn:aws:\"" $field $i $arn) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- if and .Values.adapter.alerting.webhookRoute.enabled (not .Values.adapter.alerting.webhookAuth.enabled) -}}
{{- fail "adapter.alerting.webhookRoute requires adapter.alerting.webhookAuth.enabled=true so the public webhook is not exposed without header auth" -}}
{{- end -}}

{{- if and .Values.adapter.alerting.webhookRoute.enabled (not .Values.adapter.alerting.webhookRoute.parentRef.name) -}}
{{- fail "adapter.alerting.webhookRoute.parentRef.name is required when webhookRoute is enabled" -}}
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

{{/* --- retention validation: only when setup is active --- */}}
{{- if .Values.setup.enabled -}}
{{- $validRetentions := list 1 3 5 7 14 30 60 90 120 150 180 365 400 545 731 1096 1827 2192 2557 2922 3288 3653 -}}
{{- if not (has (.Values.containerLogs.retentionDays | int) $validRetentions) -}}
{{- fail (printf "containerLogs.retentionDays=%v is not a valid CloudWatch retention. Allowed values: 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653" .Values.containerLogs.retentionDays) -}}
{{- end -}}
{{- end -}}

{{/* --- static credentials validation --- */}}
{{- if .Values.awsCredentials.create -}}
{{- if not .Values.awsCredentials.name -}}
{{- fail "awsCredentials.name is required when awsCredentials.create=true (e.g. --set awsCredentials.name=cloudwatch-aws-credentials)" -}}
{{- end -}}
{{- if not .Values.awsCredentials.accessKeyId -}}
{{- fail "awsCredentials.accessKeyId is required when awsCredentials.create=true" -}}
{{- end -}}
{{- if not .Values.awsCredentials.secretAccessKey -}}
{{- fail "awsCredentials.secretAccessKey is required when awsCredentials.create=true" -}}
{{- end -}}
{{- end -}}

{{- end -}}

{{- define "logs-aws-cloudwatch.webhookSecretName" -}}
{{- if .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
logs-adapter-aws-cloudwatch-webhook-token
{{- end -}}
{{- end -}}
