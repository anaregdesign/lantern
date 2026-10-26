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
    local initializer_launching=0 initializer_pending_signal=0
    local -a environment_args=()
    umask 077
    exec {initializer_message_fd}>&2
    initializer_interrupted() {
      # Publish custody before EXIT can inspect command_pid. A signal arriving
      # in this small launch window is honored immediately after registration.
      if (( initializer_launching != 0 )); then
        if (( initializer_pending_signal == 0 )); then initializer_pending_signal="$1"; fi
      else exit "$1"; fi
    }
    initializer_command() {
      local round result=0 limit="${initializer_command_rounds:-300}"
      initializer_launching=1
      # Close only the extra diagnostic descriptor in the asynchronous child;
      # preserve the same command/PID and its existing private stdout/stderr.
      "$@" {initializer_message_fd}>&- &
      command_pid=$!
      initializer_launching=0
      if (( initializer_pending_signal != 0 )); then
        local pending_status="$initializer_pending_signal"
        initializer_pending_signal=0
        exit "$pending_status"
      fi
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
      # Docker may represent the default-enabled OOM killer as false or null.
      # Keep true distinct and retain every other fingerprint field unchanged.
      jq -Sc '
        .HostConfig.OomKillDisable as $oom |
        if $oom==null or ($oom|type)=="boolean" then
          .HostConfig.OomKillDisable=($oom // false)
        else error("invalid initializer OomKillDisable") end |
        {Id,Name,Image,Config,HostConfig,Mounts:(.Mounts|sort_by(.Destination))}
      ' <<<"$initialized"
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
    trap 'initializer_interrupted 130' INT; trap 'initializer_interrupted 143' TERM
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
    if [[ "$kind" == compose-bind-identity-probe ]]; then
      retain_owned_compose_bind_probe "$private" || exit 1
    fi
    initializer_command docker rm "$initializer_id" > "$private/remove.log" 2>&1 || exit 1
    if [[ "$kind" == compose-bind-identity-probe ]]; then
      cp "$private/remove.log" "$bind_proof_directory/probe-remove.log" || exit 1
    fi
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
    (.HostConfig.CapDrop|sort)==["ALL"] and
    (.HostConfig.CapAdd|type)=="array" and
    (.HostConfig.CapAdd|map(ltrimstr("CAP_"))|sort)==["CHOWN","DAC_READ_SEARCH"] and
    (.Mounts|map({Name,Destination,RW,Type})|sort_by(.Destination))==($expected|sort_by(.Destination))
  ' <<<"$initialized" >/dev/null
}

validate_owned_compose_security_initializer() {
  printf '%s\n%s\n' "$original" "$initialized" |
    jq -se '
      def readonly_binds:
        type=="array" and length==2 and
        all(.[]; type=="string" and ((contains("\r") or contains("\n") or contains("\u0000"))|not) and
          (split(":")|length==3 and (.[0]|startswith("/")))) and
        (map(split(":")|.[1:])|sort)==[["/etc/ntp.conf","ro"],["/run/lantern-config","ro"]];
      def nocopy_volumes:
        type=="array" and length==2 and (map(.Target)|sort)==["/data","/state"] and
        all(.[]; type=="object" and (keys|sort)==["Source","Target","Type","VolumeOptions"] and
          .Type=="volume" and (.Source|type)=="string" and (.Source|test("^[A-Za-z0-9][A-Za-z0-9_.-]+$")) and
          .VolumeOptions=={NoCopy:true});
      .[0] as $original | .[1] |
      .Config.Entrypoint==["/app/lantern"] and .Config.Cmd==["init-security"] and
      .Config.WorkingDir==$original.Config.WorkingDir and .HostConfig.NetworkMode==$original.HostConfig.NetworkMode and
      .Config.User==$original.Config.User and .Image==$original.Image and
      .Config.Labels["com.docker.compose.project"]==$original.Config.Labels["com.docker.compose.project"] and
      .Config.Labels["com.docker.compose.service"]==$original.Config.Labels["com.docker.compose.service"] and
      .State.Status=="created" and .State.Running==false and
      $original.State.Status=="created" and $original.State.Running==false and
      ($original.HostConfig.Binds|readonly_binds) and (.HostConfig.Binds|readonly_binds) and
      ($original.HostConfig.Mounts|nocopy_volumes) and (.HostConfig.Mounts|nocopy_volumes) and
      (.HostConfig.Mounts|sort_by(.Target))==($original.HostConfig.Mounts|sort_by(.Target)) and
      (.Config.Env|sort)==($original.Config.Env|sort)' >/dev/null || return 1
  if printf '%s\n%s\n' "$original" "$initialized" |
    jq -se '.[0] as $original | .[1] | (.Mounts|sort_by(.Destination))==($original.Mounts|sort_by(.Destination))' >/dev/null; then
    return 0
  fi
  prove_owned_compose_bind_identity
}

# Only this fixed metadata command may observe a not-yet-proven Source pair.
# It has no native initialization, credentials, network or writable mount.
validate_owned_compose_bind_probe() {
  jq -e --arg image "$image" --argjson plan "$bind_proof_plan" --argjson environment "$bind_probe_environment" --argjson targets "$bind_probe_targets" '
    .Config.Image==$image and
    .Config.Entrypoint==["/bin/busybox"] and
    .Config.Cmd==(["stat","-c","%d:%i:%F"]+$targets) and
    (.Config.Env|sort)==($environment|sort) and
    .HostConfig.NetworkMode=="none" and .HostConfig.ReadonlyRootfs==true and
    .HostConfig.Privileged==false and .HostConfig.AutoRemove==false and
    .HostConfig.CapDrop==["ALL"] and (.HostConfig.CapAdd==null or .HostConfig.CapAdd==[]) and
    .HostConfig.SecurityOpt==["no-new-privileges:true"] and
    .HostConfig.Memory==33554432 and .HostConfig.NanoCpus==250000000 and .HostConfig.PidsLimit==16 and
    (.HostConfig.Binds==null or .HostConfig.Binds==[]) and
    (.HostConfig.Mounts|type)=="array" and (.HostConfig.Mounts|length)==(2*($plan|length)) and
    (.HostConfig.VolumesFrom==null or .HostConfig.VolumesFrom==[]) and
    (.HostConfig.Tmpfs==null or .HostConfig.Tmpfs=={}) and
    (.Config.Volumes==null or .Config.Volumes=={}) and
    (.HostConfig.Devices==null or .HostConfig.Devices==[]) and
    (.HostConfig.DeviceRequests==null or .HostConfig.DeviceRequests==[]) and
    (.HostConfig.DeviceCgroupRules==null or .HostConfig.DeviceCgroupRules==[]) and
    .HostConfig.PidMode=="" and .HostConfig.UTSMode=="" and .HostConfig.UsernsMode=="" and
    .HostConfig.PublishAllPorts==false and (.HostConfig.PortBindings==null or .HostConfig.PortBindings=={}) and
    .HostConfig.RestartPolicy=={Name:"no",MaximumRetryCount:0} and
    (.Mounts|type)=="array" and (.Mounts|length)==(2*($plan|length))
  ' <<<"$initialized" >/dev/null || return 1
  # Each actual target remains tied to its requested pair even when Desktop
  # resolves both requests to the same effective source. No third path is valid.
  jq -e --argjson plan "$bind_proof_plan" '
    . as $probe |
    all(range(0;$plan|length); . as $i | $plan[$i] as $pair |
      all(["original","clone"][]; . as $side | ("/proof/"+$side+"-"+($i|tostring)) as $target |
        [$probe.Mounts[]|select(.Destination==$target)] as $mounts |
        ($mounts|length)==1 and ($mounts[0]|keys|sort)==["Destination","Mode","Propagation","RW","Source","Type"] and
        $mounts[0].Type=="bind" and $mounts[0].RW==false and ($mounts[0].Mode=="" or $mounts[0].Mode=="ro") and $mounts[0].Propagation=="rprivate" and
        ($mounts[0].Source==$pair.original_source or $mounts[0].Source==$pair.clone_source) and
        [$probe.HostConfig.Mounts[]|select(.Target==$target)] as $requests |
        ($requests|length)==1 and ($requests[0]|keys|sort)==["BindOptions","ReadOnly","Source","Target","Type"] and
        $requests[0].Type=="bind" and $requests[0].ReadOnly==true and
        ($requests[0].Source==$pair.original_source or $requests[0].Source==$pair.clone_source) and
        ($requests[0].BindOptions=={Propagation:"rprivate"} or $requests[0].BindOptions=={Propagation:"rprivate",CreateMountpoint:false})))
  ' <<<"$initialized" >/dev/null
}

retain_owned_compose_bind_probe() {
  local probe_private="$1"
  [[ -n "$bind_proof_directory" && -d "$bind_proof_directory" ]] || return 1
  cp "$probe_private/created.json" "$bind_proof_directory/probe-created.json" &&
    cp "$probe_private/completed.json" "$bind_proof_directory/probe-completed.json" &&
    cp "$probe_private/start.log" "$bind_proof_directory/probe-output.log" &&
    cp "$probe_private/wait.log" "$bind_proof_directory/probe-wait.log" || return 1
  jq -Rse --argjson plan "$bind_proof_plan" '
    select(length<=4096) | split("\n") as $raw |
    select($raw[-1]=="" and ($raw|length)==(1+2*($plan|length))) | $raw[0:-1] |
    map(capture("^(?<device>[1-9][0-9]*):(?<inode>[1-9][0-9]*):(?<type>directory|regular file)$")) as $values |
    select(($values|length)==(2*($plan|length))) |
    select(all(range(0;$plan|length); . as $i |
      $values[2*$i]==$values[2*$i+1] and $values[2*$i].type==$plan[$i].expected_type)) |
    {schema:1,identity:"same-effective-daemon-mount-object",values:$values}
  ' "$probe_private/start.log" > "$bind_proof_directory/identity.json"
}

prove_owned_compose_bind_identity() {
  local bind_proof_plan bind_proof_directory bind_probe_environment bind_probe_targets original_id clone_id original_service daemon_before daemon_after original_after clone_after
  local -a proof_mounts=() proof_targets=()
  original_service="$(jq -er '.Config.Labels["com.docker.compose.service"]|select(type=="string" and test("^lantern-[012]$"))' <<<"$original")" || return 1
  bind_proof_plan="$(printf '%s\n%s\n%s\n' "$original" "$initialized" "$configuration" |
    jq -sec --arg service "$original_service" '
      def source: type=="string" and startswith("/") and length>1 and
        ((contains(",") or contains("\""))|not) and (explode|all(.>=32 and .!=127));
      .[0] as $original | .[1] as $clone | .[2].services[$service].volumes as $declared |
      ($original.Mounts|sort_by(.Destination)) as $left | ($clone.Mounts|sort_by(.Destination)) as $right |
      if ($left|type)!="array" or ($right|type)!="array" or ($left|length)!=($right|length) or
        ($left|map(.Destination)|unique|length)!=($left|length) or ($right|map(.Destination)|unique|length)!=($right|length)
      then error("mount membership differs") else . end |
      [range(0;$left|length)|. as $i|$left[$i] as $a|$right[$i] as $c|
        if $a==$c then empty else
          (if $a.Destination=="/run/lantern-config" then "directory" elif $a.Destination=="/etc/ntp.conf" then "regular file" else error("unsupported proof destination") end) as $type |
          [$declared[]|select(.type=="bind" and .read_only==true and .target==$a.Destination)] as $intent |
          if $a.Type!="bind" or $a.RW!=false or ($a|del(.Source))!=($c|del(.Source)) or
            ($a.Source|source|not) or ($c.Source|source|not) or ($intent|length)!=1 or
            ($intent[0].source!=$a.Source and $intent[0].source!=$c.Source)
          then error("unproved bind shape or declared source") else
            {destination:$a.Destination,declared_source:$intent[0].source,original_source:$a.Source,clone_source:$c.Source,expected_type:$type}
          end
        end] | if length>=1 and length<=2 then . else error("unsupported proof pair count") end
    ')" || return 1
  umask 077
  bind_proof_directory="$(mktemp -d "${TMPDIR:-/tmp}/lantern-bind-proof.XXXXXXXX")" || return 1
  printf '%s\n' "$bind_proof_plan" > "$bind_proof_directory/requested-pairs.json" || return 1
  echo "read-only bind proof private evidence retained at $bind_proof_directory" >&2 || return 1
  original_id="$(jq -er '.Id|select(test("^[0-9a-f]{64}$"))' <<<"$original")" || return 1
  clone_id="$(jq -er '.Id|select(test("^[0-9a-f]{64}$"))' <<<"$initialized")" || return 1
  [[ "$original_id" != "$clone_id" ]] || return 1
  initializer_command docker inspect --format '{{json .}}' "$original_id" > "$bind_proof_directory/original-before.json" || return 1
  initializer_command docker inspect --format '{{json .}}' "$clone_id" > "$bind_proof_directory/clone-before.json" || return 1
  printf '%s\n%s\n%s\n%s\n' "$original" "$(cat "$bind_proof_directory/original-before.json")" "$initialized" "$(cat "$bind_proof_directory/clone-before.json")" |
    jq -se 'def fingerprint: {Id,Name,Image,Config,HostConfig,Mounts:(.Mounts|sort_by(.Destination))};
      (.[0]|fingerprint)==(.[1]|fingerprint) and (.[2]|fingerprint)==(.[3]|fingerprint) and
      all(.[]; .State.Status=="created" and .State.Running==false)' >/dev/null || return 1
  initializer_command docker info --format '{{.ID}}' > "$bind_proof_directory/daemon-before" || return 1
  daemon_before="$(cat "$bind_proof_directory/daemon-before")"; [[ -n "$daemon_before" && "$daemon_before" != *$'\n'* ]] || return 1
  initializer_command docker image inspect --format '{{json .Config.Env}}' "$image" > "$bind_proof_directory/image-environment.json" || return 1
  bind_probe_environment="$(cat "$bind_proof_directory/image-environment.json")"
  jq -e 'type=="array" and all(.[]; type=="string" and test("^(PATH|LANTERN_DEFAULT_TTL_SECONDS|LANTERN_PORT|LANTERN_METRICS_ADDR|LANTERN_LOG_FORMAT|LANTERN_LOG_LEVEL|LANTERN_VERSION|LANTERN_COMMIT)=") and ((contains("\r") or contains("\n") or contains("\u0000"))|not)) and
    (map(split("=")[0])|unique|length)==length' <<<"$bind_probe_environment" >/dev/null || return 1
  bind_probe_targets="$(jq -c '[range(0;length)|. as $i|("/proof/original-"+($i|tostring)),("/proof/clone-"+($i|tostring))]' <<<"$bind_proof_plan")" || return 1
  local pair_index=0 source_pair target
  while IFS= read -r source_pair; do
    proof_mounts+=("--mount=type=bind,source=$(jq -er '.original_source' <<<"$source_pair"),target=/proof/original-$pair_index,readonly,bind-propagation=rprivate")
    proof_mounts+=("--mount=type=bind,source=$(jq -er '.clone_source' <<<"$source_pair"),target=/proof/clone-$pair_index,readonly,bind-propagation=rprivate")
    pair_index=$((pair_index+1))
  done < <(jq -c '.[]' <<<"$bind_proof_plan")
  while IFS= read -r target; do proof_targets+=("$target"); done < <(jq -r '.[]' <<<"$bind_probe_targets")
  run_owned_compose_initializer "$project" "$image" "$owner" compose-bind-identity-probe "${original_service}-bind-proof" 100 validate_owned_compose_bind_probe \
    --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true --memory 32m --cpus 0.25 --pids-limit 16 \
    "${proof_mounts[@]}" --entrypoint /bin/busybox "$image" stat -c '%d:%i:%F' "${proof_targets[@]}" || return "$?"
  initializer_command docker inspect --format '{{json .}}' "$original_id" > "$bind_proof_directory/original-after.json" || return 1
  initializer_command docker inspect --format '{{json .}}' "$clone_id" > "$bind_proof_directory/clone-after.json" || return 1
  initializer_command docker info --format '{{.ID}}' > "$bind_proof_directory/daemon-after" || return 1
  daemon_after="$(cat "$bind_proof_directory/daemon-after")"; [[ "$daemon_before" == "$daemon_after" ]] || return 1
  original_after="$(cat "$bind_proof_directory/original-after.json")"; clone_after="$(cat "$bind_proof_directory/clone-after.json")"
  printf '%s\n%s\n%s\n%s\n' "$original" "$original_after" "$initialized" "$clone_after" |
    jq -se 'def fingerprint: {Id,Name,Image,Config,HostConfig,Mounts:(.Mounts|sort_by(.Destination))};
      (.[0]|fingerprint)==(.[1]|fingerprint) and (.[2]|fingerprint)==(.[3]|fingerprint) and
      all(.[]; .State.Status=="created" and .State.Running==false)' >/dev/null || return 1
  local snapshot
  for snapshot in original-before clone-before original-after clone-after; do
    jq -Sc '{Id,Name,Image,Config,HostConfig,Mounts:(.Mounts|sort_by(.Destination))}' "$bind_proof_directory/$snapshot.json" > "$bind_proof_directory/$snapshot.fingerprint.json" || return 1
  done
  jq -n --arg original "$original_id" --arg clone "$clone_id" --arg daemon "$daemon_before" --argjson pairs "$bind_proof_plan" --slurpfile identity "$bind_proof_directory/identity.json" \
    '{schema:1,status:"PASS",identity:"same-effective-daemon-mount-object",original_id:$original,clone_id:$clone,daemon_id:$daemon,pairs:$pairs,physical_identity:$identity[0],
      fingerprints:["original-before.fingerprint.json","clone-before.fingerprint.json","original-after.fingerprint.json","clone-after.fingerprint.json"]}' > "$bind_proof_directory/proof.json"
}

