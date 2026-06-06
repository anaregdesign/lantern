{{/*
MCP sidecar partial. lantern-mcp is stdio-only (it implements the
Model Context Protocol over stdin/stdout, not a network listener), so
the canonical way to run it on Kubernetes is as a **sidecar inside the
agent runtime's pod** rather than as a free-standing Service.

This file exports a single named template, `lantern.mcpSidecar`, that
emits a container spec callers can splice into their own Pod template
via `{{ include "lantern.mcpSidecar" . | nindent 8 }}`. The chart
deliberately does NOT render a Pod / Deployment for lantern-mcp on its
own — see `mcp/examples/` for the canonical agent-runtime configs and
the issue thread on #391 for the rationale.

The container is configured via `.Values.mcp`:

  - mcp.image.{repository,tag,pullPolicy}
  - mcp.lanternAddr   (defaults to in-cluster ClusterIP Service)
  - mcp.pingTimeout
  - mcp.ttl           (a map[bucket]duration; each non-empty entry is
                       emitted as LANTERN_MCP_TTL_<BUCKET>)
  - mcp.resources
  - mcp.extraEnv      (raw env list appended after the templated ones)

Example consumer (in your own chart / manifest):

  containers:
    - name: agent
      image: my-agent:latest
      stdin: true
      tty: false
    {{- include "lantern.mcpSidecar" . | nindent 4 }}

The agent talks to lantern-mcp over stdio inside the pod
(`kubectl exec -it ... -c lantern-mcp` reproduces the same channel
for manual probes).
*/}}
{{- define "lantern.mcpSidecar" -}}
- name: lantern-mcp
  image: "{{ .Values.mcp.image.repository }}:{{ .Values.mcp.image.tag | default .Chart.AppVersion }}"
  imagePullPolicy: {{ .Values.mcp.image.pullPolicy }}
  stdin: true
  tty: false
  env:
    - name: LANTERN_ADDR
      value: {{ default (printf "http://%s.%s.svc.cluster.local:%v" (include "lantern.fullname" .) .Release.Namespace .Values.service.port) .Values.mcp.lanternAddr | quote }}
    - name: LANTERN_MCP_PING_TIMEOUT
      value: {{ .Values.mcp.pingTimeout | quote }}
    {{- range $bucket, $ttl := .Values.mcp.ttl }}
    {{- if $ttl }}
    - name: {{ printf "LANTERN_MCP_TTL_%s" (upper $bucket) }}
      value: {{ $ttl | quote }}
    {{- end }}
    {{- end }}
    {{- with .Values.mcp.extraEnv }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  resources:
    {{- toYaml .Values.mcp.resources | nindent 4 }}
{{- end -}}
