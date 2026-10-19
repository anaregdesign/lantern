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

{{- define "lantern.validateSecurity" -}}
{{- if not (has .Values.auth.mode (list "off" "oidc")) -}}{{ fail "auth.mode must be off or oidc" }}{{- end -}}
{{- if or (hasKey .Values.replication "discovery") (hasKey .Values.replication "peers") (hasKey .Values "peerTLS") -}}{{ fail "legacy peer discovery/TLS values are retired; configure peerPlane" }}{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) (not .Values.peerPlane.enabled) -}}{{ fail "multiple replicas require signed private peerPlane membership" }}{{- end -}}
{{- if and .Values.metrics.expose (eq .Values.auth.mode "oidc") -}}{{ fail "OIDC diagnostics must remain loopback; supply an operator-authenticated sidecar" }}{{- end -}}
{{- if and (or .Values.metrics.serviceMonitor.enabled .Values.metrics.podMonitoring.enabled) (not .Values.metrics.expose) -}}{{ fail "scrape resources require explicit metrics.expose in OFF mode" }}{{- end -}}
{{- if or .Values.publicTLS.existingSecret .Values.peerPlane.enabled (eq .Values.auth.mode "oidc") -}}
{{- if or (le (int (include "lantern.workloadUID" .)) 0) (le (int .Values.podSecurityContext.fsGroup) 0) (not .Values.podSecurityContext.runAsNonRoot) -}}{{ fail "workload Secret provisioning requires a non-root UID and fsGroup" }}{{- end -}}
{{- if or .Values.securityContext.allowPrivilegeEscalation .Values.securityContext.privileged -}}{{ fail "workload Secret provisioning requires an unprivileged container" }}{{- end -}}
{{- end -}}
{{- if or (hasKey .Values.auth "nodeRole") (hasKey .Values.auth "writerFiles") (hasKey .Values.auth "profile") -}}{{ fail "retired auth writer/profile selectors are unsupported" }}{{- end -}}
{{- if eq .Values.auth.mode "oidc" -}}
{{- if not .Values.auth.files -}}{{ fail "OIDC requires an explicit per-workload auth.files allowlist" }}{{- end -}}
{{- if gt (len .Values.auth.files) 32 -}}{{ fail "auth.files exceeds the bounded per-workload file set" }}{{- end -}}
{{- $seen := dict -}}
{{- range $file := .Values.auth.files -}}
{{- if or (not (regexMatch "^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$" $file)) (eq $file "..") (hasKey $seen $file) -}}{{ fail "auth file names must be unique simple basenames" }}{{- end -}}
{{- if has $file (list "writer.key" "writer.pub" "machines.json" "operator.key") -}}{{ fail "retired writer/bootstrap and operator signing keys cannot be workload auth files" }}{{- end -}}
{{- $_ := set $seen $file true -}}
{{- end -}}
{{- if not (has .Values.auth.nodeFile .Values.auth.files) -}}{{ fail "auth.nodeFile must be explicitly included in auth.files" }}{{- end -}}
{{- if not (has .Values.auth.storeMode (list "fresh" "resume")) -}}{{ fail "auth.storeMode must be fresh or resume" }}{{- end -}}
{{- if not .Values.auth.configMap -}}{{ fail "OIDC requires auth.configMap" }}{{- end -}}
{{- if not .Values.auth.existingSecret -}}{{ fail "OIDC requires auth.existingSecret" }}{{- end -}}
{{- if not .Values.publicTLS.existingSecret -}}{{ fail "OIDC requires publicTLS.existingSecret" }}{{- end -}}
{{- end -}}
{{- if .Values.peerPlane.enabled -}}
{{- range $name := list "deployment" "workloadIDPrefix" "membershipConfigMap" "operatorKeySecret" "identitySecret" -}}
{{- if not (index $.Values.peerPlane $name) -}}{{ fail (printf "peerPlane.%s is required" $name) }}{{- end -}}
{{- end -}}
{{- if eq (int .Values.peerPlane.port) (int .Values.service.port) -}}{{ fail "private peer and public ports must differ" }}{{- end -}}
{{- end -}}
{{- if and (eq .Values.auth.mode "off") (or .Values.auth.configMap .Values.auth.existingSecret) -}}{{ fail "OFF must not ignore OIDC security configuration" }}{{- end -}}
{{- if not .Values.peerPlane.enabled -}}
{{- if or .Values.peerPlane.deployment .Values.peerPlane.workloadIDPrefix .Values.peerPlane.membershipConfigMap .Values.peerPlane.operatorKeySecret .Values.peerPlane.identitySecret -}}{{ fail "disabled peerPlane must not ignore operator material" }}{{- end -}}
{{- end -}}
{{- if and .Values.admin.enabled (eq .Values.auth.mode "oidc") -}}
{{- if not (hasPrefix "https://" .Values.admin.server.upstream) -}}{{ fail "OIDC Admin requires a pinned HTTPS authority upstream" }}{{- end -}}
{{- if and .Values.admin.ingress.enabled (not .Values.admin.ingress.tls) -}}{{ fail "OIDC Admin ingress requires TLS" }}{{- end -}}
{{- end -}}
{{- end -}}