# Every service is verified before any native initialization. Each clone uses
# the same current JSON, nonroot UID, private network and exact read-only mounts.
initialize_owned_compose_security() {
  local project="$1" image="$2" owner="$3"
  shift 3
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" && "$image" =~ ^sha256:[0-9a-f]{64}$ && "$owner" =~ ^[1-9][0-9]*:[0-9]+$ ]] || return 1
  local configuration layout service container original network binding index=0
  local -a originals=() mounts=()
  configuration="$(LANTERN_IMAGE="$image" docker compose "$@" config --format json)" || return 1
  layout="$(owned_compose_volume_layout "$project" "$owner" <<<"$configuration")" || return 1
  [[ "$(jq -er '.count' <<<"$layout")" == 6 ]] || return 1
  jq -e --arg image "$image" '
    [.services|to_entries[]|select(.key|test("^lantern-[012]$"))|.value] |
    length==3 and all(.[]; .image==$image and .environment.LANTERN_AUTH_MODE=="oidc" and
      (.environment.LANTERN_SECURITY_CONFIG_JSON|type)=="string" and (.environment.LANTERN_SECURITY_CONFIG_JSON|length)>0 and
      all(.volumes[]|select(.type=="volume"); .volume.nocopy==true))
  ' <<<"$configuration" >/dev/null || return 1
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "$@" ps -aq "$service")" || return 1
    [[ "$container" =~ ^[0-9a-f]{12,64}$ ]] || return 1
    original="$(docker inspect --format '{{json .}}' "$container")" || return 1
    printf '%s\n%s\n' "$original" "$configuration" |
      jq -se --arg project "$project" --arg service "$service" --arg image "$image" --arg owner "$owner" '
        .[0] as $original | .[1] as $configuration | $configuration.services[$service].environment as $settings |
        [$configuration.services[$service].volumes[]|select(.type=="volume")|. as $mount|
          {Type:"volume",Source:$configuration.volumes[$mount.source].name,Target:$mount.target,VolumeOptions:{NoCopy:true}}] as $requests |
        $original.Config.Labels["com.docker.compose.project"]==$project and $original.Config.Labels["com.docker.compose.service"]==$service and
        $original.Config.User==$owner and $original.Image==$image and $original.State.Status=="created" and
        all($settings|to_entries[]|select(.value!=null); . as $entry | [$original.Config.Env[]|select(.==($entry.key+"="+$entry.value))]|length==1) and
        ($original.HostConfig.Binds|type)=="array" and ($original.HostConfig.Binds|length)==2 and
        all($original.HostConfig.Binds[]; type=="string" and length>0 and
          ((contains("\r") or contains("\n") or contains("\u0000"))|not) and
          (split(":")|length==3 and (.[0]|startswith("/")))) and
        ([$original.HostConfig.Binds[]|split(":")|.[1:]]|sort)==[["/etc/ntp.conf","ro"],["/run/lantern-config","ro"]] and
        all($requests[]; (.Source|type)=="string" and (.Source|test("^[A-Za-z0-9][A-Za-z0-9_.-]+$"))) and
        ($original.HostConfig.Mounts|type)=="array" and
        ($original.HostConfig.Mounts|sort_by(.Target))==($requests|sort_by(.Target)) and
        ([$original.Mounts[]|select(.Type=="volume")|{Name,Target:.Destination,RW}]|sort_by(.Target))==
          ($requests|map({Name:.Source,Target,RW:true})|sort_by(.Target)) and
        ($original.HostConfig.VolumesFrom==null or $original.HostConfig.VolumesFrom==[]) and
        ($original.HostConfig.Tmpfs==null or $original.HostConfig.Tmpfs=={}) and
        ($original.Config.Volumes==null or $original.Config.Volumes=={})
      ' >/dev/null || return 1
    originals+=("$original")
  done
  for service in lantern-0 lantern-1 lantern-2; do
    original="${originals[$index]}"
    network="$(jq -er '.HostConfig.NetworkMode|select(type=="string" and length>0)' <<<"$original")" || return 1
    # Replay the two read-only binds and declared NoCopy volumes separately.
    # Legacy volume binds can copy image ownership back over fresh empty roots.
    mounts=()
    while IFS= read -r binding; do mounts+=("--volume=$binding"); done < <(jq -r '.HostConfig.Binds[]' <<<"$original")
    while IFS= read -r binding; do mounts+=("--mount=$binding"); done < <(jq -r '.HostConfig.Mounts[]|"type=volume,source="+.Source+",target="+.Target+",volume-nocopy"' <<<"$original")
    run_owned_compose_initializer "$project" "$image" "$owner" compose-security-initializer "${service}-security-init" 900 validate_owned_compose_security_initializer \
      --network "$network" "${mounts[@]}" --entrypoint /app/lantern "$image" init-security || return "$?"
    index=$((index + 1))
  done
}
