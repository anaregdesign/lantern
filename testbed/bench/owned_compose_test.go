package bench_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedComposeRejectsResidueAndUnverifiableOwnership(t *testing.T) {
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"none", "down", "container", "volume", "network", "inspect", "different_project"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			const script = `set -euo pipefail
source "$1"
export COMPOSE_PROJECT_NAME=lantern-bench-owned-fixture
failure="$2"
docker() {
  case "$*" in
    "compose -f fixture.yml --profile off-diagnostics down -v --remove-orphans") [[ "$failure" != down ]];;
    "ps -aq --filter label=com.docker.compose.project=lantern-bench-owned-fixture")
      [[ "$failure" != inspect ]] || return 19
      if [[ "$failure" == container ]]; then echo residue; fi;;
    "volume ls -q --filter label=com.docker.compose.project=lantern-bench-owned-fixture")
      if [[ "$failure" == volume ]]; then echo residue; fi;;
    "network ls -q --filter label=com.docker.compose.project=lantern-bench-owned-fixture")
      if [[ "$failure" == network ]]; then echo residue; fi;;
    *) return 89;;
  esac
}

project="$COMPOSE_PROJECT_NAME"
if [[ "$failure" == different_project ]]; then project=unrelated; fi
if down_owned_compose "$project" -f fixture.yml; then
  [[ "$failure" == none ]]
else
  [[ "$failure" != none ]]
fi
`
			if output, err := exec.CommandContext(ctx, "bash", "-c", script, "owned-compose-test", helper, failure).CombinedOutput(); err != nil {
				t.Fatalf("owned Compose verification: %v\n%s", err, output)
			}
		})
	}
}

