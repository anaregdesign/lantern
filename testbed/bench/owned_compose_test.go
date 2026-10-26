//go:build unix

package bench_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type ownedComposeFixtureResult struct {
	directory       string
	output          []byte
	commandErr      error
	controlErr      error
	exitCode        int
	timedOut        bool
	leftOver        bool
	inactive        bool
	reaped          bool
	controlComplete bool
}

func (result ownedComposeFixtureResult) controlQualified() bool {
	return result.controlErr == nil && result.controlComplete && result.reaped && result.inactive && !result.timedOut && !result.leftOver
}

// This entire file exercises Bash/POSIX ownership. Windows native CI selects
// other packages; this fixture makes no Windows process-custody claim.
func runOwnedComposeFixture(t *testing.T, script string, arguments, environment []string, budget time.Duration, beforeSeal ...func(string) error) (result ownedComposeFixtureResult) {
	t.Helper()
	result.exitCode = -1
	if len(beforeSeal) > 1 {
		result.controlErr = errors.New("multiple fixture seal hooks")
		return
	}
	var err error
	result.directory, err = os.MkdirTemp("", "lantern-owned-compose-fixture-")
	if err != nil {
		result.controlErr = err
		return
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("owned fixture evidence retained at %s", result.directory)
		} else if err := os.RemoveAll(result.directory); err != nil {
			t.Errorf("remove completed fixture: %v", err)
		}
	})
	log, err := os.OpenFile(filepath.Join(result.directory, "fixture.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		result.controlErr = err
		return
	}
	// Fallback closes are best effort; the primary seal/close is checked below.
	defer func() { _ = log.Close() }()
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		result.controlErr = err
		return
	}
	defer func() { _ = gateRead.Close() }()
	defer func() { _ = gateWrite.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	// No fixture work starts before the controller durably publishes custody.
	preamble := `IFS= read -r owned_fixture_gate <&3 || exit 97
exec 3<&-
[[ "$owned_fixture_gate" == go ]] || exit 97
`
	args := append([]string{"-c", preamble + script, "owned-compose-fixture"}, arguments...)
	cmd := exec.CommandContext(ctx, "bash", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{gateRead}
	// Regular files avoid an unbounded pipe-copy wait after the direct child
	// exits. Only proven group inactivity permits reading/sealing that output.
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), environment...)
	cmd.Env = append(cmd.Env, "OUTDIR="+result.directory, "TMPDIR="+result.directory)
	cmd.WaitDelay = time.Second // fallback only; the group controller owns cleanup
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err == syscall.ESRCH {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		result.controlErr = err
		return
	}
	group := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reaped := false
	defer func() {
		// The natural-drain allowance covers a finite fake sleep interrupted when
		// its parent dies. Containment of any longer-lived member is a FAILURE.
		if !waitOwnedComposeGroup(group, 1500*time.Millisecond) {
			result.leftOver = true
			if err := syscall.Kill(-group, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
				result.controlErr = errors.Join(result.controlErr, err)
			}
			if !waitOwnedComposeGroup(group, time.Second) {
				if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
					result.controlErr = errors.Join(result.controlErr, err)
				}
			}
		}
		if !reaped {
			select {
			case waitErr := <-done:
				result.commandErr = waitErr
				reaped = true
			case <-time.After(2 * time.Second):
				result.controlErr = errors.Join(result.controlErr, errors.New("fixture direct reap unproven"))
			}
		}
		result.inactive = waitOwnedComposeGroup(group, 2*time.Second)
		result.reaped = reaped
		if reaped && cmd.ProcessState != nil {
			result.exitCode = cmd.ProcessState.ExitCode()
		}
		terminal := map[string]any{"pid": group, "pgid": group, "inactive": result.inactive,
			"direct_reaped": reaped, "timed_out": result.timedOut, "left_over": result.leftOver, "exit_code": result.exitCode}
		if !result.inactive || !reaped {
			result.controlErr = errors.Join(result.controlErr, errors.New("fixture process custody incomplete"))
		}
		// Test-only injections run after terminal process proof. They never change
		// child commands, signal custody, waits or the fixture's time allowance.
		if result.inactive && reaped && len(beforeSeal) == 1 {
			result.controlErr = errors.Join(result.controlErr, beforeSeal[0](result.directory))
		}
		result.controlErr = errors.Join(result.controlErr, writeOwnedComposeFixtureState(result.directory, "terminal.json", terminal))
		if _, err := fmt.Fprintf(os.Stderr, "owned_compose_fixture_terminal pgid=%d inactive=%t reaped=%t timeout=%t leftover=%t evidence=%s\n", group, result.inactive, reaped, result.timedOut, result.leftOver, result.directory); err != nil {
			result.controlErr = errors.Join(result.controlErr, err)
		}
		if !result.inactive || !reaped {
			return
		}
		result.controlErr = errors.Join(result.controlErr, log.Sync(), log.Close())
		output, openErr := os.Open(filepath.Join(result.directory, "fixture.log"))
		if openErr != nil {
			result.controlErr = errors.Join(result.controlErr, openErr)
			return
		}
		defer func() { _ = output.Close() }()
		result.output, err = io.ReadAll(io.LimitReader(output, 64*1024+1))
		result.controlErr = errors.Join(result.controlErr, err, output.Close())
		if len(result.output) > 64*1024 {
			result.output = result.output[:64*1024]
			result.controlErr = errors.Join(result.controlErr, errors.New("fixture output exceeds diagnostic limit"))
		}
		result.controlComplete = result.controlErr == nil
	}()
	if err := gateRead.Close(); err != nil {
		result.controlErr = err
		return
	}
	if actualGroup, err := syscall.Getpgid(group); err != nil || actualGroup != group || group == syscall.Getpgrp() {
		result.controlErr = errors.New("fixture group identity unproven")
		return
	}
	if err := writeOwnedComposeFixtureState(result.directory, "started.json", map[string]any{"pid": group, "pgid": group, "budget_ms": budget.Milliseconds()}); err != nil {
		result.controlErr = err
		return
	}
	if _, err := fmt.Fprintf(os.Stderr, "owned_compose_fixture_started pgid=%d evidence=%s\n", group, result.directory); err != nil {
		result.controlErr = err
		return
	}
	if _, err := io.WriteString(gateWrite, "go\n"); err != nil {
		result.controlErr = err
		return
	}
	if err := gateWrite.Close(); err != nil {
		result.controlErr = err
		return
	}
	select {
	case result.commandErr = <-done:
		reaped = true
	case <-ctx.Done():
		result.timedOut = true
	}
	if ctx.Err() != nil {
		result.timedOut = true
	}
	return
}

func waitOwnedComposeGroup(group int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if err := syscall.Kill(-group, 0); err == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeOwnedComposeFixtureState(directory, name string, value map[string]any) (err error) {
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	return file.Sync()
}

func ownedComposeFixtureDiagnostics(directory string) string {
	var summary strings.Builder
	for _, name := range []string{"started.json", "terminal.json", "actions", "client-pid", "launch-injected", "launch-deferred", "state-0", "state-1", "state-2"} {
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			fmt.Fprintf(&summary, "%s: missing\n", name)
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(file, 1024))
		err = errors.Join(err, file.Close())
		fmt.Fprintf(&summary, "%s: %q read_error=%t\n", name, raw, err != nil)
	}
	return summary.String()
}

