{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Log ingest base URL: dynatrace.ingestUrl, or dynatrace.platformUrl with ".apps." replaced
by ".live.", which is where SaaS serves the classic API.
*/}}
{{- define "logs-dynatrace.ingestUrl" -}}
{{- if .Values.dynatrace.ingestUrl -}}
{{- trimSuffix "/" .Values.dynatrace.ingestUrl -}}
{{- else if .Values.dynatrace.platformUrl -}}
{{- trimSuffix "/" (replace ".apps." ".live." .Values.dynatrace.platformUrl) -}}
{{- end -}}
{{- end -}}

{{/*
The ingest URL split into the pieces Fluent Bit's http output takes separately, as a
JSON dict: host, port, tls, basePath. Port defaults from the scheme.
*/}}
{{- define "logs-dynatrace.ingestEndpoint" -}}
{{- $raw := include "logs-dynatrace.ingestUrl" . -}}
{{- $u := urlParse $raw -}}
{{- if not (has $u.scheme (list "http" "https")) -}}
{{- fail (printf "dynatrace.ingestUrl %q must start with http:// or https://" $raw) -}}
{{- end -}}
{{- $tls := eq $u.scheme "https" -}}
{{- $hostPort := splitList ":" $u.host -}}
{{- $host := first $hostPort -}}
{{- $port := ternary "443" "80" $tls -}}
{{- if eq (len $hostPort) 2 -}}
{{- $port = last $hostPort -}}
{{- end -}}
{{- dict "host" $host "port" $port "tls" $tls "basePath" (trimSuffix "/" $u.path) | toJson -}}
{{- end -}}

{{/*
Validate required values and fail fast with a readable message.
*/}}
{{- define "logs-dynatrace.validate" -}}
{{- if .Values.adapter.enabled -}}
{{- if not .Values.dynatrace.platformUrl -}}
{{- fail "dynatrace.platformUrl is required when adapter.enabled=true. Example: --set dynatrace.platformUrl=https://abc12345.apps.dynatrace.com" -}}
{{- end -}}
{{- if not (has .Values.adapter.authMode (list "platformToken" "oauth")) -}}
{{- fail (printf "adapter.authMode must be platformToken or oauth, got %q" .Values.adapter.authMode) -}}
{{- end -}}
{{- end -}}
{{- if (index .Values "fluent-bit").enabled -}}
{{- if not (include "logs-dynatrace.ingestUrl" .) -}}
{{- fail "dynatrace.ingestUrl (or dynatrace.platformUrl) is required when fluent-bit.enabled=true" -}}
{{- end -}}
{{- if not .Values.fluentBitCustomizations.clusterInstance -}}
{{- fail "fluentBitCustomizations.clusterInstance is required. It names the cluster these records came from and cannot be defaulted." -}}
{{- end -}}
{{- if not (regexMatch "^[A-Za-z0-9._-]+$" .Values.fluentBitCustomizations.clusterInstance) -}}
{{- fail (printf "fluentBitCustomizations.clusterInstance %q may contain only letters, digits, '.', '_' and '-'" .Values.fluentBitCustomizations.clusterInstance) -}}
{{- end -}}
{{- if not (has .Values.dynatrace.ingest.authScheme (list "Api-Token" "Bearer")) -}}
{{- fail (printf "dynatrace.ingest.authScheme must be Api-Token or Bearer, got %q" .Values.dynatrace.ingest.authScheme) -}}
{{- end -}}
{{- end -}}
{{- range $k := list "containerLogsSource" "auditLogsSource" -}}
{{- if not (regexMatch "^[A-Za-z0-9._-]+$" (index $.Values.dynatrace $k)) -}}
{{- fail (printf "dynatrace.%s may contain only letters, digits, '.', '_' and '-'" $k) -}}
{{- end -}}
{{- end -}}
{{- if eq .Values.dynatrace.containerLogsSource .Values.dynatrace.auditLogsSource -}}
{{- fail "dynatrace.containerLogsSource and dynatrace.auditLogsSource must differ" -}}
{{- end -}}
{{- if .Values.dynatrace.credentials.create -}}
{{- if and (index .Values "fluent-bit").enabled (not .Values.dynatrace.credentials.ingestToken) -}}
{{- fail "dynatrace.credentials.ingestToken is required when dynatrace.credentials.create=true and fluent-bit.enabled=true" -}}
{{- end -}}
{{- if and .Values.adapter.enabled (eq .Values.adapter.authMode "platformToken") (not .Values.dynatrace.credentials.platformToken) -}}
{{- fail "dynatrace.credentials.platformToken is required when dynatrace.credentials.create=true and adapter.authMode=platformToken" -}}
{{- end -}}
{{- if and .Values.adapter.enabled (eq .Values.adapter.authMode "oauth") (not (and .Values.dynatrace.credentials.oauthClientId .Values.dynatrace.credentials.oauthClientSecret)) -}}
{{- fail "dynatrace.credentials.oauthClientId and oauthClientSecret are required when dynatrace.credentials.create=true and adapter.authMode=oauth" -}}
{{- end -}}
{{- end -}}
{{- end -}}
