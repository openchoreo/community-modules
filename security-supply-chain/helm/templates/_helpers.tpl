{{- define "image-security.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "image-security.labels" -}}
app.kubernetes.io/name: {{ include "image-security.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "image-security.serverName" -}}
{{- printf "%s-trivy-server" (include "image-security.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "image-security.serverURL" -}}
{{- printf "http://%s.%s.svc:%d" (include "image-security.serverName" .) .Release.Namespace (int .Values.server.port) -}}
{{- end -}}

{{/* Environment for every cosign container. */}}
{{- define "image-security.cosignEnv" -}}
- name: DOCKER_CONFIG
  value: /etc/cosign/docker
{{- with .Values.signing.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- range .Values.signing.secretEnv }}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ .secretName }}
      key: {{ .key }}
      # A missing Secret must fail the step, not leave the pod pending.
      optional: true
{{- end }}
{{- end -}}

{{/* Common cosign flags. */}}
{{- define "image-security.cosignFlags" -}}
- --key
- {{ required "signing.key is required when signing.enabled is true" .Values.signing.key | quote }}
- --tlog-upload={{ .Values.signing.tlogUpload }}
- --yes
{{- range .Values.signing.extraArgs }}
- {{ . | quote }}
{{- end }}
{{- end -}}

{{/* Volume giving cosign the registry push credentials as config.json. */}}
{{- define "image-security.registryVolume" -}}
- name: registry-push-secret
  secret:
    secretName: '{{ printf "{{workflow.parameters.%s}}" .Values.workflow.registryPushSecretParameter }}'
    optional: true
    items:
      - key: .dockerconfigjson
        path: config.json
{{- end -}}