func TestOwnedComposeFixtureContainment(t *testing.T) {
	for _, test := range []struct {
		name     string
		script   string
		budget   time.Duration
		timedOut bool
		leftOver bool
	}{
		{name: "ordinary-exit", script: "printf 'complete\\n'", budget: 5 * time.Second},
		{name: "deadline", script: `trap '' TERM
( trap '' TERM; while :; do sleep 30; done ) &
printf 'deadline worker started\n'
wait`, budget: 2 * time.Second, timedOut: true, leftOver: true},
		{name: "leader-exit-live-output-writer", script: `( trap '' TERM; while :; do sleep 30; done ) &
printf 'leader exiting with inherited writer\n'
exit 0`, budget: 10 * time.Second, leftOver: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := runOwnedComposeFixture(t, test.script, nil, nil, test.budget)
			if fixture.controlErr != nil || !fixture.controlComplete || !fixture.reaped || !fixture.inactive {
				t.Fatalf("containment controller/evidence failed: complete=%t reaped=%t inactive=%t err=%v evidence=%s", fixture.controlComplete, fixture.reaped, fixture.inactive, fixture.controlErr, fixture.directory)
			}
			if fixture.timedOut != test.timedOut || fixture.leftOver != test.leftOver {
				t.Fatalf("containment timeout=%t leftover=%t want=%t/%t evidence=%s", fixture.timedOut, fixture.leftOver, test.timedOut, test.leftOver, fixture.directory)
			}
			if test.leftOver && !strings.Contains(string(fixture.output), "started") && !strings.Contains(string(fixture.output), "inherited writer") {
				t.Fatalf("controlled owned child was not launched: %s", fixture.output)
			}
			if fixture.controlQualified() != (!test.timedOut && !test.leftOver) {
				t.Fatal("fixture qualification did not retain the specific containment failure")
			}
			for _, name := range []string{"started.json", "terminal.json"} {
				raw, err := os.ReadFile(filepath.Join(fixture.directory, name))
				if err != nil || !json.Valid(raw) {
					t.Fatalf("missing bounded custody evidence %s: %v", name, err)
				}
			}
			if test.timedOut {
				var exited *exec.ExitError
				if !errors.As(fixture.commandErr, &exited) || fixture.exitCode != -1 || exited.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
					t.Fatalf("deadline fixture lacked its exact forced exit: code=%d err=%v", fixture.exitCode, fixture.commandErr)
				}
			} else if fixture.commandErr != nil || fixture.exitCode != 0 {
				t.Fatalf("fixture direct command failed unexpectedly: %v %s", fixture.commandErr, fixture.output)
			}
		})
	}
}

