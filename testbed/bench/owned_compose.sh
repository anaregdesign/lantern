#!/usr/bin/env bash
# Absence must be proven, including stopped workloads. A Docker lookup failure
# is not evidence that a fresh project or its persistent volumes are unused.
owned_compose_project_absent() {
  local project="$1"
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" ]] || return 1
  local resources
  resources="$(docker ps -aq --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$resources" ]] || return 1
  resources="$(docker volume ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$resources" ]] || return 1
  resources="$(docker network ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$resources" ]]
}

# Remove only the explicitly named benchmark project, including optional
# diagnostic services. This is teardown for an owned run, never fresh startup.
down_owned_compose() {
  local project="$1"
  shift
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" ]] || return 1
  docker compose "$@" --profile off-diagnostics down -v --remove-orphans >/dev/null || return 1
  owned_compose_project_absent "$project"
}

# Normalize the complete owned directory set. Receipt cohorts have six
# state/backup volumes; graph cohorts add one dedicated no-copy spool per node.
# A partial spool overlay or an unknown volume never enters initialization.
owned_compose_volume_layout() {
  local project="$1" owner="$2"
  jq -ec --arg project "$project" --arg owner "$owner" '
    select(.name==$project) | . as $configuration |
    [range(0;3) | "lantern-\(.)" as $service |
      $configuration.services[$service] | select(.user==$owner) |
      [.volumes[] | select(.type=="volume" or .target=="/state" or .target=="/data" or
        .target=="/var/lib/lantern-snapshot-spool")] as $mounts |
      select(($mounts | map(.target) | sort)==["/data","/state"] or
        ($mounts | map(.target) | sort)==["/data","/state","/var/lib/lantern-snapshot-spool"]) |
      select(all($mounts[]; .type=="volume" and .read_only!=true and
        (.target!="/var/lib/lantern-snapshot-spool" or .volume.nocopy==true))) |
      {service:$service, mounts:[$mounts[] | . as $mount |
        $configuration.volumes[$mount.source] |
        select(.external!=true and (.name|type)=="string" and
          (.driver==null or .driver=="local") and (.driver_opts==null or .driver_opts=={})) |
        {source:$mount.source,target:$mount.target,volume:.name}]} |
      select((.mounts | map(.target) | sort)==($mounts | map(.target) | sort))] as $services |
    select(($services|length)==3 and
      ([$services[].mounts|length] | unique | length)==1) |
    [$services[].mounts[]] as $mounts |
    select(($mounts|length)==6 or ($mounts|length)==9) |
    select(($mounts|map(.volume)|unique|length)==($mounts|length) and
      ($configuration.volumes|keys|sort)==($mounts|map(.source)|sort)) |
    {count:($mounts|length),services:$services}
  '
}

