#!/usr/bin/env bash
# Remove only the explicitly named benchmark project, including optional
# diagnostic services. A successful Compose exit is insufficient if residue
# remains or Docker cannot verify ownership-labelled resources.
down_owned_compose() {
  local project="$1"
  shift
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" ]] || return 1
  docker compose "$@" --profile off-diagnostics down -v --remove-orphans >/dev/null || return 1
  local resources
  resources="$(docker ps -aq --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$resources" ]] || return 1
  resources="$(docker volume ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$resources" ]] || return 1
  resources="$(docker network ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$resources" ]]
}

# Called only after the named project was proven absent. Refuse an existing
# workload, foreign/shared volume or any contents before changing ownership.
# Only the six newly-created state/backup directories enter the initializer.
initialize_owned_compose_volumes() {
  local project="$1" image="$2" owner="$3"
  shift 3
  [[ -n "$project" && "$project" == "${COMPOSE_PROJECT_NAME:-}" && "$image" =~ ^sha256:[0-9a-f]{64}$ && "$owner" =~ ^[1-9][0-9]*:[0-9]+$ ]] || return 1
  local residue
  residue="$(docker ps -aq --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$residue" ]] || return 1
  residue="$(docker volume ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$residue" ]] || return 1
  residue="$(docker network ls -q --filter "label=com.docker.compose.project=$project")" || return 1
  [[ -z "$residue" ]] || return 1
  local configuration expected existing name
  configuration="$(docker compose "$@" config --format json)" || return 1
  expected="$(jq -er --arg project "$project" --arg owner "$owner" '
    select(.name==$project) | . as $configuration |
    [range(0;3) | "lantern-\(.)" as $service |
      $configuration.services[$service] | select(.user==$owner) | .volumes[] |
      select(.type=="volume" and (.target=="/state" or .target=="/data")) |
      $configuration.volumes[.source] | select(.external!=true) | .name] |
    select(length==6 and (unique|length)==6) | .[]
  ' <<<"$configuration")" || return 1
  existing="$(docker volume ls --format '{{.Name}}')" || return 1
  for name in $expected; do
    [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]+$ && $'\n'"$existing"$'\n' != *$'\n'"$name"$'\n'* ]] || return 1
  done
  docker compose "$@" create lantern-0 lantern-1 lantern-2 >/dev/null || return 1
  local service container identity volumes volume label seen="|" index=0
  local -a mounts=()
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "$@" ps -aq "$service")" || return 1
    [[ "$container" =~ ^[0-9a-f]{12,64}$ ]] || return 1
    identity="$(docker inspect --format '{{json .}}' "$container")" || return 1
    volumes="$(jq -er --arg project "$project" --arg image "$image" --arg owner "$owner" --arg service "$service" '
      select(.Config.Labels["com.docker.compose.project"]==$project and
        .Config.Labels["com.docker.compose.service"]==$service and
        .Config.User==$owner and .Image==$image and .State.Status=="created") |
      [.Mounts[] | select(.Type=="volume" and (.Destination=="/state" or .Destination=="/data"))] |
      select(length==2 and (map(.Destination)|unique|length)==2) | sort_by(.Destination)[] | .Name
    ' <<<"$identity")" || return 1
    for volume in $volumes; do
      [[ "$volume" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]+$ && "$seen" != *"|$volume|"* ]] || return 1
      [[ $'\n'"$expected"$'\n' == *$'\n'"$volume"$'\n'* ]] || return 1
      label="$(docker volume inspect --format '{{index .Labels "com.docker.compose.project"}}' "$volume")" || return 1
      [[ "$label" == "$project" ]] || return 1
      seen+="$volume|"
      mounts+=(--mount "type=volume,source=$volume,target=/owned/$index")
      index=$((index + 1))
    done
  done
  [[ "$index" == 6 ]] || return 1
  docker run --rm --pull=never --network=none --read-only --user 0:0 \
    --cap-drop=ALL --cap-add=CHOWN --pids-limit=64 --memory=64m \
    --label=lantern.owner=compose-volume-initializer --entrypoint /bin/sh \
    "${mounts[@]}" "$image" -c '
      set -eu
      for directory in /owned/0 /owned/1 /owned/2 /owned/3 /owned/4 /owned/5; do
        test -d "$directory" && test -z "$(ls -A "$directory")" || exit 2
      done
      for directory in /owned/0 /owned/1 /owned/2 /owned/3 /owned/4 /owned/5; do
        chown 0:0 "$directory"
        chmod 700 "$directory"
        chown "$1" "$directory"
      done
    ' fixture "$owner"
}
