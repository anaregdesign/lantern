#!/usr/bin/env bash
# One opt-in, non-performance cohort. Ordinary run.sh owns all cleanup.
run_local_time_mechanism() {
  [[ "${LANTERN_BENCH_LOCAL_TIME_MECHANISM:-0}" == 1 &&
     "$SCENARIO_NAME" == receipt_admission_lookup && "$receipt_driver" == 1 &&
     "$retained_heap" == 0 && "${KEEP_UP:-0}" == 0 && "${SKIP_UP:-0}" == 0 ]] || return 1
  local service original replacement elapsed=0 code config_before config_after version volumes_before volumes_after raw
  local result="${LANTERN_BENCH_LOCAL_TIME_RESULT:-}"
  [[ "$result" == /* && ! -e "$result" && ! -L "$result" ]] || return 1
  local_time_capture pre || return 1
  # Actual authenticated Connect JSON over verified HTTP/2, with no mutation/load.
  for index in 0 1 2; do
    version="$(curl --silent --show-error --fail --noproxy '*' --http2 \
      --proto '=https' --max-redirs 0 --max-time 10 \
      --cacert "$PEER_TLS_DIR/ca.pem" -H 'Content-Type: application/json' \
      -H 'Connect-Protocol-Version: 1' -H "Authorization: Bearer $LANTERN_BENCH_AUTH_TOKEN" \
      --data '{}' -o "$OUTDIR/local_time_capability_$index.json" -w '%{http_version}' \
      "https://localhost:${REPLICA_GRPC_PORTS[$index]}/graph.v1.LanternService/GetReceiptCapability")" || return 1
    [[ "$version" == 2 || "$version" == 2.0 ]] || return 1
    jq -e '.enabled==true' "$OUTDIR/local_time_capability_$index.json" >/dev/null || return 1
  done
  service=lantern-0
  original="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$service")" || return 1
  [[ "$original" =~ ^[0-9a-f]{64}$ ]] || return 1
  raw="$(docker inspect --format '{{json .}}' "$original")" || return 1
  local_time_assert_route <<<"$raw" || return 1
  volumes_before="$(jq -ce '[.Mounts[]|select(.Type=="volume")|{Name,Destination,RW}]|sort_by(.Destination)' <<<"$raw")" || return 1
  docker stop --time 45 "$original" >/dev/null || return 1
  docker inspect --format '{{json .State}}' "$original" |
    jq -e '.Running==false and .ExitCode==0 and .Status=="exited"' >/dev/null || return 1
  config_before="$(local_time_mechanism_config_digest)" || return 1
  "$AUTHFIXTURE" -compose -restart-node "$service" -directory "$PEER_TLS_DIR" || return 1
  config_after="$(local_time_mechanism_config_digest)" || return 1
  [[ "$config_before" == "$config_after" ]] || return 1
  # An anchored peer may legitimately retain sound holdover. Only the new
  # process below is unanchored: it resumes existing state, never initializes it.
  docker stop --time 10 "$LOCAL_TIME_FIXTURE_ID" >/dev/null || return 1
  docker inspect --format '{{json .State}}' "$LOCAL_TIME_FIXTURE_ID" |
    jq -e '.Running==false and .ExitCode==0 and .Status=="exited"' >/dev/null || return 1
  # Compose create has no --no-deps option. Prove the selected service has no
  # dependency edges, then recreate only it without building or pulling.
  docker compose "${COMPOSE_FILES[@]}" config --format json |
    jq -e --arg service "$service" '.services[$service] | type=="object" and
      ((.depends_on // {})|length)==0 and ((.links // [])|length)==0 and
      ((.volumes_from // [])|length)==0' >/dev/null || return 1
  docker compose "${COMPOSE_FILES[@]}" create --force-recreate --no-build --pull never "$service" >/dev/null || return 1
  replacement="$(docker compose "${COMPOSE_FILES[@]}" ps -aq "$service")" || return 1
  [[ "$replacement" =~ ^[0-9a-f]{64}$ && "$replacement" != "$original" ]] || return 1
  raw="$(docker inspect --format '{{json .}}' "$replacement")" || return 1
  local_time_assert_route <<<"$raw" || return 1
  jq -e '.Config.Entrypoint==["/app/lantern"] and (.Config.Cmd==null or .Config.Cmd==[]) and .State.Status=="created"' <<<"$raw" >/dev/null || return 1
  volumes_after="$(jq -ce '[.Mounts[]|select(.Type=="volume")|{Name,Destination,RW}]|sort_by(.Destination)' <<<"$raw")" || return 1
  [[ "$volumes_before" == "$volumes_after" ]] || return 1
  local_time_safe_inspect serving <<<"$raw" | local_time_write_new "$OUTDIR/local_time_cold_created.json" || return 1
  docker start "$replacement" >/dev/null || return 1
  # Keep production's 45s anchoring wait and 3s exchange/backoff unchanged.
  local started=$SECONDS running
  while (( SECONDS - started < 75 )); do
    running="$(docker inspect --format '{{.State.Running}}' "$replacement")" || return 1
    [[ "$running" == true ]] || break
    sleep 1
  done
  [[ "$running" == false ]] || return 1
  elapsed=$((SECONDS - started))
  code="$(docker wait "$replacement")" || return 1
  [[ "$code" == 1 ]] || return 1
  raw="$(docker inspect --format '{{json .}}' "$replacement")" || return 1
  local_time_assert_route <<<"$raw" || return 1
  local_time_safe_inspect serving <<<"$raw" | local_time_write_new "$OUTDIR/local_time_cold_terminal.json" || return 1
  local_time_network | local_time_write_new "$OUTDIR/local_time_loss_network.json" || return 1
  (umask 077; docker logs "$replacement" > "$OUTDIR/local_time_unanchored.log" 2>&1) || return 1
  python3 - "$OUTDIR" "$result" "$LOCAL_TIME_FIXTURE_IP" "$original" "$replacement" "$elapsed" "$config_before" <<'PY'
import hashlib, json, os, pathlib, sys
root, destination, ip, original, replacement, elapsed, config = sys.argv[1:]
root = pathlib.Path(root)
log = root / 'local_time_unanchored.log'
lines = log.read_text().splitlines()
events = []
for line in lines:
    try: events.append(json.loads(line))
    except ValueError: pass
snapshots = [e for e in events if e.get('msg') == 'authority_time_startup']
if len(snapshots) != 1:
    raise SystemExit('missing unanchored native-time refusal')
state = snapshots[0]['state']
attempts = state['attempts'][:state['count']]
if (state['in_flight'] or state['count'] not in (1, 2)
        or not all(a['stage'] == 'read' for a in attempts)
        or not any('->' + ip + ':123' in line for line in lines)):
    raise SystemExit('source-loss refusal was not the pinned local UDP path')
def ref(path):
    return {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
report = {'schema': 1, 'protocol': 'bench_local_udp_mechanism_v1',
          'source_kind': 'controlled-local-udp', 'qualification': False,
          'verdict': 'pass', 'initialized_members': 3, 'ready_members': 3,
          'authenticated_http2_members': 3, 'private_peer_preflight': True,
          'unanchored_resume_refused': True, 'external_fallback': False,
          'holdover_semantics_changed': False, 'initialization_repeated': False,
          'original_container': original, 'resume_container': replacement,
          'security_json_sha256': config, 'loss_elapsed_seconds': int(elapsed),
          'route_profile': ref(root / 'time_source_pre.json'), 'refusal_log': ref(log),
          'cold_created': ref(root / 'local_time_cold_created.json'),
          'cold_terminal': ref(root / 'local_time_cold_terminal.json'),
          'loss_network': ref(root / 'local_time_loss_network.json'),
          'existing_volumes_preserved': True,
          'capabilities': [ref(root / ('local_time_capability_' + str(i) + '.json')) for i in range(3)]}
body = (json.dumps(report, sort_keys=True, indent=2) + '\n').encode()
target = pathlib.Path(destination)
if not target.is_absolute() or target.parent.resolve() != target.parent:
    raise SystemExit('physical absolute new mechanism result required')
for path in (root / 'local_time_mechanism.json', target):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(body); stream.flush(); os.fsync(stream.fileno())
PY
}

local_time_mechanism_config_digest() {
  python3 - "$PEER_TLS_DIR/lantern-0.env" <<'PY'
import hashlib, pathlib, sys
rows = [r for r in pathlib.Path(sys.argv[1]).read_bytes().splitlines()
        if r.startswith(b'LANTERN_SECURITY_CONFIG_JSON=')]
if len(rows) != 1 or rows[0] == b'LANTERN_SECURITY_CONFIG_JSON=':
    raise SystemExit('one current security JSON required')
print(hashlib.sha256(rows[0]).hexdigest())
PY
}