func TestOwnedComposeInitializesOnlyFreshMatchingVolumes(t *testing.T) {
	helper, _ := filepath.Abs("owned_compose.sh")
	for _, profile := range []struct {
		name  string
		spool bool
		count int
	}{{name: "receipt-six", count: 6}, {name: "graph-nine", spool: true, count: 9}} {
		t.Run(profile.name, func(t *testing.T) {
			failures := []string{"none", "local-driver-empty-options", "null-driver-options", "root-user", "existing-project", "name-collision", "foreign-config", "external-volume", "unknown-volume", "unknown-mounted-volume", "foreign-driver", "malformed-driver", "bind-driver-options", "nonempty-driver-options", "array-driver-options", "string-driver-options", "bool-driver-options", "foreign-container", "wrong-user", "wrong-image", "running-container", "foreign-volume", "shared-volume", "missing-volume", "readonly-volume", "unexpected-volume", "swapped-target-volumes", "swapped-service-volumes", "nonempty-volume", "failed-final-volume-listing", "newline-only-entry"}
			if profile.spool {
				failures = append(failures, "partial-spool", "missing-spool", "duplicate-spool", "wrong-spool-target", "spool-bind", "spool-copy-enabled", "external-spool", "shared-spool", "runtime-spool-missing", "runtime-spool-target", "runtime-spool-bind", "runtime-spool-readonly", "runtime-spool-swapped")
			}
			for _, failure := range failures {
				t.Run(failure, func(t *testing.T) {
					root := t.TempDir()
					volumes := filepath.Join(root, "volumes")
					if err := os.Mkdir(volumes, 0700); err != nil {
						t.Fatal(err)
					}
					for i := range profile.count {
						if err := os.Mkdir(filepath.Join(volumes, fmt.Sprint(i)), 0755); err != nil {
							t.Fatal(err)
						}
					}
					sentinelPath := filepath.Join(root, "sentinel-path")
					changes := filepath.Join(root, "changes")
					services := map[string]any{}
					declared := map[string]any{}
					for i := range 3 {
						state, backup := fmt.Sprintf("state-%d", i), fmt.Sprintf("backup-%d", i)
						mounts := []any{
							map[string]any{"type": "volume", "source": state, "target": "/state"},
							map[string]any{"type": "volume", "source": backup, "target": "/data"},
						}
						declared[state], declared[backup] = map[string]any{"name": state}, map[string]any{"name": backup}
						if profile.spool {
							spool := fmt.Sprintf("snapshot-spool-%d", i)
							mount := map[string]any{"type": "volume", "source": spool, "target": "/var/lib/lantern-snapshot-spool", "volume": map[string]any{"nocopy": true}}
							if i == 2 {
								switch failure {
								case "wrong-spool-target":
									mount["target"] = "/unexpected-spool"
								case "spool-bind":
									mount["type"] = "bind"
								case "spool-copy-enabled":
									mount["volume"] = map[string]any{"nocopy": false}
								case "shared-spool":
									mount["source"] = "snapshot-spool-0"
								}
							}
							if failure != "partial-spool" || i != 2 {
								mounts = append(mounts, mount)
							}
							if failure == "duplicate-spool" && i == 2 {
								mounts = append(mounts, mount)
							}
							declared[spool] = map[string]any{"name": spool}
						}
						services[fmt.Sprintf("lantern-%d", i)] = map[string]any{"user": "501:20", "volumes": mounts}
					}
					project := "lantern-bench-owned-fixture"
					switch failure {
					case "foreign-config":
						project = "foreign"
					case "external-volume":
						declared["state-0"] = map[string]any{"name": "state-0", "external": true}
					case "unknown-volume":
						declared["unrequested"] = map[string]any{"name": "unrequested"}
					case "unknown-mounted-volume":
						declared["unrequested"] = map[string]any{"name": "unrequested"}
						service := services["lantern-1"].(map[string]any)
						service["volumes"] = append(service["volumes"].([]any), map[string]any{"type": "volume", "source": "unrequested", "target": "/unexpected"})
					case "missing-spool":
						delete(declared, "snapshot-spool-2")
					case "external-spool":
						declared["snapshot-spool-2"] = map[string]any{"name": "snapshot-spool-2", "external": true}
					}
					if failure == "local-driver-empty-options" || failure == "null-driver-options" {
						for _, item := range declared {
							volume := item.(map[string]any)
							if failure == "local-driver-empty-options" {
								volume["driver"], volume["driver_opts"] = "local", map[string]any{}
							} else {
								volume["driver"], volume["driver_opts"] = nil, nil
							}
						}
					}
					driverVolume := "state-2"
					if profile.spool {
						driverVolume = "snapshot-spool-2"
					}
					if volume, ok := declared[driverVolume].(map[string]any); ok {
						switch failure {
						case "foreign-driver":
							volume["driver"] = "nfs"
						case "malformed-driver":
							volume["driver"] = false
						case "bind-driver-options":
							volume["driver"] = "local"
							volume["driver_opts"] = map[string]any{"type": "none", "o": "bind", "device": "/unowned"}
						case "nonempty-driver-options":
							volume["driver_opts"] = map[string]any{"unexpected": "value"}
						case "array-driver-options":
							volume["driver_opts"] = []any{}
						case "string-driver-options":
							volume["driver_opts"] = ""
						case "bool-driver-options":
							volume["driver_opts"] = false
						}
					}
					configuration, err := json.Marshal(map[string]any{"name": project, "services": services, "volumes": declared})
					if err != nil {
						t.Fatal(err)
					}
					const script = `set -euo pipefail
source "$1"
export COMPOSE_PROJECT_NAME=lantern-bench-owned-fixture
failure="$2"
owner=501:20
if [[ "$failure" == root-user ]]; then owner=0:0; fi
run_owned_compose_initializer() {
 [[ "$1" == "$COMPOSE_PROJECT_NAME" && "$2" == "$TEST_IMAGE" && "$3" == 0:0 && "$4" == compose-volume-initializer && "$5" == volume-init && "$6" == 300 && "$7" == validate_owned_compose_volume_initializer ]] || return 90
 shift 7
 docker run --rm --pull=never --user 0:0 "$@"
}
docker() {
 if [[ "$1" == volume && "$2" == ls && "$3" == --format ]]; then
  if [[ "$failure" == name-collision ]]; then echo state-1; fi
  return
 fi
 if [[ "$1" == ps || ( "$1" == volume && "$2" == ls ) || "$1" == network ]]; then
  if [[ "$failure" == existing-project ]]; then echo existing; fi
  return
 fi
 if [[ "$1" == compose ]]; then
  if [[ "$4" == config ]]; then echo "$TEST_CONFIG"; return; fi
  if [[ "$4" == create ]]; then : > "$TEST_CREATED"; return; fi
  local service="${@: -1}" index
  index="${service##*-}"
  printf '%064d\n' "$((index+1))"; return
 fi
 if [[ "$1" == inspect ]]; then
  local container="${@: -1}" index project="$COMPOSE_PROJECT_NAME" user=501:20 image="$TEST_IMAGE" status=created
  index="$((10#${container: -1}-1))"
  if [[ "$failure" == foreign-container && "$index" == 1 ]]; then project=foreign; fi
  if [[ "$failure" == wrong-user && "$index" == 1 ]]; then user=0:0; fi
  if [[ "$failure" == wrong-image && "$index" == 1 ]]; then image=foreign; fi
  if [[ "$failure" == running-container && "$index" == 1 ]]; then status=running; fi
  local state="state-$index" backup="backup-$index" spool="snapshot-spool-$index" missing=false readonly=false extra=false
  if [[ "$failure" == shared-volume && "$index" == 2 ]]; then state=state-0; fi
  if [[ "$failure" == swapped-service-volumes && "$index" -lt 2 ]]; then state="state-$((1-index))"; fi
  if [[ "$failure" == swapped-target-volumes && "$index" == 1 ]]; then state=backup-1; backup=state-1; fi
  if [[ "$failure" == missing-volume && "$index" == 1 ]]; then missing=true; fi
  if [[ "$failure" == readonly-volume && "$index" == 1 ]]; then readonly=true; fi
  if [[ "$failure" == unexpected-volume && "$index" == 1 ]]; then extra=true; fi
  local spool_target=/var/lib/lantern-snapshot-spool spool_type=volume spool_rw=true spool_missing=false
  if [[ "$index" == 2 ]]; then
   if [[ "$failure" == runtime-spool-missing ]]; then spool_missing=true; fi
   if [[ "$failure" == runtime-spool-target ]]; then spool_target=/unexpected-spool; fi
   if [[ "$failure" == runtime-spool-bind ]]; then spool_type=bind; fi
   if [[ "$failure" == runtime-spool-readonly ]]; then spool_rw=false; fi
  fi
  if [[ "$failure" == runtime-spool-swapped && "$index" -lt 2 ]]; then spool="snapshot-spool-$((1-index))"; fi
  jq -nc --arg project "$project" --arg user "$user" --arg image "$image" --arg status "$status" --arg service "lantern-$index" --arg state "$state" --arg backup "$backup" --arg spool "$spool" --arg spool_target "$spool_target" --arg spool_type "$spool_type" --argjson with_spool "$TEST_SPOOL" --argjson spool_rw "$spool_rw" --argjson spool_missing "$spool_missing" --argjson missing "$missing" --argjson readonly "$readonly" --argjson extra "$extra" '
   {Config:{Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$service},User:$user},Image:$image,State:{Status:$status},
    Mounts:([{Type:"volume",Destination:"/state",Name:$state,RW:($readonly|not)}]+(if $missing then [] else [{Type:"volume",Destination:"/data",Name:$backup,RW:true}] end)+
      (if $with_spool and ($spool_missing|not) then [{Type:$spool_type,Destination:$spool_target,Name:$spool,RW:$spool_rw}] else [] end)+
      (if $extra then [{Type:"volume",Destination:"/unexpected",Name:"unrequested",RW:true}] else [] end))}'
  return
 fi
 if [[ "$1" == volume ]]; then
  if [[ "$failure" == foreign-volume && "${@: -1}" == state-2 ]]; then echo foreign; else echo "$COMPOSE_PROJECT_NAME"; fi
  return
 fi
 [[ "$1" == run && "$*" == *"--network=none"* && "$*" == *"--read-only"* && "$*" == *"--user 0:0"* ]] || return 88
 local argument source target capabilities= mount_count=0 last_target=
 for argument in "$@"; do
  case "$argument" in
   --cap-drop=*|--cap-add=*) capabilities+="$argument ";;
   --cap-drop|--cap-add|--privileged|--privileged=*) return 87;;
  esac
  if [[ "$argument" == type=volume,source=*,target=/owned/* ]]; then
   source="${argument#type=volume,source=}"; source="${source%%,*}"
   target="${argument##*target=/owned/}"; target="${target%%,*}"
   [[ "$target" == "$mount_count" ]] || return 86
   if [[ "$source" == "$TEST_LAST_VOLUME" ]]; then last_target="$target"; fi
   mount_count=$((mount_count+1))
  fi
 done
 [[ "$capabilities" == '--cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_READ_SEARCH ' ]] || return 87
 [[ "$mount_count" == "$TEST_COUNT" && -n "$last_target" ]] || return 85
 if [[ "$failure" == nonempty-volume ]]; then
  printf keep > "$TEST_VOLUMES/$last_target/original"
  printf '%s\n' "$TEST_VOLUMES/$last_target/original" > "$TEST_SENTINEL_PATH"
 fi
 if [[ "$failure" == newline-only-entry ]]; then
  printf keep > "$TEST_VOLUMES/$((TEST_COUNT-1))/"$'\n'
 fi
 while [[ "$1" != -c ]]; do shift; done
 shift
 local command="$1"; shift
 [[ "$#" == 3 && "$2" == 501:20 && "$3" == "$TEST_COUNT" ]] || return 84
 command="${command//\/owned\//$TEST_VOLUMES/}"
 TEST_LS_FAILURE_TARGET="$TEST_VOLUMES/$((TEST_COUNT-1))"
 export TEST_LS_FAILURE_TARGET
 ls() {
  if [[ "$TEST_CASE" == failed-final-volume-listing && "${@: -1}" == "$TEST_LS_FAILURE_TARGET" ]]; then
   : > "$TEST_LS_ATTEMPT"
   return 19
  fi
  command ls "$@"
 }
 chown() { printf 'chown %s\n' "$*" >> "$TEST_CHANGES"; }
 chmod() { printf 'chmod %s\n' "$*" >> "$TEST_CHANGES"; }
 export -f ls chown chmod
 bash -c "$command" "$@"
}

if initialize_owned_compose_volumes "$COMPOSE_PROJECT_NAME" "$TEST_IMAGE" "$owner" -f fixture.yml; then
 [[ "$TEST_SUCCESS" == true ]]
else
 [[ "$TEST_SUCCESS" == false ]]
fi
`
					command := exec.CommandContext(t.Context(), "bash", "-c", script, "fresh-volumes", helper, failure)
					created := filepath.Join(root, "created")
					lastVolume := "backup-2"
					if profile.spool {
						lastVolume = "snapshot-spool-2"
					}
					success := failure == "none" || failure == "local-driver-empty-options" || failure == "null-driver-options"
					lsAttempt := filepath.Join(root, "ls-attempt")
					command.Env = append(os.Environ(), "TEST_IMAGE=sha256:"+strings.Repeat("a", 64), "TEST_VOLUMES="+volumes, "TEST_CHANGES="+changes, "TEST_CREATED="+created, "TEST_CONFIG="+string(configuration), fmt.Sprintf("TEST_SPOOL=%t", profile.spool), fmt.Sprintf("TEST_COUNT=%d", profile.count), "TEST_LAST_VOLUME="+lastVolume, "TEST_SENTINEL_PATH="+sentinelPath, "TEST_CASE="+failure, fmt.Sprintf("TEST_SUCCESS=%t", success), "TEST_LS_ATTEMPT="+lsAttempt)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("fresh volume preparation: %v\n%s", err, output)
					}
					if !success {
						switch failure {
						case "existing-project", "root-user", "name-collision", "foreign-config", "external-volume", "unknown-volume", "unknown-mounted-volume", "foreign-driver", "malformed-driver", "bind-driver-options", "nonempty-driver-options", "array-driver-options", "string-driver-options", "bool-driver-options", "partial-spool", "missing-spool", "duplicate-spool", "wrong-spool-target", "spool-bind", "spool-copy-enabled", "external-spool", "shared-spool":
							if _, err := os.Stat(created); !os.IsNotExist(err) {
								t.Fatal("unsafe existing project or root identity reached Compose create", err)
							}
						}
						if _, err := os.Stat(changes); !os.IsNotExist(err) {
							t.Fatal("refused setup changed volume ownership", err)
						}
						if failure == "failed-final-volume-listing" {
							if _, err := os.Stat(lsAttempt); err != nil {
								t.Fatal("final-volume read failure was not exercised", err)
							}
						}
						if failure == "newline-only-entry" {
							original := filepath.Join(volumes, fmt.Sprint(profile.count-1), "\n")
							if raw, err := os.ReadFile(original); err != nil || string(raw) != "keep" {
								t.Fatal("newline-only original entry was changed or removed", err)
							}
						}
						if failure == "nonempty-volume" {
							original, err := os.ReadFile(sentinelPath)
							if err != nil {
								t.Fatal("nonempty final volume never reached the initializer preflight", err)
							}
							if raw, err := os.ReadFile(strings.TrimSpace(string(original))); err != nil || string(raw) != "keep" {
								t.Fatal("original volume contents changed", err)
							}
						}
						return
					}
					raw, err := os.ReadFile(changes)
					if err != nil || strings.Count(string(raw), "chown 0:0 ") != profile.count || strings.Count(string(raw), "chown 501:20 ") != profile.count || strings.Count(string(raw), "chmod 700 ") != profile.count {
						t.Fatalf("incomplete scoped volume preparation: %v %s", err, raw)
					}
				})
			}
		})
	}
}

