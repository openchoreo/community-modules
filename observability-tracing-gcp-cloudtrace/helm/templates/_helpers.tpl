{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Fail fast on missing required values. Called once from templates/validate.yaml.
*/}}
{{- define "tracing-gcp-cloudtrace.validate" -}}

{{- if .Values.adapter.enabled -}}
{{- if not .Values.gcp.projectId -}}
{{- fail "gcp.projectId is required. Example: --set gcp.projectId=my-project" -}}
{{- end -}}
{{- end -}}

{{- end -}}
