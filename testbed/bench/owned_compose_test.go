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
	for _, failure := range []string{"none", "root-user", "existing-project", "name-collision", "foreign-config", "external-volume", "foreign-container", "wrong-user", "wrong-image", "running-container", "foreign-volume", "shared-volume", "missing-volume", "nonempty-volume"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			volumes := filepath.Join(root, "volumes")
			if err := os.Mkdir(volumes, 0700); err != nil {
				t.Fatal(err)
			}
			for i := range 6 {
				if err := os.Mkdir(filepath.Join(volumes, string(rune('0'+i))), 0755); err != nil {
					t.Fatal(err)
				}
			}
			original := filepath.Join(volumes, "5", "original")
			if failure == "nonempty-volume" {
				if err := os.WriteFile(original, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			changes := filepath.Join(root, "changes")
			services := map[string]any{}
			declared := map[string]any{}
			for i := range 3 {
				state, backup := fmt.Sprintf("state-%d", i), fmt.Sprintf("backup-%d", i)
				services[fmt.Sprintf("lantern-%d", i)] = map[string]any{"user": "501:20", "volumes": []any{
					map[string]any{"type": "volume", "source": state, "target": "/state"},
					map[string]any{"type": "volume", "source": backup, "target": "/data"},
				}}
				declared[state], declared[backup] = map[string]any{"name": state}, map[string]any{"name": backup}
			}
			project := "lantern-bench-owned-fixture"
			if failure == "foreign-config" {
				project = "foreign"
			}
			if failure == "external-volume" {
				declared["state-0"] = map[string]any{"name": "state-0", "external": true}
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
  local service="${@: -1}" index="${service##*-}"
  printf '%064d\n' "$((index+1))"; return
 fi
 if [[ "$1" == inspect ]]; then
  local container="${@: -1}" index project="$COMPOSE_PROJECT_NAME" user=501:20 image="$TEST_IMAGE" status=created
  index="$((10#${container: -1}-1))"
  if [[ "$failure" == foreign-container && "$index" == 1 ]]; then project=foreign; fi
  if [[ "$failure" == wrong-user && "$index" == 1 ]]; then user=0:0; fi
  if [[ "$failure" == wrong-image && "$index" == 1 ]]; then image=foreign; fi
  if [[ "$failure" == running-container && "$index" == 1 ]]; then status=running; fi
  local state="state-$index" backup="backup-$index" missing=false
  if [[ "$failure" == shared-volume && "$index" == 2 ]]; then state=state-0; fi
  if [[ "$failure" == missing-volume && "$index" == 1 ]]; then missing=true; fi
  jq -nc --arg project "$project" --arg user "$user" --arg image "$image" --arg status "$status" --arg service "lantern-$index" --arg state "$state" --arg backup "$backup" --argjson missing "$missing" '
   {Config:{Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$service},User:$user},Image:$image,State:{Status:$status},
    Mounts:([{Type:"volume",Destination:"/state",Name:$state}]+(if $missing then [] else [{Type:"volume",Destination:"/data",Name:$backup}] end))}'
  return
 fi
 if [[ "$1" == volume ]]; then
  if [[ "$failure" == foreign-volume && "${@: -1}" == state-2 ]]; then echo foreign; else echo "$COMPOSE_PROJECT_NAME"; fi
  return
 fi
 [[ "$1" == run && "$*" == *"--network=none"* && "$*" == *"--read-only"* && "$*" == *"--cap-drop=ALL"* && "$*" == *"--cap-add=CHOWN"* ]] || return 88
 while [[ "$1" != -c ]]; do shift; done
 shift
 local command="$1"; shift
 command="${command//\/owned\//$TEST_VOLUMES/}"
 chown() { printf 'chown %s\n' "$*" >> "$TEST_CHANGES"; }
 chmod() { printf 'chmod %s\n' "$*" >> "$TEST_CHANGES"; }
 export -f chown chmod
 bash -c "$command" "$@"
}
if initialize_owned_compose_volumes "$COMPOSE_PROJECT_NAME" "$TEST_IMAGE" "$owner" -f fixture.yml; then
 [[ "$failure" == none ]]
else
 [[ "$failure" != none ]]
fi
`
			command := exec.CommandContext(t.Context(), "bash", "-c", script, "fresh-volumes", helper, failure)
			created := filepath.Join(root, "created")
			command.Env = append(os.Environ(), "TEST_IMAGE=sha256:"+strings.Repeat("a", 64), "TEST_VOLUMES="+volumes, "TEST_CHANGES="+changes, "TEST_CREATED="+created, "TEST_CONFIG="+string(configuration))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("fresh volume preparation: %v\n%s", err, output)
			}
			if failure != "none" {
				if failure == "existing-project" || failure == "root-user" || failure == "name-collision" || failure == "foreign-config" || failure == "external-volume" {
					if _, err := os.Stat(created); !os.IsNotExist(err) {
						t.Fatal("unsafe existing project or root identity reached Compose create", err)
					}
				}
				if _, err := os.Stat(changes); !os.IsNotExist(err) {
					t.Fatal("refused setup changed volume ownership", err)
				}
				if failure == "nonempty-volume" {
					if raw, err := os.ReadFile(original); err != nil || string(raw) != "keep" {
						t.Fatal("original volume contents changed", err)
					}
				}
				return
			}
			raw, err := os.ReadFile(changes)
			if err != nil || strings.Count(string(raw), "chown 0:0 ") != 6 || strings.Count(string(raw), "chown 501:20 ") != 6 || strings.Count(string(raw), "chmod 700 ") != 6 {
				t.Fatalf("incomplete scoped volume preparation: %v %s", err, raw)
			}
		})
	}
}
