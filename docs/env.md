# Lantern server environment variables

<!-- GENERATED FILE - do not edit. Regenerate with `make envdoc` (or `go generate ./...`). -->

Every `LANTERN_*` variable the server reads, generated from the envconfig
registry (#847). A set-but-malformed value logs a startup warning and falls
back to its default; an unknown `LANTERN_*` name logs a typo warning; with
`LANTERN_STRICT_CONFIG=true` either condition fails boot. An explicitly empty
value is treated as unset for the non-string kinds. The MCP server's
`LANTERN_MCP_*` namespace belongs to that process and is not listed here.

| Variable | Type | Default | Description |
|---|---|---|---|
| `LANTERN_ANTI_ENTROPY_GAP_WARN_THRESHOLD` | int | `1024` | Origin-seq gap size above which the anti-entropy sweep logs a warning. |
| `LANTERN_ANTI_ENTROPY_INTERVAL_MS` | int | `30000` | Anti-entropy sweep cadence in milliseconds. |
| `LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS` | int | `30000` | Per-peer anti-entropy catch-up subscribe timeout in milliseconds. |
| `LANTERN_AUTH_EXEMPT_REFLECTION` | bool | `true` | Keep gRPC server reflection reachable without a token when auth is enabled (schema discovery is not data access). Set false to require the bearer token for reflection too. |
| `LANTERN_AUTH_TOKENS` | string | (empty) | Comma-separated bearer tokens arming data-plane auth (empty = open, the default). Requests must send 'Authorization: Bearer <token>' matching any entry (constant-time compare); multiple entries allow zero-downtime rotation (add new on all servers -> switch clients -> drop old). Health checks are always exempt. Pair with TLS outside trusted networks - bearer tokens over plaintext h2c are sniffable. |
| `LANTERN_BACKUP_DIR` | string | (empty) | Mounted directory dumps are written to and restored from. |
| `LANTERN_BACKUP_ENABLED` | bool | `false` | Enable the periodic whole-graph dump loop (requires LANTERN_BACKUP_DIR). |
| `LANTERN_BACKUP_INSTANCE_ID` | string | (empty) | Per-instance dump filename token for shared storage; defaults to the hostname. |
| `LANTERN_BACKUP_INTERVAL` | duration | `5m0s` | Dump cadence (Go duration). |
| `LANTERN_BACKUP_RESTORE_ON_START` | bool | `true` | Replay the newest valid dump as a baseline before serving. |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | bool | `false` | Fail boot when restore-on-startup errors instead of starting with current state. |
| `LANTERN_BACKUP_RETAIN` | int | `3` | How many of this instance's own dumps to keep, newest first (0 keeps all). |
| `LANTERN_BLOCK_PROFILE_RATE` | int | `0` | runtime.SetBlockProfileRate in nanoseconds between samples (0 = disabled). |
| `LANTERN_COMMIT` | string | (empty) | Overrides the commit label reported in lantern_build_info. |
| `LANTERN_CORS_ALLOWED_ORIGINS` | string | (empty) | Comma-separated browser origins allowed by CORS (e.g. the admin SPA origin); empty disables CORS. |
| `LANTERN_DEFAULT_TTL_SECONDS` | int | `60` | Reported in GetServerStatus and startup logs only; RPC writes without an expiration are permanent (decay is opt-in per write, #523). |
| `LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT` | uint32 | `10000` | Deletion cap used when DeleteVerticesByPrefix leaves limit unset. |
| `LANTERN_DELETE_BY_PREFIX_MAX_LIMIT` | uint32 | `100000` | Ceiling DeleteVerticesByPrefix's limit is clamped to. |
| `LANTERN_DRAIN_DELAY_SECONDS` | int | `0` | Zero-drop rolling-update window: keep serving this long after readiness flips NOT_SERVING (0 = disabled). |
| `LANTERN_GC_INTERVAL_SECONDS` | int | `60` | Graph-cache GC tick interval in seconds (expired vertex/edge sweep). |
| `LANTERN_ILLUMINATE_MAX_K` | int | `1024` | Upper bound on the Illuminate k parameter. |
| `LANTERN_ILLUMINATE_MAX_STEP` | int | `16` | Upper bound on the Illuminate BFS step parameter. |
| `LANTERN_LLM_API_KEY` | string | (empty) | Secret for LANTERN_LLM_AUTH=api-key; leave empty for the token auth modes. |
| `LANTERN_LLM_AUTH` | string | `api-key` | Credential mode (#826/#854): api-key (default) | azure-managed-identity | azure-client-secret | google-adc | google-service-account. Token modes inject a credentialed HTTP client and run with an empty API key. |
| `LANTERN_LLM_AZURE_CLIENT_ID` | string | (empty) | Entra application (client) id for auth=azure-client-secret. |
| `LANTERN_LLM_AZURE_CLIENT_SECRET` | string | (empty) | Entra client secret for auth=azure-client-secret. |
| `LANTERN_LLM_AZURE_TENANT_ID` | string | (empty) | Entra tenant for auth=azure-client-secret. |
| `LANTERN_LLM_BASE_URL` | string | (empty) | Endpoint override: Azure OpenAI resource URL, OpenAI-compatible gateway, region-pinned Gemini, etc. |
| `LANTERN_LLM_GOOGLE_CREDENTIALS_FILE` | string | (empty) | Service-account key file path for auth=google-service-account (google-adc reads the ambient environment instead). |
| `LANTERN_LLM_MAX_TOKENS` | int | `0` | Max output tokens per generation (0 = provider default). |
| `LANTERN_LLM_MODEL` | string | (empty) | Provider model id (required unless provider=disabled). |
| `LANTERN_LLM_PROVIDER` | string | `disabled` | LLM backend for server-side features (#828): disabled (default) | openai | anthropic | gemini. disabled composes the server without any LLM. |
| `LANTERN_LOG_FORMAT` | string | `json` | Log output format: json or text. |
| `LANTERN_LOG_LEVEL` | level | `info` | Structured-log level: debug, info, warn, or error. |
| `LANTERN_MAX_BATCH_SIZE` | int | `10000` | Maximum items accepted per batch RPC (Put/Get/Add/Delete plural forms). |
| `LANTERN_MAX_CONCURRENT_STREAMS` | uint32 | `1024` | HTTP/2 max concurrent streams per connection (0 = unlimited). |
| `LANTERN_MAX_EDGES` | int | `0` | Soft cap on live edges (0 = unlimited). Local write RPCs that would exceed it fail with RESOURCE_EXHAUSTED; replication apply and backup restore bypass the cap. |
| `LANTERN_MAX_KEY_LEN` | int | `1024` | Maximum accepted vertex-key length in bytes. |
| `LANTERN_MAX_RECV_MSG_BYTES` | int | `16777216` | Maximum accepted request message size in bytes. |
| `LANTERN_MAX_REPLICATION_LAG` | int | `10000` | Readiness gate: maximum tolerated replication lag (entries) before /readyz reports not ready. |
| `LANTERN_MAX_SEND_MSG_BYTES` | int | `16777216` | Maximum produced response message size in bytes. |
| `LANTERN_MAX_VERTICES` | int | `0` | Soft cap on live vertices (0 = unlimited). Local write RPCs that would exceed it fail with RESOURCE_EXHAUSTED; replication apply and backup restore bypass the cap. Conservative pre-check: edge writes count both endpoints as potentially new. |
| `LANTERN_METRICS_ADDR` | string | `:9090` | host:port for the /metrics + /healthz + /readyz HTTP listener (empty disables it). |
| `LANTERN_MUTATION_LOG_CAPACITY` | int | `100000` | Replication mutation-log ring capacity in entries; size for peak_cluster_rps x retention_seconds. |
| `LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER` | int | `512` | Per-subscriber outbound channel depth; a subscriber that falls further behind is gapped. |
| `LANTERN_MUTEX_PROFILE_FRACTION` | int | `0` | runtime.SetMutexProfileFraction sampling rate (0 = disabled; has runtime cost). |
| `LANTERN_NODE_ID` | string | (empty) | Stable 32-hex-char (16-byte) node identity for HLC/replication; random per boot when unset. |
| `LANTERN_PEERS` | string | (empty) | Comma-separated static peer list (host:port) for the replication pump; empty = single instance. |
| `LANTERN_PEER_DEFAULT_PORT` | string | `50051` | Port appended to DNS-discovered peer addresses. |
| `LANTERN_PEER_DISCOVERY` | string | `static` | Peer discovery mode: static or dns. |
| `LANTERN_PEER_DISCOVERY_INTERVAL_MS` | int | `10000` | Peer re-resolution cadence in milliseconds (0 = resolve once at startup). |
| `LANTERN_PEER_DNS_NAME` | string | (empty) | DNS name resolved for peer discovery when LANTERN_PEER_DISCOVERY=dns (e.g. a headless Service). |
| `LANTERN_PORT` | int | `6380` | TCP port of the primary Connect/h2c listener. |
| `LANTERN_PPROF_ENABLED` | bool | `false` | Mount /debug/pprof/* on the metrics listener (keep the listener internal-only). |
| `LANTERN_PUMP_BACKOFF_MAX_MS` | int | `30000` | Reconnect backoff ceiling, in milliseconds. |
| `LANTERN_PUMP_BACKOFF_MIN_MS` | int | `250` | Initial reconnect backoff after a peer session error, in milliseconds. |
| `LANTERN_RATE_LIMIT_BURST` | int | `0` | Token-bucket burst size; when unset or <= 0 it resolves to 2x LANTERN_RATE_LIMIT_RPS. |
| `LANTERN_RATE_LIMIT_RPS` | float | `0` | Process-wide token-bucket refill rate in requests/second (0 disables rate limiting). |
| `LANTERN_REFLECTION` | bool | `true` | Serve gRPC server reflection on the primary listener. |
| `LANTERN_SCAN_DEFAULT_LIMIT` | uint32 | `1000` | Page size used when a Scan* request leaves limit unset. |
| `LANTERN_SCAN_MAX_LIMIT` | uint32 | `10000` | Ceiling a Scan* request's limit is clamped to. |
| `LANTERN_SEARCH_DEFAULT_LIMIT` | uint32 | `100` | Ranked-hit count used when SearchVertices leaves limit unset. |
| `LANTERN_SEARCH_DEFAULT_MIN_SHOULD` | uint32 | `1` | Minimum-should-match count applied when the mode resolves to min-should but the request leaves it 0. |
| `LANTERN_SEARCH_DEFAULT_MODE` | string | `any` | Match mode applied when a SearchVertices request omits it: any (OR), all (AND), or min-should. Validated at startup — an unrecognised value fails boot. |
| `LANTERN_SEARCH_ENABLED` | bool | `true` | Build the full-text search index and serve SearchVertices (off = FAILED_PRECONDITION). |
| `LANTERN_SEARCH_MAX_LIMIT` | uint32 | `1000` | Ceiling SearchVertices' limit is clamped to. |
| `LANTERN_SEARCH_POSITIONS` | bool | `true` | Record positional postings so phrase queries verify adjacency and the proximity boost ranks tight matches higher. Off drops the per-(word term, vertex) position store: phrase degrades to the AND-intersection and the boost goes inert, shrinking the index on large corpora. |
| `LANTERN_SHUTDOWN_TIMEOUT_SECONDS` | int | `30` | Graceful-shutdown drain budget for in-flight requests before a hard close. |
| `LANTERN_SLOW_RPC_THRESHOLD_MS` | int | `500` | RPCs slower than this emit a warn-level "slow rpc" log line (0 disables). |
| `LANTERN_STRICT_CONFIG` | bool | `false` | Refuse to boot when any LANTERN_* value is malformed or an unknown LANTERN_* variable is set. |
| `LANTERN_TLS_CERT_FILE` | string | (empty) | PEM certificate path; setting cert + key enables TLS on the primary listener. |
| `LANTERN_TLS_CLIENT_CA_FILE` | string | (empty) | PEM client-CA bundle path; setting it additionally enables mTLS client verification. |
| `LANTERN_TLS_KEY_FILE` | string | (empty) | PEM private-key path; pairs with LANTERN_TLS_CERT_FILE. |
| `LANTERN_TOMBSTONE_TTL` | duration | `8760h0m0s` | Delete-tombstone retention window and the upper bound on caller-supplied expirations. |
| `LANTERN_TRAVERSAL_MAX_PUSHES` | int | `1000000` | Maximum PPR/PageRank-Nibble forward pushes per Illuminate call; exhaustion returns RESOURCE_EXHAUSTED, never a partial result. |
| `LANTERN_TRAVERSAL_MAX_RESULTS` | int | `1024` | Maximum PPR star members or local-community members returned by Illuminate; wire top_n=0/max_size=0 resolve to this cap. |
| `LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES` | int | `10000000` | Maximum adjacency entries scanned by PPR/PageRank-Nibble per Illuminate call; exhaustion returns RESOURCE_EXHAUSTED. |
| `LANTERN_TRAVERSAL_TIMEOUT_MS` | int | `5000` | Server-side wall-clock budget for Illuminate traversals in milliseconds (default 5000; 0 explicitly disables it); expiry surfaces as DEADLINE_EXCEEDED. |
| `LANTERN_VERSION` | string | (empty) | Overrides the version label reported in lantern_build_info and GetServerStatus. |
