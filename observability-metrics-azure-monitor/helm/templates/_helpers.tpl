{{- define "metrics-azure-monitor.serviceAccountName" -}}
{{- $sa := .Values.adapter.serviceAccount | default dict -}}
{{- $create := true -}}
{{- if hasKey $sa "create" -}}{{- $create = get $sa "create" -}}{{- end -}}
{{- $name := get $sa "name" | default "" -}}
{{- if $create -}}
{{- default "metrics-adapter-azure-monitor" $name -}}
{{- else -}}
{{- required "adapter.serviceAccount.name is required when adapter.serviceAccount.create=false" $name -}}
{{- end -}}
{{- end -}}

{{- define "metrics-azure-monitor.webhookSecretName" -}}
{{- if .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- .Values.adapter.alerting.webhookAuth.sharedSecretRef.name -}}
{{- else -}}
metrics-adapter-azure-monitor-webhook-token
{{- end -}}
{{- end -}}

{{- define "metrics-azure-monitor.validate" -}}
{{- if .Values.adapter.enabled -}}
{{- $_ := required "region is required. Example: --set region=eastus2" .Values.region -}}
{{- $_ := required "workspace.id is required (Log Analytics workspace customerId GUID)" .Values.workspace.id -}}
{{- $_ := required "workspace.resourceId is required (ARM resource ID of the workspace)" .Values.workspace.resourceId -}}
{{- $_ := required "azure.subscriptionId is required" .Values.azure.subscriptionId -}}
{{- $_ := required "azure.resourceGroup is required" .Values.azure.resourceGroup -}}
{{- $_ := required "adapter.alerting.actionGroupId is required" .Values.adapter.alerting.actionGroupId -}}
{{- $_ := required "adapter.alerting.observerUrl is required" .Values.adapter.alerting.observerUrl -}}
{{- $sa := .Values.adapter.serviceAccount | default dict -}}
{{- $create := true -}}
{{- if hasKey $sa "create" -}}{{- $create = get $sa "create" -}}{{- end -}}
{{- if and $create (not $sa.clientId) -}}
{{- fail "adapter.serviceAccount.clientId is required when create=true (the UAMI azure.workload.identity/client-id for the federated credential)" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookAuth.enabled (not (or .Values.adapter.alerting.webhookAuth.sharedSecret .Values.adapter.alerting.webhookAuth.sharedSecretRef.name)) -}}
{{- fail "adapter.alerting.webhookAuth requires either sharedSecret or sharedSecretRef.name when enabled" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookAuth.enabled .Values.adapter.alerting.webhookAuth.sharedSecret (lt (len .Values.adapter.alerting.webhookAuth.sharedSecret) 16) -}}
{{- fail "adapter.alerting.webhookAuth.sharedSecret must be at least 16 bytes when webhookAuth.enabled=true" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookRoute.enabled (not .Values.adapter.alerting.webhookAuth.enabled) -}}
{{- fail "adapter.alerting.webhookRoute requires adapter.alerting.webhookAuth.enabled=true so the public webhook is not exposed without header auth" -}}
{{- end -}}
{{- if and .Values.adapter.alerting.webhookRoute.enabled (not .Values.adapter.alerting.webhookRoute.parentRef.name) -}}
{{- fail "adapter.alerting.webhookRoute.parentRef.name is required when webhookRoute is enabled" -}}
{{- end -}}
{{- end -}}
{{- end -}}
