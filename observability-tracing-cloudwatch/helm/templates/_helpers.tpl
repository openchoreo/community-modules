{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "tracing-cloudwatch.clusterName" -}}
{{- .Values.clusterName -}}
{{- end -}}

{{- define "tracing-cloudwatch.region" -}}
{{- .Values.region -}}
{{- end -}}

{{- define "tracing-cloudwatch.validate" -}}
{{- if not (include "tracing-cloudwatch.clusterName" .) -}}
{{- fail "clusterName is required. Example: --set clusterName=openchoreo-dev" -}}
{{- end -}}
{{- if not (include "tracing-cloudwatch.region" .) -}}
{{- fail "region is required. Example: --set region=us-east-1" -}}
{{- end -}}
{{- end -}}
