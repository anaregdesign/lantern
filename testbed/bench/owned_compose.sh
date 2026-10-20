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
  local service container identity volumes volume label wanted seen="|" index=0
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
      index=$((index + 1))
    done
  done
  [[ "$index" == "$expected_count" ]] || return 1
  docker run --rm --pull=never --network=none --read-only --user 0:0 \
    --cap-drop=ALL --cap-add=CHOWN --pids-limit=64 --memory=64m \
    --label=lantern.owner=compose-volume-initializer --entrypoint /bin/sh \
    "${mounts[@]}" "$image" -c '
      set -eu
      case "$2" in 6|9) ;; *) exit 2 ;; esac
      index=0
      while test "$index" -lt "$2"; do
        directory="/owned/$index"
        test ! -L "$directory" && test -d "$directory" || exit 2
        contents="$(ls -A "$directory")" || exit 2
        test -z "$contents" || exit 2
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
