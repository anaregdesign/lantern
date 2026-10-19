#!/usr/bin/env bash
# Prepare the original authority for Linux serving containers. The exporter
# runs separately from every Server, with only this new private workspace.
prepare_compose_authority_exporter() {
  local repository="$1" workspace="$2" image="$3" platform architecture docker_path exporter launcher owner metadata
  [[ "$repository" == /* && -d "$repository/server" && "$workspace" == /* && -d "$workspace" && ! -L "$workspace" && "$workspace" != *','* && "$workspace" != *$'\n'* ]] || return 1
  [[ "$image" =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
  case "$(uname -s)" in
    Darwin) metadata="$(stat -f '%Lp:%u' "$workspace")" || return 1 ;;
    Linux) metadata="$(stat -c '%a:%u' "$workspace")" || return 1 ;;
    *) return 1 ;;
  esac
  [[ "$metadata" == "700:$(id -u)" ]] || return 1
  platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" || return 1
  case "$platform" in
    linux/amd64) architecture=amd64 ;;
    linux/arm64) architecture=arm64 ;;
    *) echo 'Compose authority requires a supported Linux image' >&2; return 1 ;;
  esac
  docker_path="$(command -v docker)" || return 1
  [[ "$docker_path" == /* ]] || return 1
  exporter="$workspace/authority-linux.test"
  launcher="$workspace/authority-exporter"
  [[ ! -e "$exporter" && ! -L "$exporter" && ! -e "$launcher" && ! -L "$launcher" ]] || return 1
  CGO_ENABLED=0 GOFLAGS= GOOS=linux GOARCH="$architecture" \
    go -C "$repository/server" test -c -o "$exporter" ./internal/security >&2 || return 1
  owner="$(id -u):$(id -g)"
  (
    umask 077
    set -o noclobber
    {
      printf '#!/usr/bin/env bash\nset -euo pipefail\n'
      printf 'exec %q run --rm --pull=never --network=none --read-only --cap-drop=ALL --security-opt=no-new-privileges --pids-limit=256 --memory=1g --label=lantern.owner=compose-authority-provisioner ' "$docker_path"
      printf '%s ' '--tmpfs /tmp:rw,nosuid,nodev,size=64m,mode=1777'
      printf -- '--user %q --mount %q --entrypoint %q ' "$owner" "type=bind,source=$workspace,target=$workspace" "$exporter"
      printf '%s ' '--env LANTERN_CURRENT_FIXTURE_DIR --env LANTERN_CURRENT_FIXTURE_ISSUER --env LANTERN_CURRENT_FIXTURE_VOTERS --env LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE --env LANTERN_AUTH_FIXTURE_COMPOSE'
      printf '%q -test.timeout=75s "$@"\n' "$image"
    } > "$launcher"
    chmod 700 "$launcher"
  ) || return 1
  printf '%s\n' "$launcher"
}

# This is a declared deployment input, not an OS clock update or a time
# measurement. The normal production producer still contacts and validates it.
write_compose_time_source() {
  local directory="$1" service
  [[ "$directory" == /* && -d "$directory" && ! -L "$directory" ]] || return 1
  for service in lantern-0 lantern-1 lantern-2; do
    [[ -d "$directory/$service" && ! -L "$directory/$service" && ! -e "$directory/$service/ntp.conf" && ! -L "$directory/$service/ntp.conf" ]] || return 1
  done
  for service in lantern-0 lantern-1 lantern-2; do
    (
      umask 022
      set -o noclobber
      printf 'server time.asia.apple.com\n' > "$directory/$service/ntp.conf"
      chmod 644 "$directory/$service/ntp.conf"
    ) || return 1
  done
}
