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
	for _, profile := range []string{"security", "volume"} {
		for _, failure := range []string{"none", "init-failure", "client-disconnect", "INT", "TERM", "launch-INT", "launch-TERM", "wrong-init-image", "wrong-init-user", "wrong-init-env", "wrong-init-mount", "wrong-init-label", "wrong-init-exit", "stop-failure", "remove-failure"} {
			if profile == "volume" && failure == "wrong-init-env" {
				continue
			}
			t.Run(profile+"/"+failure, func(t *testing.T) {
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
				fixture := runOwnedComposeFixture(t, script, []string{helper}, []string{"CASE=" + failure, "PROFILE=" + profile}, 15*time.Second)
				out, output, runErr := fixture.directory, fixture.output, fixture.commandErr
				if !fixture.controlQualified() {
					t.Fatalf("fixture custody failed: complete=%t reaped=%t timeout=%t leftover=%t inactive=%t command=%v control=%v evidence=%s\n%s\n%s", fixture.controlComplete, fixture.reaped, fixture.timedOut, fixture.leftOver, fixture.inactive, runErr, fixture.controlErr, out, output, ownedComposeFixtureDiagnostics(out))
				}
				if (runErr == nil) != (failure == "none") {
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
