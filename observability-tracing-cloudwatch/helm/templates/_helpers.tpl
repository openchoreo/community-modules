{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "tracing-cloudwatch.instanceName" -}}
{{- .Values.instanceName -}}
{{- end -}}

{{- define "tracing-cloudwatch.region" -}}
{{- .Values.region -}}
{{- end -}}

{{- define "tracing-cloudwatch.validate" -}}
{{- if not (include "tracing-cloudwatch.instanceName" .) -}}
{{- fail "instanceName is required. Example: --set instanceName=openchoreo-dev" -}}
{{- end -}}
{{- if not (include "tracing-cloudwatch.region" .) -}}
{{- fail "region is required. Example: --set region=us-east-1" -}}
{{- end -}}
{{- end -}}
