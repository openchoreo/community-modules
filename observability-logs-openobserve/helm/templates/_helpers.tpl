{{/*
Copyright 2026 The OpenChoreo Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
Return common.openObserveAuditStream. OpenObserve rewrites other characters to "_" on
ingest, which would leave the adapter and setup job reading a different stream.

Usage: {{ include "observability-logs-openobserve.auditStream" . }}
*/}}
{{- define "observability-logs-openobserve.auditStream" -}}
{{- $stream := .Values.common.openObserveAuditStream -}}
{{- if not (regexMatch "^[a-z0-9_]+$" ($stream | toString)) -}}
{{- fail (printf "common.openObserveAuditStream %q must contain only lowercase letters, digits and '_'" $stream) -}}
{{- end -}}
{{- $stream -}}
{{- end }}

{{/*
Optional TLS settings for a Fluent Bit http output, rendered only when TLS is on.
Takes a dict with tls, caFile, verifyHostname and vhost.
*/}}
{{- define "observability-logs-openobserve.fluentBitTlsOptions" -}}
{{- if .tls }}
{{- with .caFile }}
        tls.ca_file {{ . }}
{{- end }}
{{- if .verifyHostname }}
        tls.verify_hostname On
{{- end }}
{{- with .vhost }}
        tls.vhost {{ . }}
{{- end }}
{{- end }}
{{- end }}
