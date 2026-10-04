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
