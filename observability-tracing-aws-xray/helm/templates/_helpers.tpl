{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "tracing-aws-xray.region" -}}
{{- .Values.region -}}
{{- end -}}

{{- define "tracing-aws-xray.validate" -}}
{{- if not (include "tracing-aws-xray.region" .) -}}
{{- fail "region is required. Example: --set region=us-east-1" -}}
{{- end -}}
{{- end -}}