# Called only after the named project was proven absent. Refuse an existing
# workload, foreign/shared volume or any contents before changing ownership.
# Only the exact six or nine newly-created directories enter the initializer.
initialize_owned_compose_volumes() {
  local project="$1" image="$2" owner="$3"
  shift 3
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" && "$image" =~ ^sha256:[0-9a-f]{64}$ && "$owner" =~ ^[1-9][0-9]*:[0-9]+$ ]] || return 1
  owned_compose_project_absent "$project" || return 1
  local configuration layout expected expected_count existing name
  configuration="$(docker compose "$@" config --format json)" || return 1
  layout="$(owned_compose_volume_layout "$project" "$owner" <<<"$configuration")" || return 1
  expected="$(jq -er '.services[].mounts[].volume' <<<"$layout")" || return 1
  expected_count="$(jq -er '.count' <<<"$layout")" || return 1
  existing="$(docker volume ls --format '{{.Name}}')" || return 1
  for name in $expected; do
    [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]+$ && $'\n'"$existing"$'\n' != *$'\n'"$name"$'\n'* ]] || return 1
  done
  docker compose "$@" create lantern-0 lantern-1 lantern-2 >/dev/null || return 1
  local service container identity volumes volume label wanted seen="|" index=0 expected_mounts='[]' volume_runtime_owner="$owner"
  local -a mounts=()
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "$@" ps -aq "$service")" || return 1
    [[ "$container" =~ ^[0-9a-f]{12,64}$ ]] || return 1
    identity="$(docker inspect --format '{{json .}}' "$container")" || return 1
    wanted="$(jq -ec --arg service "$service" '.services[] | select(.service==$service) | .mounts | map({target,volume}) | sort_by(.target)' <<<"$layout")" || return 1
    volumes="$(jq -er --arg project "$project" --arg image "$image" --arg owner "$owner" --arg service "$service" --argjson wanted "$wanted" '
      select(.Config.Labels["com.docker.compose.project"]==$project and
        .Config.Labels["com.docker.compose.service"]==$service and
        .Config.User==$owner and .Image==$image and .State.Status=="created") |
      [.Mounts[] | select(.Type=="volume" or .Destination=="/state" or .Destination=="/data" or
        .Destination=="/var/lib/lantern-snapshot-spool")] |
      select(all(.[]; .Type=="volume" and .RW==true)) |
      select((map({target:.Destination,volume:.Name}) | sort_by(.target))==$wanted) |
      sort_by(.Destination)[] | .Name
    ' <<<"$identity")" || return 1
    for volume in $volumes; do
      [[ "$volume" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]+$ && "$seen" != *"|$volume|"* ]] || return 1
      [[ $'\n'"$expected"$'\n' == *$'\n'"$volume"$'\n'* ]] || return 1
      label="$(docker volume inspect --format '{{index .Labels "com.docker.compose.project"}}' "$volume")" || return 1
      [[ "$label" == "$project" ]] || return 1
      seen+="$volume|"
      mounts+=(--mount "type=volume,source=$volume,target=/owned/$index,volume-nocopy")
      expected_mounts="$(jq -c --arg volume "$volume" --arg target "/owned/$index" '.+[{Name:$volume,Destination:$target,RW:true,Type:"volume"}]' <<<"$expected_mounts")" || return 1
      index=$((index + 1))
    done
  done
  [[ "$index" == "$expected_count" ]] || return 1
  # Image copy-up preserves private /state ownership and mode 0700. Permit
  # read/search for the complete emptiness check before any ownership change;
  # DAC_READ_SEARCH does not bypass write permissions like DAC_OVERRIDE.
  run_owned_compose_initializer "$project" "$image" 0:0 compose-volume-initializer volume-init 300 validate_owned_compose_volume_initializer \
    --network=none --read-only \
    --cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_READ_SEARCH --pids-limit=64 --memory=64m \
    --entrypoint /bin/sh \
    "${mounts[@]}" "$image" -c '
      set -eu
      case "$2" in 6|9) ;; *) exit 2 ;; esac
      index=0
      while test "$index" -lt "$2"; do
        directory="/owned/$index"
        test ! -L "$directory" && test -d "$directory" || exit 2
        # The marker preserves trailing newlines, including a newline-only
        # filename. An unsuccessful listing must never prove emptiness.
        contents="$(ls -A "$directory" && printf x)" || exit 2
        test "$contents" = x || exit 2
        index=$((index + 1))
      done
      index=0
      while test "$index" -lt "$2"; do
        directory="/owned/$index"
        chown 0:0 "$directory"
        chmod 700 "$directory"
        chown "$1" "$directory"
        index=$((index + 1))
      done
    ' fixture "$owner" "$expected_count"
}

