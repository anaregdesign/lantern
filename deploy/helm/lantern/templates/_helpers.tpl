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
{{- $oidc := eq .Values.auth.mode "oidc" -}}
{{- if not (has .Values.auth.mode (list "off" "oidc")) -}}{{ fail "auth.mode must be off or oidc" }}{{- end -}}
{{- if or .Values.peerPlane.enabled $oidc -}}
{{- if not .Values.nativeTime.configMap -}}{{ fail "private peers and OIDC require nativeTime.configMap with ntp.conf" }}{{- end -}}
{{- else if .Values.nativeTime.configMap -}}{{ fail "standalone OFF must not ignore nativeTime.configMap" }}{{- end -}}
{{- if or (hasKey .Values.replication "discovery") (hasKey .Values.replication "peers") (hasKey .Values "peerTLS") -}}{{ fail "legacy peer discovery/TLS values are retired; configure peerPlane" }}{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) (not (or .Values.peerPlane.enabled $oidc)) -}}{{ fail "multiple replicas require signed private membership" }}{{- end -}}
{{- if and .Values.metrics.expose $oidc -}}{{ fail "OIDC diagnostics must remain loopback; supply an operator-authenticated sidecar" }}{{- end -}}
{{- if and (or .Values.metrics.serviceMonitor.enabled .Values.metrics.podMonitoring.enabled) (not .Values.metrics.expose) -}}{{ fail "scrape resources require explicit metrics.expose in OFF mode" }}{{- end -}}
{{- if or .Values.publicTLS.existingSecret .Values.peerPlane.enabled $oidc -}}
{{- if or (le (int (include "lantern.workloadUID" .)) 0) (le (int .Values.podSecurityContext.fsGroup) 0) (not .Values.podSecurityContext.runAsNonRoot) -}}{{ fail "workload Secret provisioning requires a non-root UID and fsGroup" }}{{- end -}}
{{- if or .Values.securityContext.allowPrivilegeEscalation .Values.securityContext.privileged -}}{{ fail "workload Secret provisioning requires an unprivileged container" }}{{- end -}}
{{- end -}}
{{- range $name := list "nodeRole" "writerFiles" "profile" "storeMode" "nodeFile" "configMap" -}}
{{- if hasKey $.Values.auth $name -}}{{ fail (printf "retired auth.%s setting is unsupported" $name) }}{{- end -}}
{{- end -}}
{{- if $oidc -}}
{{- if or (lt (int .Values.replicaCount) 1) (gt (int .Values.replicaCount) 31) -}}{{ fail "OIDC requires 1 through 31 fixed replicas" }}{{- end -}}
{{- if not .Values.auth.configSecret -}}{{ fail "OIDC requires auth.configSecret with per-Pod supplied JSON" }}{{- end -}}
{{- if not .Values.auth.existingSecret -}}{{ fail "OIDC requires auth.existingSecret for identity material" }}{{- end -}}
{{- if .Values.publicTLS.existingSecret -}}{{ fail "OIDC public TLS comes from supplied JSON, not publicTLS.existingSecret" }}{{- end -}}
{{- if or .Values.peerPlane.enabled .Values.peerPlane.deployment .Values.peerPlane.workloadIDPrefix .Values.peerPlane.membershipConfigMap .Values.peerPlane.operatorKeySecret .Values.peerPlane.identitySecret -}}{{ fail "OIDC derives private peers from identity material; generic OFF peer inputs must be absent" }}{{- end -}}
{{- if ne .Values.peerPlane.mode "resume" -}}{{ fail "OIDC cannot select generic OFF peerPlane.mode" }}{{- end -}}
{{- $required := list "identity.json" "COMPLETE" "workload.pem" "workload-key.pem" "roots.pem" "data-operator.pem" "membership/control.json" "membership/data.json" -}}
{{- if ne (len .Values.auth.files) (len $required) -}}{{ fail "auth.files must contain exactly the eight per-workload identity paths" }}{{- end -}}
{{- $seen := dict -}}
{{- range $file := .Values.auth.files -}}
{{- if or (not (has $file $required)) (hasKey $seen $file) -}}{{ fail "auth.files must contain unique known identity paths only" }}{{- end -}}
{{- $_ := set $seen $file true -}}
{{- end -}}
{{- if or (lt (int .Values.auth.controlPort) 1) (gt (int .Values.auth.controlPort) 65535) (eq (int .Values.auth.controlPort) (int .Values.service.port)) (eq (int .Values.auth.controlPort) (int .Values.peerPlane.port)) -}}{{ fail "OIDC control port must be valid and distinct from public/data ports" }}{{- end -}}
{{- range $env := .Values.extraEnv -}}
{{- if or (regexMatch "^LANTERN_(OIDC_|SECURITY_|PEER_|CDC_|GRAPH_SNAPSHOT_SPOOL_)" $env.name) (has $env.name (list "LANTERN_AUTH_MODE" "LANTERN_PEERS" "LANTERN_TLS_CERT_FILE" "LANTERN_TLS_KEY_FILE" "LANTERN_TLS_CLIENT_CA_FILE")) -}}{{ fail "OIDC extraEnv cannot override its supplied configuration/material inputs" }}{{- end -}}
{{- end -}}
{{- else if or .Values.auth.configSecret .Values.auth.existingSecret .Values.auth.files -}}{{ fail "OFF must not ignore OIDC security inputs" }}{{- end -}}
{{- if or .Values.peerPlane.enabled $oidc -}}
{{- if or (lt (int .Values.peerPlane.port) 1) (gt (int .Values.peerPlane.port) 65535) (eq (int .Values.peerPlane.port) (int .Values.service.port)) -}}{{ fail "private data peer and public ports must be valid and distinct" }}{{- end -}}
{{- end -}}
{{- if .Values.peerPlane.enabled -}}
{{- range $name := list "deployment" "workloadIDPrefix" "membershipConfigMap" "operatorKeySecret" "identitySecret" -}}
{{- if not (index $.Values.peerPlane $name) -}}{{ fail (printf "peerPlane.%s is required" $name) }}{{- end -}}
{{- end -}}
{{- else if not $oidc -}}
{{- if or .Values.peerPlane.deployment .Values.peerPlane.workloadIDPrefix .Values.peerPlane.membershipConfigMap .Values.peerPlane.operatorKeySecret .Values.peerPlane.identitySecret -}}{{ fail "disabled peerPlane must not ignore operator material" }}{{- end -}}
{{- end -}}
{{- if and .Values.admin.enabled $oidc -}}
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
rm -rf /run/lantern-private/public /run/lantern-private/security /run/lantern-private/peers /run/lantern-private/operator /run/lantern-private/input
copy_file() {
  source="$1"; destination="$2"; limit="${3:-4456448}"
  [ -f "$source" ] && [ -s "$source" ]
  [ "$(wc -c < "$source")" -le "$limit" ]
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
mkdir -p /run/lantern-private/input /run/lantern-private/security/nodes/"$POD_NAME"/membership
chmod 700 /run/lantern-private/input /run/lantern-private/security /run/lantern-private/security/nodes /run/lantern-private/security/nodes/"$POD_NAME" /run/lantern-private/security/nodes/"$POD_NAME"/membership
copy_file "/run/lantern-source/input/$POD_NAME.json" /run/lantern-private/input/config.json 122880
{{- range $file := .Values.auth.files }}
copy_file "/run/lantern-source/security/$POD_NAME.{{ replace "/" "." $file }}" "/run/lantern-private/security/nodes/$POD_NAME/{{ $file }}"
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
