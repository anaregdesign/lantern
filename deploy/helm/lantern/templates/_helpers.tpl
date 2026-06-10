{{/*
Expand the name of the chart.
*/}}
{{- define "lantern.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. Truncated at 63 chars.
*/}}
{{- define "lantern.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Headless service name (used for peer discovery).
*/}}
{{- define "lantern.headlessName" -}}
{{- if .Values.service.headlessName -}}
{{- .Values.service.headlessName -}}
{{- else -}}
{{- printf "%s-headless" (include "lantern.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
FQDN for the headless service in-cluster.
*/}}
{{- define "lantern.headlessFQDN" -}}
{{- printf "%s.%s.svc.cluster.local" (include "lantern.headlessName" .) .Release.Namespace -}}
{{- end -}}

{{/*
The DNS name the peer pump should resolve. Falls back to the headless
FQDN when replication.discovery.dnsName is empty.
*/}}
{{- define "lantern.discoveryDNSName" -}}
{{- if .Values.replication.discovery.dnsName -}}
{{- .Values.replication.discovery.dnsName -}}
{{- else -}}
{{- include "lantern.headlessFQDN" . -}}
{{- end -}}
{{- end -}}

{{/*
Chart label.
*/}}
{{- define "lantern.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "lantern.labels" -}}
helm.sh/chart: {{ include "lantern.chart" . }}
{{ include "lantern.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels (stable; used by Service + StatefulSet).
*/}}
{{- define "lantern.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lantern.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Service account name.
*/}}
{{- define "lantern.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "lantern.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Admin-scoped helpers (gated by .Values.admin.enabled). The admin
Deployment / Service / Ingress share the lantern.* common labels but
need a distinct fullname and selector so they don't collide with the
server StatefulSet selector.
*/}}
{{- define "lantern.adminFullname" -}}
{{- printf "%s-admin" (include "lantern.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "lantern.adminSelectorLabels" -}}
app.kubernetes.io/name: {{ include "lantern.name" . }}-admin
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: admin
{{- end -}}

{{- define "lantern.adminLabels" -}}
helm.sh/chart: {{ include "lantern.chart" . }}
{{ include "lantern.adminSelectorLabels" . }}
app.kubernetes.io/version: {{ .Values.admin.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "lantern.name" . }}
{{- end -}}

{{/*
MCP-scoped helpers (gated by .Values.mcp.enabled). lantern-mcp serves
the Model Context Protocol over Streamable HTTP, so it ships as its own
Deployment + Service (mirroring the admin pattern) with a distinct
fullname / selector that does not collide with the server StatefulSet.
*/}}
{{- define "lantern.mcpFullname" -}}
{{- printf "%s-mcp" (include "lantern.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "lantern.mcpSelectorLabels" -}}
app.kubernetes.io/name: {{ include "lantern.name" . }}-mcp
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: mcp
{{- end -}}

{{- define "lantern.mcpLabels" -}}
helm.sh/chart: {{ include "lantern.chart" . }}
{{ include "lantern.mcpSelectorLabels" . }}
app.kubernetes.io/version: {{ .Values.mcp.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "lantern.name" . }}
{{- end -}}