{{- define "lantern.adminServerUpstream" -}}
{{- if .Values.admin.server.upstream -}}
{{- .Values.admin.server.upstream -}}
{{- else -}}
{{- printf "h2c://%s:%v" (include "lantern.fullname" .) .Values.service.port -}}
{{- end -}}
{{- end -}}

{{/* Both init and Server inherit the same effective non-root UID. */}}
{{- define "lantern.workloadUID" -}}
{{- if hasKey .Values.securityContext "runAsUser" -}}
{{- .Values.securityContext.runAsUser -}}
{{- else -}}
{{- .Values.podSecurityContext.runAsUser -}}
{{- end -}}
{{- end -}}

{{- define "lantern.provisionWorkloadFiles" -}}
set -eu
umask 077
[ "$(id -u)" = "$EXPECTED_UID" ] && [ "$EXPECTED_UID" != 0 ]
case "$POD_NAME" in ''|*[!a-z0-9-]*) exit 1;; esac
rm -rf /run/lantern-private/public /run/lantern-private/security /run/lantern-private/peers /run/lantern-private/operator
copy_file() {
  source="$1"; destination="$2"
  [ -f "$source" ] && [ -s "$source" ]
  [ "$(wc -c < "$source")" -le 4456448 ]
  rm -f "$destination.tmp"
  cp -L "$source" "$destination.tmp"
  chmod 600 "$destination.tmp"
  mv -f "$destination.tmp" "$destination"
}
{{- if .Values.publicTLS.existingSecret }}
mkdir -p /run/lantern-private/public
chmod 700 /run/lantern-private/public
copy_file "/run/lantern-source/public/$POD_NAME.crt" "/run/lantern-private/public/$POD_NAME.crt"
copy_file "/run/lantern-source/public/$POD_NAME.key" "/run/lantern-private/public/$POD_NAME.key"
{{- end }}
{{- if eq .Values.auth.mode "oidc" }}
mkdir -p /run/lantern-private/security
chmod 700 /run/lantern-private/security
{{- range $file := .Values.auth.files }}
copy_file "/run/lantern-source/security/$POD_NAME.{{ $file }}" {{ printf "/run/lantern-private/security/%s" $file | quote }}
{{- end }}
{{- end }}
{{- if .Values.peerPlane.enabled }}
mkdir -p /run/lantern-private/peers /run/lantern-private/operator
chmod 700 /run/lantern-private/peers /run/lantern-private/operator
copy_file "/run/lantern-source/peers/$POD_NAME.crt" "/run/lantern-private/peers/$POD_NAME.crt"
copy_file "/run/lantern-source/peers/$POD_NAME.key" "/run/lantern-private/peers/$POD_NAME.key"
copy_file /run/lantern-source/peers/ca.pem /run/lantern-private/peers/ca.pem
copy_file /run/lantern-source/operator/operator.pub /run/lantern-private/operator/operator.pub
{{- end }}
{{- end -}}