func TestOwnedComposeInitializerCustody(t *testing.T) {
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"security", "volume"} {
		for _, failure := range []string{"none", "init-failure", "client-disconnect", "INT", "TERM", "wrong-init-image", "wrong-init-user", "wrong-init-env", "wrong-init-mount", "wrong-init-label", "wrong-init-exit", "stop-failure", "remove-failure"} {
			if profile == "volume" && failure == "wrong-init-env" {
				continue
			}
			t.Run(profile+"/"+failure, func(t *testing.T) {
				out := t.TempDir()
				const script = `set -euo pipefail
source "$1"
export COMPOSE_PROJECT_NAME=lantern-bench-owned-fixture
TEST_MAIN_PID=$BASHPID
TEST_IMAGE="sha256:$(printf 'a%.0s' {1..64})"
TEST_SECURITY_CONFIG='{"name":"lantern-bench-owned-fixture","services":{},"volumes":{}}'
for index in 0 1 2; do
 TEST_SECURITY_CONFIG="$(jq -c --arg service "lantern-$index" --arg image "$TEST_IMAGE" '
  .services[$service]={image:$image,user:"501:20",environment:{LANTERN_AUTH_MODE:"oidc",LANTERN_SECURITY_CONFIG_JSON:("current-json-"+$service)},
   volumes:[{type:"volume",source:("state-"+$service),target:"/state"},{type:"volume",source:("backup-"+$service),target:"/data"}]} |
  .volumes["state-"+$service]={name:("state-"+$service)} | .volumes["backup-"+$service]={name:("backup-"+$service)}' <<<"$TEST_SECURITY_CONFIG")"
done
sleep() { command sleep 0.005; }
docker() {
 if [[ "$1" == compose ]]; then
  case "$4" in
   config) [[ "$LANTERN_IMAGE" == "$TEST_IMAGE" ]] || return 89; printf '%s\n' "$TEST_SECURITY_CONFIG";;
   ps) local svc="${@: -1}"; printf '%064d\n' "$((${svc##*-}+1))";;
   *) return 87;;
  esac
  return
 fi
 if [[ "$1" == create ]]; then
  local arg next="" name="" nonce_value="" env_file="" src="" user_value="" network_value="" kind_value="" service_value=""
  for arg in "$@"; do
   case "$next" in
    --name) name="$arg";; --user) user_value="$arg";; --network) network_value="$arg";; --env-file) env_file="$arg";; --volumes-from) src="$arg";;
    --label) case "$arg" in lantern.initializer=*) nonce_value="${arg#*=}";; lantern.owner=*) kind_value="${arg#*=}";; com.docker.compose.service=*) service_value="${arg#*=}";; esac;;
   esac
   next="$arg"
  done
  local i=0
  if [[ "$PROFILE" == security ]]; then
   i="${service_value##*-}"
   [[ "$name" == "$COMPOSE_PROJECT_NAME-$service_value-security-init" && "$user_value" == 501:20 && "$network_value" == "$COMPOSE_PROJECT_NAME-default" && "$src" == "$(printf '%064d' "$((i+1))")" && "$kind_value" == compose-security-initializer ]] || return 88
   [[ "$(cat "$env_file")" == $'LANTERN_AUTH_MODE=oidc\nLANTERN_SECURITY_CONFIG_JSON=current-json-'"$service_value" ]] || return 88
  else
   [[ "$name" == "$COMPOSE_PROJECT_NAME-volume-init" && "$user_value" == 0:0 && "$kind_value" == compose-volume-initializer ]] || return 88
  fi
  [[ "$nonce_value" == lantern-owned-init.* && "${@: -2:1}" == "$TEST_IMAGE" || "$PROFILE" == volume ]] || return 88
  printf '%s\n' "$nonce_value" > "$OUTDIR/nonce-$i"
  printf 'created\n' > "$OUTDIR/state-$i"
  printf 'create %s\n' "$i" >> "$OUTDIR/actions"
  printf '%064d\n' "$((i+11))"
  return
 fi
 local target="${@: -1}" i=0 initialized=false
 if [[ "$target" == *-security-init ]]; then i="${target%-security-init}"; i="${i##*-}"; initialized=true
 elif [[ "$target" == *-volume-init ]]; then initialized=true
 elif [[ "$target" =~ ^[0-9a-f]{64}$ ]]; then
  i="$((10#${target: -2}))"
  if (( i >= 11 )); then i=$((i-11)); initialized=true; else i=$((i-1)); fi
 else return 86; fi
 local svc="lantern-$i" fail_index=1
 if [[ "$PROFILE" == volume ]]; then svc=volume-init; fail_index=0; fi
 case "$1" in
  start)
   [[ "$2" == --attach && "$initialized" == true ]] || return 86
   printf 'start %s\n' "$i" >> "$OUTDIR/actions"
   if [[ "$i" == "$fail_index" && ( "$CASE" == client-disconnect || "$CASE" == stop-failure || "$CASE" == INT || "$CASE" == TERM ) ]]; then
    printf 'running\n' > "$OUTDIR/state-$i"
    if [[ "$CASE" == INT || "$CASE" == TERM ]]; then kill -"$CASE" "$TEST_MAIN_PID"; while :; do command sleep 1; done; fi
    return 23
   fi
   printf 'exited\n' > "$OUTDIR/state-$i"
   if [[ "$i" == "$fail_index" && "$CASE" == init-failure ]]; then return 23; fi
   return;;
  stop)
   [[ "$initialized" == true && "$2" == --time && "$3" == 10 ]] || return 86
   printf 'stop %s\n' "$i" >> "$OUTDIR/actions"
   [[ "$CASE" != stop-failure ]] || return 19
   printf 'exited\n' > "$OUTDIR/state-$i"; return;;
  kill)
   [[ "$initialized" == true && "$2" == --signal && "$3" == KILL ]] || return 86
   printf 'kill %s\n' "$i" >> "$OUTDIR/actions"
   printf 'exited\n' > "$OUTDIR/state-$i"; return;;
  wait) printf 'wait %s\n' "$i" >> "$OUTDIR/actions"; printf '0\n'; return;;
  logs) printf 'logs %s\n' "$i" >> "$OUTDIR/actions"; printf 'private retained fixture log\n'; return;;
  rm)
   printf 'rm %s\n' "$i" >> "$OUTDIR/actions"
   [[ "$CASE" != remove-failure || "$i" != "$fail_index" ]] || return 19
   rm "$OUTDIR/state-$i"; return;;
  inspect) ;;
  *) return 86;;
 esac
 local state=created nonce_value="" current_image="$TEST_IMAGE" user_value=501:20 exit_value=0
 if [[ "$initialized" == true ]]; then
  [[ -f "$OUTDIR/state-$i" ]] || return 1
  read -r state < "$OUTDIR/state-$i"; read -r nonce_value < "$OUTDIR/nonce-$i"
 fi
 if [[ "$PROFILE" == volume ]]; then user_value=0:0; fi
 jq -nc --arg project "$COMPOSE_PROJECT_NAME" --arg svc "$svc" --arg image "$TEST_IMAGE" --arg case "$CASE" --arg profile "$PROFILE" --arg state "$state" --arg nonce "$nonce_value" --arg user "$user_value" --argjson i "$i" --argjson fail_index "$fail_index" --argjson initialized "$initialized" '
  {Id:((if $initialized then $i+11 else $i+1 end)|tostring|("0"*(64-length)) + .),
   Name:("/"+$project+"-"+$svc+(if $profile=="security" then "-security-init" else "" end)),
   Config:{Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$svc},Image:$image,User:$user,WorkingDir:"/app",
    Env:["LANTERN_AUTH_MODE=oidc",("LANTERN_SECURITY_CONFIG_JSON=current-json-"+$svc)],Entrypoint:["/app/lantern"],Cmd:["init-security"]},Image:$image,
   State:{Status:$state,Running:($state=="running"),ExitCode:0},HostConfig:{NetworkMode:($project+"-default")},
   Mounts:[{Type:"bind",Source:("/private/"+$svc),Destination:"/run/lantern-config",RW:false},{Type:"bind",Source:("/private/"+$svc+"/ntp.conf"),Destination:"/etc/ntp.conf",RW:false},{Type:"volume",Name:("state-"+$svc),Destination:"/state",RW:true}]} |
  if $initialized then .Config.Labels += {"lantern.owner":("compose-"+$profile+"-initializer"),"com.docker.compose.oneoff":"True","lantern.initializer":$nonce} else . end |
  if $profile=="volume" then .Config.Entrypoint=["/bin/sh"] | .Config.Cmd=["-c","fixture-script","fixture","501:20","6"] |
   .HostConfig={NetworkMode:"none",ReadonlyRootfs:true,CapDrop:["ALL"],CapAdd:["CHOWN","DAC_READ_SEARCH"]} |
   .Mounts=[range(0;6)|{Name:("owned-"+tostring),Destination:("/owned/"+tostring),RW:true,Type:"volume"}] else . end |
  if $initialized and $i==$fail_index then
   if $case=="wrong-init-image" then .Image="wrong"
   elif $case=="wrong-init-user" then .Config.User="foreign"
   elif $case=="wrong-init-env" then .Config.Env=[]
   elif $case=="wrong-init-mount" then .Mounts=[]
   elif $case=="wrong-init-label" then .Config.Labels["lantern.owner"]="foreign"
   elif $case=="wrong-init-exit" and $state=="exited" then .State.ExitCode=23 else . end
  else . end'
}
trap 'printf "old INT\n" >> "$OUTDIR/traps"' INT
trap 'printf "old TERM\n" >> "$OUTDIR/traps"' TERM
if [[ "$PROFILE" == security ]]; then
 if initialize_owned_compose_security "$COMPOSE_PROJECT_NAME" "$TEST_IMAGE" 501:20 -f fixture.yml; then result=0; else result=$?; fi
else
 expected_count=6; volume_runtime_owner=501:20
 expected_mounts="$(jq -nc '[range(0;6)|{Name:("owned-"+tostring),Destination:("/owned/"+tostring),RW:true,Type:"volume"}]')"
 if run_owned_compose_initializer "$COMPOSE_PROJECT_NAME" "$TEST_IMAGE" 0:0 compose-volume-initializer volume-init 300 validate_owned_compose_volume_initializer --network=none --read-only --cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_READ_SEARCH --entrypoint /bin/sh "$TEST_IMAGE" -c fixture-script fixture 501:20 6; then result=0; else result=$?; fi
fi
[[ "$(trap -p INT)" == *"old INT"* && "$(trap -p TERM)" == *"old TERM"* ]] || exit 95
exit "$result"
`
				cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "initializer-custody", helper)
				cmd.Env = append(os.Environ(), "OUTDIR="+out, "CASE="+failure, "PROFILE="+profile, "TMPDIR="+out)
				output, runErr := cmd.CombinedOutput()
				if (runErr == nil) != (failure == "none") {
					t.Fatalf("initializer error=%v output=%s", runErr, output)
				}
				actions, _ := os.ReadFile(filepath.Join(out, "actions"))
				failed := 1
				if profile == "volume" {
					failed = 0
				}
				if failure == "none" {
					count := 3
					if profile == "volume" {
						count = 1
					}
					if strings.Count(string(actions), "start ") != count || strings.Count(string(actions), "rm ") != count {
						t.Fatalf("successful lifecycle missing exact starts/removals: %s", actions)
					}
				} else {
					if _, err := os.Stat(filepath.Join(out, fmt.Sprintf("state-%d", failed))); err != nil {
						t.Fatalf("failure container/state was removed: %v, %s", err, actions)
					}
					if profile == "security" && strings.Contains(string(actions), "create 2") {
						t.Fatalf("continued after failed initialization: %s", actions)
					}
					if failure == "client-disconnect" || failure == "INT" || failure == "TERM" || failure == "stop-failure" {
						for _, action := range []string{"stop", "wait", "logs"} {
							if !strings.Contains(string(actions), fmt.Sprintf("%s %d\n", action, failed)) {
								t.Fatalf("missing failure custody %s: %s %s", action, actions, output)
							}
						}
						state, _ := os.ReadFile(filepath.Join(out, fmt.Sprintf("state-%d", failed)))
						if string(state) != "exited\n" {
							t.Fatalf("daemon initializer still active: %q", state)
						}
						if failure == "stop-failure" && !strings.Contains(string(actions), fmt.Sprintf("kill %d\n", failed)) {
							t.Fatalf("failed stop lacked identity-bound kill fallback: %s", actions)
						}
					}
					if strings.HasPrefix(failure, "wrong-init-") && failure != "wrong-init-exit" && strings.Contains(string(actions), fmt.Sprintf("start %d\n", failed)) {
						t.Fatalf("unverified clone started: %s", actions)
					}
					if !strings.Contains(string(output), "initializer private evidence retained at ") {
						t.Fatalf("private logs not retained: %s", output)
					}
				}
			})
		}
	}
}