func TestOwnedComposeFixtureEvidenceFailureIsNotExpectedRefusal(t *testing.T) {
	for _, test := range []struct {
		name string
		seal func(string) error
		want error
	}{
		{name: "terminal-ledger", seal: func(directory string) error {
			return os.WriteFile(filepath.Join(directory, "terminal.json"), []byte("occupied"), 0600)
		}, want: os.ErrExist},
		{name: "output-read", seal: func(directory string) error {
			return os.Rename(filepath.Join(directory, "fixture.log"), filepath.Join(directory, "held-output.log"))
		}, want: os.ErrNotExist},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := runOwnedComposeFixture(t, "exit 23", nil, nil, 5*time.Second, test.seal)
			var exited *exec.ExitError
			if !errors.As(fixture.commandErr, &exited) || exited.ExitCode() != 23 || fixture.exitCode != 23 {
				t.Fatalf("expected rejection was not observed independently: %v", fixture.commandErr)
			}
			if !errors.Is(fixture.controlErr, test.want) || fixture.controlComplete || fixture.controlQualified() {
				t.Fatalf("evidence failure accepted as expected rejection: complete=%t control=%v", fixture.controlComplete, fixture.controlErr)
			}
			if !fixture.reaped || !fixture.inactive || fixture.timedOut || fixture.leftOver {
				t.Fatalf("evidence injection changed process custody: reaped=%t inactive=%t timeout=%t leftover=%t", fixture.reaped, fixture.inactive, fixture.timedOut, fixture.leftOver)
			}
		})
	}
}

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
	capabilityCases := []struct {
		name     string
		value    string
		accepted bool
	}{
		{"capabilities-bare", `["CHOWN","DAC_READ_SEARCH"]`, true},
		{"capabilities-prefixed", `["CAP_DAC_READ_SEARCH","CAP_CHOWN"]`, true},
		{"capabilities-mixed", `["CAP_CHOWN","DAC_READ_SEARCH"]`, true},
		{"capabilities-mixed-reverse", `["CHOWN","CAP_DAC_READ_SEARCH"]`, true},
		{"wrong-init-caps-missing", `["CAP_CHOWN"]`, false},
		{"wrong-init-caps-empty", `[]`, false},
		{"wrong-init-caps-extra", `["CHOWN","DAC_READ_SEARCH","CAP_NET_ADMIN"]`, false},
		{"wrong-init-caps-unknown", `["CHOWN","CAP_UNKNOWN"]`, false},
		{"wrong-init-caps-duplicate", `["CHOWN","DAC_READ_SEARCH","CHOWN"]`, false},
		{"wrong-init-caps-duplicate-alias", `["CHOWN","CAP_CHOWN","CAP_DAC_READ_SEARCH"]`, false},
		{"wrong-init-caps-nonstring", `["CAP_CHOWN",42]`, false},
		{"wrong-init-caps-null-element", `["CAP_CHOWN",null]`, false},
		{"wrong-init-caps-null", `null`, false},
		{"wrong-init-caps-string", `"CHOWN,DAC_READ_SEARCH"`, false},
		{"wrong-init-caps-object", `{"CHOWN":true,"DAC_READ_SEARCH":true}`, false},
		{"wrong-init-caps-object-bare-values", `{"a":"CHOWN","b":"DAC_READ_SEARCH"}`, false},
		{"wrong-init-caps-object-prefixed-values", `{"a":"CAP_CHOWN","b":"CAP_DAC_READ_SEARCH"}`, false},
		{"wrong-init-caps-repeated-prefix", `["CAP_CAP_CHOWN","DAC_READ_SEARCH"]`, false},
		{"wrong-init-caps-lowercase-prefix", `["cap_CHOWN","DAC_READ_SEARCH"]`, false},
		{"wrong-init-caps-malformed", `["CAP_ CHOWN","DAC_READ_SEARCH"]`, false},
		{"wrong-init-caps-empty-name", `["CAP_","DAC_READ_SEARCH"]`, false},
		{"wrong-init-caps-trailing-space", `["CAP_CHOWN ","DAC_READ_SEARCH"]`, false},
	}
	oomCases := []struct {
		name      string
		initial   string
		completed string
		accepted  bool
		started   bool
	}{
		{"oom-false-null", `false`, `null`, true, true},
		{"oom-null-false", `null`, `false`, true, true},
		{"oom-absent-false", `absent`, `false`, true, true},
		{"oom-false-absent", `false`, `absent`, true, true},
		{"oom-true-stable", `true`, `true`, true, true},
		{"wrong-init-oom-false-true", `false`, `true`, false, true},
		{"wrong-init-oom-null-true", `null`, `true`, false, true},
		{"wrong-init-oom-true-false", `true`, `false`, false, true},
		{"wrong-init-oom-initial-string", `"false"`, `"false"`, false, false},
		{"wrong-init-oom-initial-number", `0`, `0`, false, false},
		{"wrong-init-oom-initial-object", `{}`, `{}`, false, false},
		{"wrong-init-oom-initial-array", `[]`, `[]`, false, false},
		{"wrong-init-oom-poststart-string", `false`, `"false"`, false, true},
		{"wrong-init-oom-poststart-number", `false`, `0`, false, true},
		{"wrong-init-oom-poststart-object", `false`, `{}`, false, true},
		{"wrong-init-oom-poststart-array", `false`, `[]`, false, true},
	}
	for _, profile := range []string{"security", "volume"} {
		failures := []string{"none", "init-failure", "client-disconnect", "INT", "TERM", "launch-INT", "launch-TERM", "wrong-init-image", "wrong-init-user", "wrong-init-env", "wrong-init-mount", "wrong-init-label", "wrong-init-exit", "stop-failure", "remove-failure"}
		if profile == "security" {
			failures = append(failures,
				"empty-mount-mechanisms", "subscriber-buffer-explicit",
				"wrong-original-binds-missing", "wrong-original-binds-null", "wrong-original-binds-empty",
				"wrong-original-binds-object", "wrong-original-binds-string", "wrong-original-binds-nonstring",
				"wrong-original-binds-empty-string", "wrong-original-binds-cr", "wrong-original-binds-lf", "wrong-original-binds-nul",
				"wrong-original-structured-mounts", "wrong-original-structured-mounts-object",
				"wrong-original-config-copy-missing", "wrong-original-config-copy-false", "wrong-original-config-copy-null", "wrong-original-config-copy-string",
				"wrong-original-volume-request-missing", "wrong-original-volume-request-null", "wrong-original-volume-request-string",
				"wrong-original-volume-request-extra", "wrong-original-volume-request-duplicate", "wrong-original-volume-request-source", "wrong-original-volume-request-target",
				"wrong-original-volume-request-copy-missing", "wrong-original-volume-request-copy-false", "wrong-original-volume-request-copy-null", "wrong-original-volume-request-copy-string",
				"wrong-original-volume-request-driver", "wrong-original-volume-request-subpath", "wrong-original-volume-request-extra-field",
				"wrong-original-binds-legacy-volume",
				"wrong-original-volumes-from", "wrong-original-volumes-from-string",
				"wrong-original-tmpfs", "wrong-original-tmpfs-array", "wrong-original-tmpfs-string", "wrong-original-tmpfs-boolean",
				"wrong-original-anonymous-volumes", "wrong-original-anonymous-volumes-array",
				"wrong-init-mount-source", "wrong-init-mount-mode", "wrong-init-mount-rw",
				"wrong-init-mount-destination", "wrong-init-mount-type", "wrong-init-mount-volume-name",
				"wrong-init-mount-volume-source", "wrong-init-mount-driver", "wrong-init-mount-propagation",
				"wrong-init-mount-extra-field", "wrong-init-mount-missing", "wrong-init-mount-extra", "wrong-init-mount-duplicate-target",
				"wrong-init-poststart-mount-source", "wrong-init-poststart-mount-mode",
				"wrong-init-env-empty-unset", "wrong-init-env-explicit-value",
				"wrong-init-volume-request-missing", "wrong-init-volume-request-extra", "wrong-init-volume-request-duplicate", "wrong-init-volume-request-source", "wrong-init-volume-request-target",
				"wrong-init-volume-request-copy-missing", "wrong-init-volume-request-copy-false", "wrong-init-volume-request-copy-null", "wrong-init-volume-request-copy-string",
				"wrong-init-volume-request-driver", "wrong-init-volume-request-subpath", "wrong-init-volume-request-extra-field",
				"wrong-init-binds-legacy-volume", "wrong-init-volume-request-bind",
				"wrong-init-poststart-volume-copy", "wrong-init-poststart-volume-source")
		}
		if profile == "volume" {
			for _, capability := range capabilityCases {
				failures = append(failures, capability.name)
			}
		}
		for _, oom := range oomCases {
			failures = append(failures, oom.name)
		}
		failures = append(failures, "wrong-init-hostconfig-memory", "wrong-init-hostconfig-readonly")
		for _, failure := range failures {
			if profile == "volume" && failure == "wrong-init-env" {
				continue
			}
			capabilities, accepted := `["CHOWN","DAC_READ_SEARCH"]`, failure == "none" || failure == "empty-mount-mechanisms" || failure == "subscriber-buffer-explicit"
			for _, capability := range capabilityCases {
				if failure == capability.name {
					capabilities, accepted = capability.value, capability.accepted
				}
			}
			oomInitial, oomCompleted := "", ""
			refusedBeforeStart := strings.HasPrefix(failure, "wrong-init-") && failure != "wrong-init-exit"
			postStartRefusal := strings.HasPrefix(failure, "wrong-init-hostconfig-") || strings.HasPrefix(failure, "wrong-init-poststart-mount-") || strings.HasPrefix(failure, "wrong-init-poststart-volume-")
			if postStartRefusal {
				refusedBeforeStart = false
			}
			for _, oom := range oomCases {
				if failure == oom.name {
					oomInitial, oomCompleted, accepted = oom.initial, oom.completed, oom.accepted
					refusedBeforeStart = !oom.started
					postStartRefusal = !oom.accepted && oom.started
				}
			}
			t.Run(profile+"/"+failure, func(t *testing.T) {
				const script = `set -euo pipefail
source "$1"
export COMPOSE_PROJECT_NAME=lantern-bench-owned-fixture
TEST_MAIN_PID=$BASHPID
TEST_IMAGE="sha256:$(printf 'a%.0s' {1..64})"
TEST_SUBSCRIBER_BUFFER=''
if [[ "$CASE" == subscriber-buffer-explicit || "$CASE" == wrong-init-env-explicit-value ]]; then TEST_SUBSCRIBER_BUFFER=8192; fi
TEST_SECURITY_CONFIG='{"name":"lantern-bench-owned-fixture","services":{},"volumes":{}}'
for index in 0 1 2; do
 TEST_SECURITY_CONFIG="$(jq -c --arg service "lantern-$index" --arg image "$TEST_IMAGE" --arg buffer "$TEST_SUBSCRIBER_BUFFER" '
  .services[$service]={image:$image,user:"501:20",environment:{LANTERN_AUTH_MODE:"oidc",LANTERN_SECURITY_CONFIG_JSON:("current-json-"+$service),LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER:$buffer},
   volumes:[{type:"volume",source:("state-"+$service),target:"/state",volume:{nocopy:true}},{type:"volume",source:("backup-"+$service),target:"/data",volume:{nocopy:true}}]} |
  .volumes["state-"+$service]={name:("state-"+$service)} | .volumes["backup-"+$service]={name:("backup-"+$service)}' <<<"$TEST_SECURITY_CONFIG")"
done
case "$CASE" in
 wrong-original-config-copy-*)
  TEST_SECURITY_CONFIG="$(jq -c --arg case "$CASE" '
   .services["lantern-1"].volumes[0].volume |=
    (if $case=="wrong-original-config-copy-missing" then {} elif $case=="wrong-original-config-copy-false" then {nocopy:false}
     elif $case=="wrong-original-config-copy-null" then {nocopy:null} else {nocopy:"true"} end)' <<<"$TEST_SECURITY_CONFIG")";;
esac
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
  local arg next="" name="" nonce_value="" env_file="" user_value="" network_value="" kind_value="" service_value=""
  local -a supplied_mounts=() supplied_volumes=()
  for arg in "$@"; do
   case "$arg" in --volumes-from|--volumes-from=*) return 88;; --volume=*) supplied_mounts+=("${arg#*=}");; --mount=*) supplied_volumes+=("${arg#*=}");; esac
   case "$next" in
    --name) name="$arg";; --user) user_value="$arg";; --network) network_value="$arg";; --env-file) env_file="$arg";;
    --label) case "$arg" in lantern.initializer=*) nonce_value="${arg#*=}";; lantern.owner=*) kind_value="${arg#*=}";; com.docker.compose.service=*) service_value="${arg#*=}";; esac;;
   esac
   next="$arg"
  done
  local i=0
  if [[ "$PROFILE" == security ]]; then
   i="${service_value##*-}"
   [[ "$name" == "$COMPOSE_PROJECT_NAME-$service_value-security-init" && "$user_value" == 501:20 && "$network_value" == "$COMPOSE_PROJECT_NAME-default" && "$kind_value" == compose-security-initializer ]] || return 88
   [[ "${#supplied_mounts[@]}" == 2 && "${supplied_mounts[0]}" == "/private/$service_value:/run/lantern-config:ro" &&
      "${supplied_mounts[1]}" == "/private/$service_value/ntp.conf:/etc/ntp.conf:ro" && "${#supplied_volumes[@]}" == 2 &&
      "${supplied_volumes[0]}" == "type=volume,source=state-$service_value,target=/state,volume-nocopy" &&
      "${supplied_volumes[1]}" == "type=volume,source=backup-$service_value,target=/data,volume-nocopy" ]] || return 88
   [[ "$(cat "$env_file")" == $'LANTERN_AUTH_MODE=oidc\nLANTERN_SECURITY_CONFIG_JSON=current-json-'"$service_value"$'\nLANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER='"$TEST_SUBSCRIBER_BUFFER" ]] || return 88
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
   if [[ "$i" == "$fail_index" && ( "$CASE" == client-disconnect || "$CASE" == stop-failure || "$CASE" == INT || "$CASE" == TERM || "$CASE" == launch-INT || "$CASE" == launch-TERM ) ]]; then
    printf 'running\n' > "$OUTDIR/state-$i"
    if [[ "$CASE" == INT || "$CASE" == TERM || "$CASE" == launch-* ]]; then
     printf '%s\n' "$BASHPID" > "$OUTDIR/client-pid"
     if [[ "$CASE" != launch-* ]]; then kill -"$CASE" "$TEST_MAIN_PID"; fi
     while :; do command sleep 1; done
    fi
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
 jq -nc --arg project "$COMPOSE_PROJECT_NAME" --arg svc "$svc" --arg image "$TEST_IMAGE" --arg case "$CASE" --arg profile "$PROFILE" --arg state "$state" --arg nonce "$nonce_value" --arg user "$user_value" --arg buffer "$TEST_SUBSCRIBER_BUFFER" --argjson i "$i" --argjson fail_index "$fail_index" --argjson initialized "$initialized" --argjson capabilities "$CAP_ADD_JSON" --arg oom_initial "$OOM_INITIAL_JSON" --arg oom_completed "$OOM_COMPLETED_JSON" '
  {Id:((if $initialized then $i+11 else $i+1 end)|tostring|("0"*(64-length)) + .),
   Name:("/"+$project+"-"+$svc+(if $profile=="security" then "-security-init" else "" end)),
   Config:{Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$svc},Image:$image,User:$user,WorkingDir:"/app",
    Env:["LANTERN_AUTH_MODE=oidc",("LANTERN_SECURITY_CONFIG_JSON=current-json-"+$svc),("LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER="+$buffer)],Entrypoint:["/app/lantern"],Cmd:["init-security"],Volumes:null},Image:$image,
   State:{Status:$state,Running:($state=="running"),ExitCode:0},HostConfig:{NetworkMode:($project+"-default"),
    Binds:[("/private/"+$svc+":/run/lantern-config:ro"),("/private/"+$svc+"/ntp.conf:/etc/ntp.conf:ro")],
    Mounts:[{Type:"volume",Source:("state-"+$svc),Target:"/state",VolumeOptions:{NoCopy:true}},{Type:"volume",Source:("backup-"+$svc),Target:"/data",VolumeOptions:{NoCopy:true}}],VolumesFrom:null,Tmpfs:null},
   Mounts:[{Type:"bind",Source:("/private/"+$svc),Destination:"/run/lantern-config",Mode:"ro",RW:false,Propagation:"rprivate"},
    {Type:"bind",Source:("/private/"+$svc+"/ntp.conf"),Destination:"/etc/ntp.conf",Mode:"ro",RW:false,Propagation:"rprivate"},
    {Type:"volume",Name:("state-"+$svc),Source:("/var/lib/docker/volumes/state-"+$svc+"/_data"),Destination:"/state",Driver:"local",Mode:"z",RW:true,Propagation:""},
    {Type:"volume",Name:("backup-"+$svc),Source:("/var/lib/docker/volumes/backup-"+$svc+"/_data"),Destination:"/data",Driver:"local",Mode:"z",RW:true,Propagation:""}]} |
  if $case=="empty-mount-mechanisms" and ($initialized|not) then .HostConfig.VolumesFrom=[] | .HostConfig.Tmpfs={} | .Config.Volumes={} else . end |
  if ($initialized|not) and $i==$fail_index then
   if $case=="wrong-original-binds-missing" then del(.HostConfig.Binds)
   elif $case=="wrong-original-binds-null" then .HostConfig.Binds=null
   elif $case=="wrong-original-binds-empty" then .HostConfig.Binds=[]
   elif $case=="wrong-original-binds-object" then .HostConfig.Binds={a:.HostConfig.Binds[0]}
   elif $case=="wrong-original-binds-string" then .HostConfig.Binds=.HostConfig.Binds[0]
   elif $case=="wrong-original-binds-nonstring" then .HostConfig.Binds[0]=42
   elif $case=="wrong-original-binds-empty-string" then .HostConfig.Binds[0]=""
   elif $case=="wrong-original-binds-cr" then .HostConfig.Binds[0]+="\rforeign"
   elif $case=="wrong-original-binds-lf" then .HostConfig.Binds[0]+="\nforeign"
   elif $case=="wrong-original-binds-nul" then .HostConfig.Binds[0]+="\u0000foreign"
   elif $case=="wrong-original-structured-mounts" then .HostConfig.Mounts=[{Type:"bind",Source:"/foreign",Target:"/foreign"}]
   elif $case=="wrong-original-structured-mounts-object" then .HostConfig.Mounts={}
   elif $case=="wrong-original-volume-request-missing" then del(.HostConfig.Mounts)
   elif $case=="wrong-original-volume-request-null" then .HostConfig.Mounts=null
   elif $case=="wrong-original-volume-request-string" then .HostConfig.Mounts="foreign"
   elif $case=="wrong-original-volume-request-extra" then .HostConfig.Mounts+=[{Type:"volume",Source:"foreign",Target:"/foreign",VolumeOptions:{NoCopy:true}}]
   elif $case=="wrong-original-volume-request-duplicate" then .HostConfig.Mounts[1]=.HostConfig.Mounts[0]
   elif $case=="wrong-original-volume-request-source" then .HostConfig.Mounts[0].Source="foreign"
   elif $case=="wrong-original-volume-request-target" then .HostConfig.Mounts[0].Target="/foreign"
   elif $case=="wrong-original-volume-request-copy-missing" then del(.HostConfig.Mounts[0].VolumeOptions.NoCopy)
   elif $case=="wrong-original-volume-request-copy-false" then .HostConfig.Mounts[0].VolumeOptions.NoCopy=false
   elif $case=="wrong-original-volume-request-copy-null" then .HostConfig.Mounts[0].VolumeOptions.NoCopy=null
   elif $case=="wrong-original-volume-request-copy-string" then .HostConfig.Mounts[0].VolumeOptions.NoCopy="true"
   elif $case=="wrong-original-volume-request-driver" then .HostConfig.Mounts[0].VolumeOptions.DriverConfig={Name:"foreign"}
   elif $case=="wrong-original-volume-request-subpath" then .HostConfig.Mounts[0].VolumeOptions.Subpath="foreign"
   elif $case=="wrong-original-volume-request-extra-field" then .HostConfig.Mounts[0].ReadOnly=false
   elif $case=="wrong-original-binds-legacy-volume" then .HostConfig.Binds+=[("state-"+$svc+":/state:rw")]
   elif $case=="wrong-original-volumes-from" then .HostConfig.VolumesFrom=["foreign"]
   elif $case=="wrong-original-volumes-from-string" then .HostConfig.VolumesFrom="foreign"
   elif $case=="wrong-original-tmpfs" then .HostConfig.Tmpfs={"/foreign":"rw"}
   elif $case=="wrong-original-tmpfs-array" then .HostConfig.Tmpfs=[]
   elif $case=="wrong-original-tmpfs-string" then .HostConfig.Tmpfs="/foreign:rw"
   elif $case=="wrong-original-tmpfs-boolean" then .HostConfig.Tmpfs=false
   elif $case=="wrong-original-anonymous-volumes" then .Config.Volumes={"/anonymous":{}}
   elif $case=="wrong-original-anonymous-volumes-array" then .Config.Volumes=[]
   else . end
  else . end |
  if $initialized then .Config.Labels += {"lantern.owner":("compose-"+$profile+"-initializer"),"com.docker.compose.oneoff":"True","lantern.initializer":$nonce} else . end |
  if $profile=="volume" then .Config.Entrypoint=["/bin/sh"] | .Config.Cmd=["-c","fixture-script","fixture","501:20","6"] |
   .HostConfig={NetworkMode:"none",ReadonlyRootfs:true,CapDrop:["ALL"],CapAdd:$capabilities} |
   .Mounts=[range(0;6)|{Name:("owned-"+tostring),Destination:("/owned/"+tostring),RW:true,Type:"volume"}] else . end |
  if $initialized and $i==$fail_index then
   if $case=="wrong-init-image" then .Image="wrong"
   elif $case=="wrong-init-user" then .Config.User="foreign"
   elif $case=="wrong-init-env" then .Config.Env=[]
   elif $case=="wrong-init-env-empty-unset" then .Config.Env|=map(select(startswith("LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER=")|not))
   elif $case=="wrong-init-env-explicit-value" then .Config.Env[-1]="LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER=1"
   elif $case=="wrong-init-mount" then .Mounts=[]
   elif $case=="wrong-init-mount-source" or ($case=="wrong-init-poststart-mount-source" and $state=="exited") then .Mounts[0].Source=("/host_mnt"+.Mounts[0].Source)
   elif $case=="wrong-init-mount-mode" or ($case=="wrong-init-poststart-mount-mode" and $state=="exited") then .Mounts[0].Mode=""
   elif $case=="wrong-init-mount-rw" then .Mounts[0].RW=true
   elif $case=="wrong-init-mount-destination" then .Mounts[0].Destination="/foreign"
   elif $case=="wrong-init-mount-type" then .Mounts[0].Type="volume"
   elif $case=="wrong-init-mount-volume-name" then .Mounts[2].Name="foreign"
   elif $case=="wrong-init-mount-volume-source" then .Mounts[2].Source="/foreign"
   elif $case=="wrong-init-mount-driver" then .Mounts[2].Driver="foreign"
   elif $case=="wrong-init-mount-propagation" then .Mounts[0].Propagation="rshared"
   elif $case=="wrong-init-mount-extra-field" then .Mounts[0].FutureField="foreign"
   elif $case=="wrong-init-mount-missing" then del(.Mounts[0])
   elif $case=="wrong-init-mount-extra" then .Mounts+=[.Mounts[0]]
   elif $case=="wrong-init-mount-duplicate-target" then .Mounts[1].Destination=.Mounts[0].Destination
   elif $case=="wrong-init-volume-request-missing" then del(.HostConfig.Mounts)
   elif $case=="wrong-init-volume-request-extra" then .HostConfig.Mounts+=[{Type:"volume",Source:"foreign",Target:"/foreign",VolumeOptions:{NoCopy:true}}]
   elif $case=="wrong-init-volume-request-duplicate" then .HostConfig.Mounts[1]=.HostConfig.Mounts[0]
   elif $case=="wrong-init-volume-request-source" or ($case=="wrong-init-poststart-volume-source" and $state=="exited") then .HostConfig.Mounts[0].Source="foreign"
   elif $case=="wrong-init-volume-request-target" then .HostConfig.Mounts[0].Target="/foreign"
   elif $case=="wrong-init-volume-request-copy-missing" then del(.HostConfig.Mounts[0].VolumeOptions.NoCopy)
   elif $case=="wrong-init-volume-request-copy-false" or ($case=="wrong-init-poststart-volume-copy" and $state=="exited") then .HostConfig.Mounts[0].VolumeOptions.NoCopy=false
   elif $case=="wrong-init-volume-request-copy-null" then .HostConfig.Mounts[0].VolumeOptions.NoCopy=null
   elif $case=="wrong-init-volume-request-copy-string" then .HostConfig.Mounts[0].VolumeOptions.NoCopy="true"
   elif $case=="wrong-init-volume-request-driver" then .HostConfig.Mounts[0].VolumeOptions.DriverConfig={Name:"foreign"}
   elif $case=="wrong-init-volume-request-subpath" then .HostConfig.Mounts[0].VolumeOptions.Subpath="foreign"
   elif $case=="wrong-init-volume-request-extra-field" then .HostConfig.Mounts[0].ReadOnly=false
   elif $case=="wrong-init-binds-legacy-volume" then .HostConfig.Binds+=[("state-"+$svc+":/state:rw")]
   elif $case=="wrong-init-volume-request-bind" then .HostConfig.Mounts[0].Type="bind"
   elif $case=="wrong-init-label" then .Config.Labels["lantern.owner"]="foreign"
   elif $case=="wrong-init-exit" and $state=="exited" then .State.ExitCode=23 else . end
  else . end |
  if $initialized and $i==$fail_index and $oom_initial!="" then
   (if $state=="created" then $oom_initial else $oom_completed end) as $oom |
   if $oom=="absent" then del(.HostConfig.OomKillDisable) else .HostConfig.OomKillDisable=($oom|fromjson) end
  else . end |
  if $initialized and $i==$fail_index and $case=="wrong-init-hostconfig-memory" then
   .HostConfig.Memory=(if $state=="created" then 0 else 1024 end)
  elif $initialized and $i==$fail_index and $case=="wrong-init-hostconfig-readonly" then
   .HostConfig.ReadonlyRootfs=(if $state=="created" then $profile=="volume" else $profile!="volume" end)
  else . end'
}
trap 'printf "old INT\n" >> "$OUTDIR/traps"' INT
trap 'printf "old TERM\n" >> "$OUTDIR/traps"' TERM
if [[ "$CASE" == launch-* ]]; then
 signal_index=12
 if [[ "$PROFILE" == volume ]]; then signal_index=11; fi
 TEST_SIGNAL_ID="$(printf '%064d' "$signal_index")"
 # DEBUG runs before this exact assignment, after the asynchronous command was
 # launched. Pause here until the parent interruption reaches the supervisor,
 # so this tests registration custody deterministically, rather than racing it.
 set -T
 trap '
  if [[ "$BASH_COMMAND" == '\''command_pid=$!'\'' && "${1:-}" == docker && "${2:-}" == start && "${@: -1}" == "$TEST_SIGNAL_ID" && ! -e "$OUTDIR/launch-injected" ]]; then
   : > "$OUTDIR/launch-injected"
   for ((signal_round=0; signal_round<1000; signal_round++)); do
    [[ -s "$OUTDIR/client-pid" ]] && break
    command sleep 0.005
   done
   [[ -s "$OUTDIR/client-pid" ]] || exit 96
   kill -"${CASE#launch-}" "$TEST_MAIN_PID"
   for ((signal_round=0; signal_round<1000; signal_round++)); do
    (( ${initializer_pending_signal:-0} != 0 )) && break
    command sleep 0.005
   done
   (( ${initializer_pending_signal:-0} != 0 )) || exit 96
   : > "$OUTDIR/launch-deferred"
  fi
 ' DEBUG
fi
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
				fixture := runOwnedComposeFixture(t, script, []string{helper}, []string{"CASE=" + failure, "PROFILE=" + profile, "CAP_ADD_JSON=" + capabilities, "OOM_INITIAL_JSON=" + oomInitial, "OOM_COMPLETED_JSON=" + oomCompleted}, 15*time.Second)
				out, output, runErr := fixture.directory, fixture.output, fixture.commandErr
				if !fixture.controlQualified() {
					t.Fatalf("fixture custody failed: complete=%t reaped=%t timeout=%t leftover=%t inactive=%t command=%v control=%v evidence=%s\n%s\n%s", fixture.controlComplete, fixture.reaped, fixture.timedOut, fixture.leftOver, fixture.inactive, runErr, fixture.controlErr, out, output, ownedComposeFixtureDiagnostics(out))
				}
				if (runErr == nil) != accepted {
					t.Fatalf("initializer error=%v output=%s", runErr, output)
				}
				if failure == "INT" || failure == "TERM" || strings.HasPrefix(failure, "launch-") {
					expected := 130
					if strings.HasSuffix(failure, "TERM") {
						expected = 143
					}
					if fixture.exitCode != expected {
						t.Fatalf("interruption status=%d want=%d: %v %s", fixture.exitCode, expected, runErr, output)
					}
					pidRaw, err := os.ReadFile(filepath.Join(out, "client-pid"))
					pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
					if err != nil || parseErr != nil || pid <= 1 || syscall.Kill(pid, 0) != syscall.ESRCH {
						t.Fatalf("registered fake attach was not terminated/reaped: %v %v pid=%q", err, parseErr, pidRaw)
					}
					if strings.HasPrefix(failure, "launch-") {
						if _, err := os.Stat(filepath.Join(out, "launch-deferred")); err != nil {
							t.Fatalf("signal never reached the unregistered launch window: %v", err)
						}
					}
				}
				actions, _ := os.ReadFile(filepath.Join(out, "actions"))
				if strings.HasPrefix(failure, "wrong-original-") {
					if len(actions) != 0 {
						t.Fatalf("unsupported original mount construction reached initializer lifecycle: %s", actions)
					}
					for i := range 3 {
						if _, err := os.Stat(filepath.Join(out, fmt.Sprintf("state-%d", i))); !os.IsNotExist(err) {
							t.Fatalf("unsupported original created initializer %d: %v", i, err)
						}
					}
					return
				}
				failed := 1
				if profile == "volume" {
					failed = 0
				}
				if accepted {
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
					if failure == "client-disconnect" || failure == "INT" || failure == "TERM" || strings.HasPrefix(failure, "launch-") || failure == "stop-failure" {
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
					if refusedBeforeStart && strings.Contains(string(actions), fmt.Sprintf("start %d\n", failed)) {
						t.Fatalf("unverified clone started: %s", actions)
					}
					if strings.HasPrefix(failure, "wrong-init-caps-") {
						for _, action := range []string{"start", "stop", "kill", "rm"} {
							if strings.Contains(string(actions), action+" ") {
								t.Fatalf("unverified capability profile permitted %s: %s", action, actions)
							}
						}
						state, err := os.ReadFile(filepath.Join(out, fmt.Sprintf("state-%d", failed)))
						if err != nil || string(state) != "created\n" {
							t.Fatalf("refused initializer did not retain inactive created state: %v %q", err, state)
						}
						if !strings.Contains(string(output), "initializer identity unverified; container retained without signaling") {
							t.Fatalf("missing unverified initializer custody refusal: %s", output)
						}
					}
					if oomInitial != "" || strings.HasPrefix(failure, "wrong-init-hostconfig-") ||
						strings.HasPrefix(failure, "wrong-init-mount") || strings.HasPrefix(failure, "wrong-init-poststart-mount-") ||
						failure == "wrong-init-env-empty-unset" || failure == "wrong-init-env-explicit-value" {
						started := strings.Contains(string(actions), fmt.Sprintf("start %d\n", failed))
						if started != postStartRefusal {
							t.Fatalf("configuration refusal happened at the wrong lifecycle boundary: started=%t postStart=%t %s", started, postStartRefusal, actions)
						}
						for _, action := range []string{"stop", "kill", "rm"} {
							if strings.Contains(string(actions), fmt.Sprintf("%s %d\n", action, failed)) {
								t.Fatalf("unverified configuration drift permitted %s: %s", action, actions)
							}
						}
						state, err := os.ReadFile(filepath.Join(out, fmt.Sprintf("state-%d", failed)))
						expectedState := "created\n"
						if postStartRefusal {
							expectedState = "exited\n"
						}
						if err != nil || string(state) != expectedState {
							t.Fatalf("refused configuration did not retain inactive state: %v %q want %q", err, state, expectedState)
						}
						if !strings.Contains(string(output), "initializer identity unverified; container retained without signaling") {
							t.Fatalf("missing unverified configuration custody refusal: %s", output)
						}
					}
					if !strings.Contains(string(output), "initializer private evidence retained at ") {
						t.Fatalf("private logs not retained: %s", output)
					}
				}
			})
		}
	}
}

func TestOwnedComposeBindIdentityProof(t *testing.T) {
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"exact", "file-pair", "directory-pair", "two-pairs", "collapse-original", "collapse-clone", "reverse-declared", "native-service-shadow",
		"source-comma", "source-control", "source-quote", "probe-missing-source", "proof-marker-failure",
		"undeclared-pair", "named-volume", "writable-bind", "changed-mode", "changed-propagation", "changed-extra-field", "missing-mount", "extra-mount", "duplicate-target",
		"wrong-image-environment", "wrong-probe-image", "wrong-probe-command", "wrong-probe-entrypoint", "wrong-probe-environment",
		"wrong-probe-network", "wrong-probe-readonly", "wrong-probe-capabilities", "wrong-probe-privileged", "wrong-probe-security-option", "wrong-probe-limit",
		"wrong-probe-third-source", "wrong-probe-extra-field", "wrong-probe-extra-mount", "wrong-probe-writable-mount",
		"wrong-probe-auto-create", "wrong-probe-request-flags", "wrong-probe-request-extra-field", "wrong-probe-request-writable", "wrong-probe-binds",
		"wrong-probe-poststart-source", "wrong-probe-poststart-command", "unequal-identity", "zero-identity", "unsupported-type", "malformed-output", "extra-output", "missing-output",
		"original-before-drift", "clone-before-drift", "original-after-drift", "clone-after-drift", "daemon-drift", "probe-exit", "probe-disconnect", "probe-timeout", "probe-stop-failure", "probe-remove-failure", "proof-retention-failure", "INT", "TERM",
	} {
		t.Run(name, func(t *testing.T) {
			const script = `set -euo pipefail
source "$1"
project=lantern-bench-owned-fixture; service=lantern-0; owner=501:20
export COMPOSE_PROJECT_NAME="$project"
image="sha256:$(printf 'a%.0s' {1..64})"
original_id="$(printf '1%.0s' {1..64})"; clone_id="$(printf '2%.0s' {1..64})"; probe_id="$(printf '3%.0s' {1..64})"
TEST_MAIN_PID=$BASHPID
sleep() { command sleep 0.005; }
original="$(jq -nc --arg project "$project" --arg image "$image" --arg id "$original_id" '
 {Id:$id,Name:("/"+$project+"-lantern-0-1"),Image:$image,
  Config:{User:"501:20",WorkingDir:"/app",Entrypoint:["/app/lantern"],Cmd:null,
   Env:["LANTERN_AUTH_MODE=oidc","LANTERN_SECURITY_CONFIG_JSON=secret-value"],
   Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":"lantern-0"}},
  State:{Status:"created",Running:false},HostConfig:{NetworkMode:($project+"-default"),Memory:0,
   Binds:["/declared/private:/run/lantern-config:ro","/declared/private/ntp.conf:/etc/ntp.conf:ro"],
   Mounts:[{Type:"volume",Source:"state-0",Target:"/state",VolumeOptions:{NoCopy:true}},
    {Type:"volume",Source:"backup-0",Target:"/data",VolumeOptions:{NoCopy:true}}]},
  Mounts:[{Type:"bind",Source:"/declared/private",Destination:"/run/lantern-config",Mode:"ro",RW:false,Propagation:"rprivate"},
   {Type:"bind",Source:"/declared/private/ntp.conf",Destination:"/etc/ntp.conf",Mode:"ro",RW:false,Propagation:"rprivate"},
   {Type:"volume",Name:"state-0",Source:"/var/lib/docker/volumes/state-0/_data",Destination:"/state",Driver:"local",Mode:"z",RW:true,Propagation:""},
   {Type:"volume",Name:"backup-0",Source:"/var/lib/docker/volumes/backup-0/_data",Destination:"/data",Driver:"local",Mode:"z",RW:true,Propagation:""}]}')"
initialized="$(jq -c --arg id "$clone_id" --arg case "$CASE" '
 .Id=$id | .Name+="-security-init" | .Config.Cmd=["init-security"] |
 if $case!="exact" then .Mounts[1].Source="/mapped/private/ntp.conf" else . end |
 if $case=="directory-pair" then .Mounts[1].Source="/declared/private/ntp.conf" | .Mounts[0].Source="/mapped/private"
 elif $case=="two-pairs" then .Mounts[0].Source="/mapped/private"
 elif $case=="named-volume" then .Mounts[1].Source="/declared/private/ntp.conf" | .Mounts[2].Source="/foreign-volume"
 elif $case=="changed-mode" then .Mounts[1].Mode=""
 elif $case=="changed-propagation" then .Mounts[1].Propagation="rshared"
 elif $case=="changed-extra-field" then .Mounts[1].FutureField="unknown"
 elif $case=="source-comma" then .Mounts[1].Source="/mapped/private,other"
 elif $case=="source-control" then .Mounts[1].Source="/mapped/private\tother"
 elif $case=="source-quote" then .Mounts[1].Source="/mapped/private\"other"
 elif $case=="missing-mount" then del(.Mounts[0])
 elif $case=="extra-mount" then .Mounts+=[.Mounts[0]]
 elif $case=="duplicate-target" then .Mounts[0].Destination="/etc/ntp.conf"
 else . end' <<<"$original")"
if [[ "$CASE" == writable-bind ]]; then
 original="$(jq -c '.Mounts[1].RW=true' <<<"$original")"
 initialized="$(jq -c '.Mounts[1].RW=true' <<<"$initialized")"
fi
configuration="$(jq -nc --arg case "$CASE" '
 {services:{"lantern-0":{volumes:[{type:"bind",source:"/declared/private",target:"/run/lantern-config",read_only:true},
  {type:"bind",source:(if $case=="reverse-declared" then "/mapped/private/ntp.conf" elif $case=="undeclared-pair" then "/third/private/ntp.conf" else "/declared/private/ntp.conf" end),target:"/etc/ntp.conf",read_only:true}]}}}')"
printf '%s\n' "$original" > "$OUTDIR/original.json"
printf '%s\n' "$initialized" > "$OUTDIR/clone.json"
if [[ "$CASE" == native-service-shadow ]]; then service=lantern-0-security-init; fi
initializer_command() { "$@"; }
echo() {
 if [[ "$CASE" == proof-marker-failure && "$1" == read-only\ bind\ proof* ]]; then return 21; fi
 command echo "$@"
}
cp() {
 if [[ "$CASE" == proof-retention-failure && "$1" == */created.json ]]; then return 21; fi
 command cp "$@"
}
docker() {
 if [[ "$1" == info ]]; then
  local n=0; [[ ! -f "$OUTDIR/info-count" ]] || read -r n < "$OUTDIR/info-count"
  n=$((n+1)); printf '%s\n' "$n" > "$OUTDIR/info-count"
  if [[ "$CASE" == daemon-drift && "$n" == 2 ]]; then printf 'other-daemon\n'; else printf 'same-daemon\n'; fi
  return
 fi
 if [[ "$1" == image ]]; then
  if [[ "$CASE" == wrong-image-environment ]]; then printf '["LANTERN_SECURITY_CONFIG_JSON=secret-value"]\n'; else printf '["PATH=/usr/bin:/bin"]\n'; fi
  return
 fi
 if [[ "$1" == create ]]; then
  printf 'create\n' >> "$OUTDIR/actions"
  local a previous="" name="" user="" entrypoint="" nonce="" label_service="" label_kind="" args
  local -a bindings=()
  args="$(jq -nc --args '$ARGS.positional' -- "$@")"
  jq -e --arg image "$image" --argjson targets "$bind_probe_targets" '
   index($image) as $i | .[$i+1:]==(["stat","-c","%d:%i:%F"]+$targets) and
   (index("--env-file")==null) and all(.[]; (startswith("--volume") or contains("bind-create-src"))|not) and
   (index("--network") as $n | .[$n+1]=="none") and
   (index("--read-only")!=null) and (index("--cap-drop") as $n | .[$n+1]=="ALL") and
   (index("--security-opt") as $n | .[$n+1]=="no-new-privileges:true") and
   (index("--memory") as $n | .[$n+1]=="32m") and (index("--cpus") as $n | .[$n+1]=="0.25") and
   (index("--pids-limit") as $n | .[$n+1]=="16")' <<<"$args" >/dev/null || return 88
  for a in "$@"; do
   case "$previous" in --name) name="$a";; --user) user="$a";; --entrypoint) entrypoint="$a";; --label)
    case "$a" in lantern.initializer=*) nonce="${a#*=}";; lantern.owner=*) label_kind="${a#*=}";; com.docker.compose.service=*) label_service="${a#*=}";; esac;; esac
   case "$a" in --mount=*) bindings+=("${a#*=}");; esac
   previous="$a"
  done
  [[ "$name" == "$project-lantern-0-bind-proof" && "$user" == 501:20 && "$entrypoint" == /bin/busybox && "$label_service" == lantern-0-bind-proof && "$label_kind" == compose-bind-identity-probe && "$nonce" == lantern-owned-init.* ]] || return 88
  [[ "$(jq -nc --args '$ARGS.positional' -- "${bindings[@]}")" == "$(jq -c '[to_entries[]|.key as $i|.value as $p|
   ("type=bind,source="+$p.original_source+",target=/proof/original-"+($i|tostring)+",readonly,bind-propagation=rprivate"),
   ("type=bind,source="+$p.clone_source+",target=/proof/clone-"+($i|tostring)+",readonly,bind-propagation=rprivate")]' <<<"$bind_proof_plan")" ]] || return 88
  [[ "$CASE" != probe-missing-source ]] || return 83
  jq -nc --arg project "$project" --arg image "$image" --arg id "$probe_id" --arg nonce "$nonce" --argjson targets "$bind_probe_targets" --argjson bindings "$(jq -nc --args '$ARGS.positional' -- "${bindings[@]}")" '
   ($bindings|map(split(",")|{Type:"bind",Source:.[1][7:],Target:.[2][7:],ReadOnly:true,BindOptions:{Propagation:"rprivate"}})) as $requests |
   {Id:$id,Name:("/"+$project+"-lantern-0-bind-proof"),Image:$image,
    Config:{Image:$image,User:"501:20",Entrypoint:["/bin/busybox"],Cmd:(["stat","-c","%d:%i:%F"]+$targets),Env:["PATH=/usr/bin:/bin"],Volumes:null,
     Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":"lantern-0-bind-proof","com.docker.compose.oneoff":"True","lantern.owner":"compose-bind-identity-probe","lantern.initializer":$nonce}},
    HostConfig:{NetworkMode:"none",ReadonlyRootfs:true,Privileged:false,AutoRemove:false,CapDrop:["ALL"],CapAdd:null,SecurityOpt:["no-new-privileges:true"],Memory:33554432,NanoCpus:250000000,PidsLimit:16,
     Binds:null,Mounts:$requests,VolumesFrom:null,Tmpfs:null,Devices:null,DeviceRequests:null,DeviceCgroupRules:null,PidMode:"",UTSMode:"",UsernsMode:"",PublishAllPorts:false,PortBindings:{},RestartPolicy:{Name:"no",MaximumRetryCount:0}},
    Mounts:[$requests[]|{Type:"bind",Source:.Source,Destination:.Target,Mode:"",RW:false,Propagation:"rprivate"}]}' > "$OUTDIR/probe.json"
  printf 'created\n' > "$OUTDIR/probe-state"; printf '%s\n' "$probe_id"; return
 fi
 local target="${@: -1}" state=created
 if [[ "$1" == inspect && ( "$target" == "$original_id" || "$target" == "$clone_id" ) ]]; then
  local role=original n=0; [[ "$target" != "$clone_id" ]] || role=clone
  [[ ! -f "$OUTDIR/$role-count" ]] || read -r n < "$OUTDIR/$role-count"
  n=$((n+1)); printf '%s\n' "$n" > "$OUTDIR/$role-count"
  jq -c --arg case "$CASE" --arg role "$role" --argjson n "$n" '
   if ($case==($role+"-before-drift") and $n==1) or ($case==($role+"-after-drift") and $n==2) then .HostConfig.Memory=1024 else . end' "$OUTDIR/$role.json"
  return
 fi
 [[ "$target" == "$probe_id" || "$target" == "$project-lantern-0-bind-proof" ]] || return 86
 [[ -f "$OUTDIR/probe-state" ]] || return 1
 read -r state < "$OUTDIR/probe-state"
 case "$1" in
  start)
   printf 'start\n' >> "$OUTDIR/actions"
   if [[ "$CASE" == probe-disconnect || "$CASE" == probe-timeout || "$CASE" == probe-stop-failure || "$CASE" == INT || "$CASE" == TERM ]]; then
    printf 'running\n' > "$OUTDIR/probe-state"
    if [[ "$CASE" == INT || "$CASE" == TERM ]]; then kill -"$CASE" "$TEST_MAIN_PID"; while :; do command sleep 1; done; fi
    if [[ "$CASE" == probe-timeout ]]; then while :; do command sleep 1; done; fi
    return 17
   fi
   printf 'exited\n' > "$OUTDIR/probe-state"
   [[ "$CASE" != probe-exit ]] || return 17
   jq -r --arg case "$CASE" '
    to_entries[]|.key as $i|.value.expected_type as $type|
    ("5:"+(($i+100)|tostring)+":"+$type),
    (if $case=="unequal-identity" then "5:999:"+$type elif $case=="zero-identity" then "0:0:"+$type elif $case=="unsupported-type" then "5:100:symbolic link" elif $case=="malformed-output" then "garbage" else "5:"+(($i+100)|tostring)+":"+$type end)
   ' <<<"$bind_proof_plan" > "$OUTDIR/stat-output"
   if [[ "$CASE" == extra-output ]]; then printf 'garbage\n' >> "$OUTDIR/stat-output"; fi
   if [[ "$CASE" == missing-output ]]; then sed '$d' "$OUTDIR/stat-output"; else cat "$OUTDIR/stat-output"; fi
   return;;
  stop) printf 'stop\n' >> "$OUTDIR/actions"; [[ "$CASE" != probe-stop-failure ]] || return 19; printf 'exited\n' > "$OUTDIR/probe-state"; return;;
  kill) printf 'kill\n' >> "$OUTDIR/actions"; printf 'exited\n' > "$OUTDIR/probe-state"; return;;
  wait) printf 'wait\n' >> "$OUTDIR/actions"; printf '0\n'; return;;
  logs) printf 'logs\n' >> "$OUTDIR/actions"; return;;
  rm) printf 'rm\n' >> "$OUTDIR/actions"; [[ "$CASE" != probe-remove-failure ]] || return 19; rm "$OUTDIR/probe-state"; return;;
  inspect) ;;
  *) return 86;;
 esac
 jq -c --arg case "$CASE" --arg state "$state" --argjson plan "$bind_proof_plan" '
  .State={Status:$state,Running:($state=="running"),ExitCode:(if $case=="probe-exit" then 17 else 0 end)} |
  if $case=="collapse-original" or $case=="collapse-clone" then
   .Mounts|=map(. as $m|($m.Destination|capture("-(?<i>[01])$").i|tonumber) as $i|
    .Source=(if $case=="collapse-original" then $plan[$i].original_source else $plan[$i].clone_source end)) |
   .Mounts as $actual | .HostConfig.Mounts|=map(. as $r|.Source=([$actual[]|select(.Destination==$r.Target)][0].Source)) else . end |
  if $case=="wrong-probe-image" then .Image="foreign"
  elif $case=="wrong-probe-command" or ($case=="wrong-probe-poststart-command" and $state=="exited") then .Config.Cmd=["sh"]
  elif $case=="wrong-probe-entrypoint" then .Config.Entrypoint=["/bin/sh"]
  elif $case=="wrong-probe-environment" then .Config.Env+=["LANTERN_SECURITY_CONFIG_JSON=secret-value"]
  elif $case=="wrong-probe-network" then .HostConfig.NetworkMode="host"
  elif $case=="wrong-probe-readonly" then .HostConfig.ReadonlyRootfs=false
  elif $case=="wrong-probe-capabilities" then .HostConfig.CapAdd=["SYS_ADMIN"]
  elif $case=="wrong-probe-privileged" then .HostConfig.Privileged=true
  elif $case=="wrong-probe-security-option" then .HostConfig.SecurityOpt=[]
  elif $case=="wrong-probe-limit" then .HostConfig.Memory=0
  elif $case=="wrong-probe-third-source" or ($case=="wrong-probe-poststart-source" and $state=="exited") then .Mounts[0].Source="/third/path" | .HostConfig.Mounts[0].Source="/third/path"
  elif $case=="wrong-probe-extra-field" then .Mounts[0].FutureField="unknown"
  elif $case=="wrong-probe-extra-mount" then .Mounts+=[.Mounts[0]]
  elif $case=="wrong-probe-writable-mount" then .Mounts[0].RW=true
  elif $case=="wrong-probe-auto-create" then .HostConfig.Mounts[0].BindOptions.CreateMountpoint=true
  elif $case=="wrong-probe-request-flags" then .HostConfig.Mounts[0].BindOptions.NonRecursive=true
  elif $case=="wrong-probe-request-extra-field" then .HostConfig.Mounts[0].FutureField="unknown"
  elif $case=="wrong-probe-request-writable" then .HostConfig.Mounts[0].ReadOnly=false
  elif $case=="wrong-probe-binds" then .HostConfig.Binds=["/other:/proof/original-0:ro"]
  else . end' "$OUTDIR/probe.json"
}
trap 'printf "old INT\n" >> "$OUTDIR/traps"' INT
trap 'printf "old TERM\n" >> "$OUTDIR/traps"' TERM
if validate_owned_compose_security_initializer; then printf 'native-admitted\n' >> "$OUTDIR/actions"; result=0; else result=$?; fi
[[ "$(trap -p INT)" == *"old INT"* && "$(trap -p TERM)" == *"old TERM"* ]] || exit 95
exit "$result"
`
			fixture := runOwnedComposeFixture(t, script, []string{helper}, []string{"CASE=" + name}, 15*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("proof fixture custody failed: %v %s\n%s", fixture.controlErr, fixture.output, ownedComposeFixtureDiagnostics(fixture.directory))
			}
			accepted := name == "exact" || name == "file-pair" || name == "directory-pair" || name == "two-pairs" || name == "collapse-original" || name == "collapse-clone" || name == "reverse-declared" || name == "native-service-shadow"
			if (fixture.commandErr == nil) != accepted {
				t.Fatalf("admission=%v accepted=%t: %s", fixture.commandErr, accepted, fixture.output)
			}
			actions, _ := os.ReadFile(filepath.Join(fixture.directory, "actions"))
			if strings.Contains(string(fixture.output), "secret-value") {
				t.Fatal("owner credential data appeared in diagnostics")
			}
			if strings.Contains(string(actions), "native-admitted") != accepted {
				t.Fatalf("unproved native admission: %s", actions)
			}
			if name == "exact" {
				if string(actions) != "native-admitted\n" {
					t.Fatalf("exact fast path invoked a probe: %s", actions)
				}
				return
			}
			if strings.Count(string(actions), "create\n") > 1 {
				t.Fatalf("proof retried: %s", actions)
			}
			beforeProbe := name == "undeclared-pair" || name == "named-volume" || name == "writable-bind" ||
				strings.HasPrefix(name, "changed-") || name == "missing-mount" || name == "extra-mount" || name == "duplicate-target" ||
				strings.HasPrefix(name, "source-") || name == "proof-marker-failure" ||
				name == "wrong-image-environment" || name == "original-before-drift" || name == "clone-before-drift"
			if beforeProbe && strings.Contains(string(actions), "create\n") {
				t.Fatalf("unrelated owner/mount difference reached a probe: %s", actions)
			}
			if strings.HasPrefix(name, "wrong-probe-") && !strings.HasPrefix(name, "wrong-probe-poststart-") {
				state, err := os.ReadFile(filepath.Join(fixture.directory, "probe-state"))
				if strings.Count(string(actions), "create\n") != 1 || err != nil || string(state) != "created\n" {
					t.Fatalf("probe admission refusal did not reach the created object: %v %q %s", err, state, actions)
				}
				for _, action := range []string{"start", "stop", "kill", "rm"} {
					if strings.Contains(string(actions), action+"\n") {
						t.Fatalf("unverified probe permitted %s: %s", action, actions)
					}
				}
			}
			if name == "probe-missing-source" && strings.Contains(string(actions), "start\n") {
				t.Fatalf("missing source reached probe start: %s", actions)
			}
			if !beforeProbe && !strings.HasPrefix(name, "wrong-probe-") && name != "probe-missing-source" || strings.HasPrefix(name, "wrong-probe-poststart-") {
				if strings.Count(string(actions), "start\n") != 1 {
					t.Fatalf("post-start proof contract refused at an earlier boundary: %s", actions)
				}
			}
			if strings.HasPrefix(name, "wrong-probe-poststart-") {
				for _, action := range []string{"stop", "kill", "rm"} {
					if strings.Contains(string(actions), action+"\n") {
						t.Fatalf("unverified probe drift authorized %s: %s", action, actions)
					}
				}
			}
			if name == "probe-disconnect" || name == "probe-timeout" || name == "probe-stop-failure" || name == "INT" || name == "TERM" {
				for _, action := range []string{"stop", "wait", "logs"} {
					if !strings.Contains(string(actions), action+"\n") {
						t.Fatalf("owned probe lacked failure custody %s: %s", action, actions)
					}
				}
				if name == "probe-stop-failure" && !strings.Contains(string(actions), "kill\n") {
					t.Fatalf("owned failed stop lacked kill fallback: %s", actions)
				}
				state, err := os.ReadFile(filepath.Join(fixture.directory, "probe-state"))
				if err != nil || string(state) != "exited\n" {
					t.Fatalf("probe inactivity unproved: %v %q", err, state)
				}
			}
			if name == "INT" || name == "TERM" {
				expected := 130
				if name == "TERM" {
					expected = 143
				}
				if fixture.exitCode != expected {
					t.Fatalf("probe interruption=%d want=%d", fixture.exitCode, expected)
				}
			}
			if accepted {
				if strings.Count(string(actions), "start\n") != 1 || strings.Count(string(actions), "rm\n") != 1 {
					t.Fatalf("missing bounded probe lifecycle: %s", actions)
				}
				dirs, _ := filepath.Glob(filepath.Join(fixture.directory, "lantern-bind-proof.*"))
				if len(dirs) != 1 {
					t.Fatalf("proof evidence not retained: %v", dirs)
				}
				for _, file := range []string{"requested-pairs.json", "original-before.json", "clone-before.json", "original-after.json", "clone-after.json", "probe-created.json", "probe-completed.json", "probe-output.log", "identity.json", "proof.json"} {
					info, err := os.Lstat(filepath.Join(dirs[0], file))
					if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
						t.Fatalf("missing private proof %s: %v", file, err)
					}
				}
			}
		})
	}
}