# Both preparers below create a stopped, identity-checked daemon object before
# any write starts. The same bounded supervisor owns normal exit and failure.
run_owned_compose_initializer() {
  local project="$1" image="$2" owner="$3" kind="$4" service="$5" start_rounds="$6" validator="$7"
  shift 7
  local supervisor="" interrupted=0 status previous_int previous_term
  previous_int="$(trap -p INT)"; previous_term="$(trap -p TERM)"
  trap 'interrupted=130; [[ -z "$supervisor" ]] || kill -TERM "$supervisor" 2>/dev/null || true' INT
  trap 'interrupted=143; [[ -z "$supervisor" ]] || kill -TERM "$supervisor" 2>/dev/null || true' TERM
  (
    local private="" nonce="" initializer="${project}-${service}" initializer_id="" command_pid="" initialized="" baseline="" initializer_message_fd
    local -a environment_args=()
    umask 077
    exec {initializer_message_fd}>&2
    initializer_command() {
      local round result=0 limit="${initializer_command_rounds:-300}"
      "$@" & command_pid=$!
      for ((round=0; round<limit; round++)); do
        kill -0 "$command_pid" 2>/dev/null || break
        sleep 0.1
      done
      if kill -0 "$command_pid" 2>/dev/null; then
        kill -TERM "$command_pid" 2>/dev/null || true
        for round in {1..20}; do
          kill -0 "$command_pid" 2>/dev/null || break
          sleep 0.1
        done
        kill -KILL "$command_pid" 2>/dev/null || true
        result=124
      fi
      wait "$command_pid" || result=$?
      command_pid=""
      return "$result"
    }
    initializer_identity() {
      jq -e --arg project "$project" --arg service "${service%-security-init}" --arg image "$image" --arg owner "$owner" --arg name "$initializer" --arg kind "$kind" --arg nonce "$nonce" '
        (.Id|test("^[0-9a-f]{64}$")) and .Name==("/"+$name) and
        .Config.Labels["com.docker.compose.project"]==$project and
        .Config.Labels["com.docker.compose.service"]==$service and
        .Config.Labels["com.docker.compose.oneoff"]=="True" and
        .Config.Labels["lantern.owner"]==$kind and .Config.Labels["lantern.initializer"]==$nonce and
        .Config.User==$owner and .Image==$image
      ' <<<"$initialized" >/dev/null
    }
    initializer_fingerprint() {
      jq -Sc '{Id,Name,Image,Config,HostConfig,Mounts:(.Mounts|sort_by(.Destination))}' <<<"$initialized"
    }
    initializer_matches() {
      initializer_identity && [[ -n "$baseline" && "$(initializer_fingerprint)" == "$baseline" ]]
    }
    finish_initializer() {
      local exit_status=$? target
      trap - EXIT; trap '' INT TERM
      if [[ -n "$command_pid" ]]; then
        kill -TERM "$command_pid" 2>/dev/null || true
        kill -KILL "$command_pid" 2>/dev/null || true
        wait "$command_pid" 2>/dev/null || true
        command_pid=""
      fi
      if [[ -n "$private" && -n "$initializer" ]]; then
        target="${initializer_id:-$initializer}"
        if initializer_command docker inspect --format '{{json .}}' "$target" > "$private/terminal-inspect.json"; then
          initialized="$(cat "$private/terminal-inspect.json")"
          if initializer_matches; then
            if jq -e '.State.Running==true' <<<"$initialized" >/dev/null; then
              initializer_command docker stop --time 10 "$initializer_id" > "$private/stop.log" 2>&1 || exit_status=1
            fi
            if ! initializer_command docker inspect --format '{{json .}}' "$initializer_id" > "$private/terminal-inspect.json"; then
              exit_status=1
            else
              initialized="$(cat "$private/terminal-inspect.json")"
              if initializer_matches && jq -e '.State.Running==true' <<<"$initialized" >/dev/null; then
                initializer_command docker kill --signal KILL "$initializer_id" > "$private/kill.log" 2>&1 || exit_status=1
                if initializer_command docker inspect --format '{{json .}}' "$initializer_id" > "$private/terminal-inspect.json"; then
                  initialized="$(cat "$private/terminal-inspect.json")"
                else
                  exit_status=1
                fi
              fi
              if ! initializer_matches || ! jq -e '.State.Running==false and (.State.Status=="exited" or .State.Status=="created")' <<<"$initialized" >/dev/null; then
                echo 'initializer terminal inactivity could not be verified' >&"$initializer_message_fd"
                exit_status=1
              elif jq -e '.State.Status=="exited"' <<<"$initialized" >/dev/null; then
                initializer_command docker wait "$initializer_id" > "$private/wait.log" 2>&1 || exit_status=1
              fi
            fi
            initializer_command docker logs "$initializer_id" > "$private/initializer.log" 2>&1 || exit_status=1
          else
            # An unsuccessful create cannot run native code. An identity or
            # fingerprint mismatch never authorizes stopping a foreign object.
            echo 'initializer identity unverified; container retained without signaling' >&"$initializer_message_fd"
            exit_status=1
          fi
        elif [[ -n "$initializer_id" ]]; then
          echo 'initializer terminal state could not be verified' >&"$initializer_message_fd"
          exit_status=1
        fi
      fi
      if [[ -n "$private" ]]; then
        if (( exit_status == 0 )); then rm -r -- "$private" || exit_status=1
        else echo "initializer private evidence retained at $private" >&"$initializer_message_fd"; fi
      fi
      exit "$exit_status"
    }
    trap finish_initializer EXIT
    trap 'exit 130' INT; trap 'exit 143' TERM
    [[ "$project" == "${COMPOSE_PROJECT_NAME:-}" && "$image" =~ ^sha256:[0-9a-f]{64}$ ]] || exit 1
    private="$(mktemp -d "${TMPDIR:-/tmp}/lantern-owned-init.XXXXXXXX")" || exit 1
    nonce="${private##*/}"
    if [[ "$kind" == compose-security-initializer ]]; then
      jq -er '.Config.Env | if length>0 and all(.[]; type=="string" and ((contains("\r") or contains("\n") or contains("\u0000"))|not)) then .[] else error("invalid initializer environment") end' <<<"$original" > "$private/environment" || exit 1
      environment_args=(--env-file "$private/environment")
    fi
    initializer_command docker create --pull never --name "$initializer" --user "$owner" \
      --label "com.docker.compose.project=$project" --label "com.docker.compose.service=${service%-security-init}" \
      --label com.docker.compose.oneoff=True --label "lantern.owner=$kind" --label "lantern.initializer=$nonce" \
      "${environment_args[@]}" "$@" > "$private/created-id" || exit 1
    initializer_id="$(cat "$private/created-id")"
    [[ "$initializer_id" =~ ^[0-9a-f]{64}$ ]] || exit 1
    initializer_command docker inspect --format '{{json .}}' "$initializer_id" > "$private/created.json" || exit 1
    initialized="$(cat "$private/created.json")"
    initializer_identity && "$validator" && jq -e '.State.Status=="created" and .State.Running==false' <<<"$initialized" >/dev/null || exit 1
    baseline="$(initializer_fingerprint)" || exit 1
    initializer_command_rounds="$start_rounds" initializer_command docker start --attach "$initializer_id" > "$private/start.log" 2>&1 || exit 1
    initializer_command docker inspect --format '{{json .}}' "$initializer_id" > "$private/completed.json" || exit 1
    initialized="$(cat "$private/completed.json")"
    initializer_matches && jq -e '.State.Status=="exited" and .State.Running==false and .State.ExitCode==0' <<<"$initialized" >/dev/null || exit 1
    initializer_command docker wait "$initializer_id" > "$private/wait.log" 2>&1 || exit 1
    initializer_command docker rm "$initializer_id" > "$private/remove.log" 2>&1 || exit 1
    initializer=""
  ) &
  supervisor=$!
  if (( interrupted != 0 )); then kill -TERM "$supervisor" 2>/dev/null || true; fi
  while :; do
    if wait "$supervisor"; then status=0; else status=$?; fi
    kill -0 "$supervisor" 2>/dev/null || break
  done
  trap - INT TERM
  [[ -z "$previous_int" ]] || eval "$previous_int"
  [[ -z "$previous_term" ]] || eval "$previous_term"
  if (( interrupted != 0 )); then return "$interrupted"; fi
  return "$status"
}

validate_owned_compose_volume_initializer() {
  jq -e --argjson expected "$expected_mounts" --arg runtime_owner "$volume_runtime_owner" --arg count "$expected_count" '
    .Config.Entrypoint==["/bin/sh"] and (.Config.Cmd|length)==5 and .Config.Cmd[0]=="-c" and
    .Config.Cmd[2]=="fixture" and .Config.Cmd[3]==$runtime_owner and .Config.Cmd[4]==$count and
    .HostConfig.NetworkMode=="none" and .HostConfig.ReadonlyRootfs==true and
    (.HostConfig.CapDrop|sort)==["ALL"] and (.HostConfig.CapAdd|sort)==["CHOWN","DAC_READ_SEARCH"] and
    (.Mounts|map({Name,Destination,RW,Type})|sort_by(.Destination))==($expected|sort_by(.Destination))
  ' <<<"$initialized" >/dev/null
}

validate_owned_compose_security_initializer() {
  printf '%s\n%s\n' "$original" "$initialized" |
    jq -se '.[0] as $original | .[1] |
      .Config.Entrypoint==["/app/lantern"] and .Config.Cmd==["init-security"] and
      .Config.WorkingDir==$original.Config.WorkingDir and .HostConfig.NetworkMode==$original.HostConfig.NetworkMode and
      (.Config.Env|sort)==($original.Config.Env|sort) and
      (.Mounts|sort_by(.Destination))==($original.Mounts|sort_by(.Destination))' >/dev/null
}

# Every service is verified before any native initialization. Each clone uses
# the same current JSON, nonroot UID, private network and exact read-only mounts.
initialize_owned_compose_security() {
  local project="$1" image="$2" owner="$3"
  shift 3
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" && "$image" =~ ^sha256:[0-9a-f]{64}$ && "$owner" =~ ^[1-9][0-9]*:[0-9]+$ ]] || return 1
  local configuration layout service container original network index=0
  local -a originals=() containers=()
  configuration="$(LANTERN_IMAGE="$image" docker compose "$@" config --format json)" || return 1
  layout="$(owned_compose_volume_layout "$project" "$owner" <<<"$configuration")" || return 1
  [[ "$(jq -er '.count' <<<"$layout")" == 6 ]] || return 1
  jq -e --arg image "$image" '
    [.services|to_entries[]|select(.key|test("^lantern-[012]$"))|.value] |
    length==3 and all(.[]; .image==$image and .environment.LANTERN_AUTH_MODE=="oidc" and
      (.environment.LANTERN_SECURITY_CONFIG_JSON|type)=="string" and (.environment.LANTERN_SECURITY_CONFIG_JSON|length)>0)
  ' <<<"$configuration" >/dev/null || return 1
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "$@" ps -aq "$service")" || return 1
    [[ "$container" =~ ^[0-9a-f]{12,64}$ ]] || return 1
    original="$(docker inspect --format '{{json .}}' "$container")" || return 1
    printf '%s\n%s\n' "$original" "$configuration" |
      jq -se --arg project "$project" --arg service "$service" --arg image "$image" --arg owner "$owner" '
        .[0] as $original | .[1].services[$service].environment as $settings |
        $original.Config.Labels["com.docker.compose.project"]==$project and $original.Config.Labels["com.docker.compose.service"]==$service and
        $original.Config.User==$owner and $original.Image==$image and $original.State.Status=="created" and
        all($settings|to_entries[]|select(.value!=null); . as $entry | [$original.Config.Env[]|select(.==($entry.key+"="+$entry.value))]|length==1)
      ' >/dev/null || return 1
    originals+=("$original"); containers+=("$container")
  done
  for service in lantern-0 lantern-1 lantern-2; do
    original="${originals[$index]}"; container="${containers[$index]}"
    network="$(jq -er '.HostConfig.NetworkMode|select(type=="string" and length>0)' <<<"$original")" || return 1
    run_owned_compose_initializer "$project" "$image" "$owner" compose-security-initializer "${service}-security-init" 900 validate_owned_compose_security_initializer \
      --network "$network" --volumes-from "$container" --entrypoint /app/lantern "$image" init-security || return 1
    index=$((index + 1))
  done
}
