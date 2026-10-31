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
	"sync"
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
	var signalMu sync.Mutex
	signals := [3]ownedComposeSignalObservation{{Phase: "context_cancel"}, {Phase: "cleanup_term"}, {Phase: "cleanup_kill"}}
	recordSignal := func(index int, err error) {
		signalMu.Lock()
		defer signalMu.Unlock()
		signals[index].Attempted, signals[index].Result = true, ownedComposeSyscallObservation(err)
	}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		recordSignal(0, err)
		if err == syscall.ESRCH {
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
		waits := [3]ownedComposeGroupObservation{{Phase: "natural_drain", BudgetMS: 1500}, {Phase: "after_term", BudgetMS: 1000}, {Phase: "final", BudgetMS: 2000}}
		if !waitOwnedComposeGroup(group, 1500*time.Millisecond, &waits[0]) {
			result.leftOver = true
			err := syscall.Kill(-group, syscall.SIGTERM)
			recordSignal(1, err)
			if err != nil && err != syscall.ESRCH {
				result.controlErr = errors.Join(result.controlErr, err)
			}
			if !waitOwnedComposeGroup(group, time.Second, &waits[1]) {
				err := syscall.Kill(-group, syscall.SIGKILL)
				recordSignal(2, err)
				if err != nil && err != syscall.ESRCH {
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
		result.inactive = waitOwnedComposeGroup(group, 2*time.Second, &waits[2])
		result.reaped = reaped
		if reaped && cmd.ProcessState != nil {
			result.exitCode = cmd.ProcessState.ExitCode()
		}
		signalMu.Lock()
		signalResults := signals
		signalMu.Unlock()
		terminal := map[string]any{"pid": group, "pgid": group, "inactive": result.inactive,
			"direct_reaped": reaped, "timed_out": result.timedOut, "left_over": result.leftOver, "exit_code": result.exitCode,
			"group_waits": waits, "signals": signalResults}
		if !result.inactive || !reaped {
			result.controlErr = errors.Join(result.controlErr, errors.New("fixture process custody incomplete"))
			// One bounded, metadata-only observation follows the failed proof. It
			// cannot revise that verdict, even if the group disappears meanwhile.
			terminal["process_observation"] = observeOwnedComposeProcesses(group)
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

type ownedComposeSyscallResult struct {
	Outcome    string `json:"outcome"`
	Errno      int    `json:"errno"`
	ErrnoKnown bool   `json:"errno_known"`
}

func ownedComposeSyscallObservation(err error) ownedComposeSyscallResult {
	if err == nil {
		return ownedComposeSyscallResult{Outcome: "success", ErrnoKnown: true}
	}
	outcome := "error"
	if err == syscall.ESRCH {
		outcome = "esrch"
	}
	var errno syscall.Errno
	known := errors.As(err, &errno)
	return ownedComposeSyscallResult{Outcome: outcome, Errno: int(errno), ErrnoKnown: known}
}

type ownedComposeSignalObservation struct {
	Phase     string                    `json:"phase"`
	Attempted bool                      `json:"attempted"`
	Result    ownedComposeSyscallResult `json:"result"`
}

type ownedComposeGroupObservation struct {
	Attempted bool                      `json:"attempted"`
	Phase     string                    `json:"phase"`
	BudgetMS  int64                     `json:"budget_ms"`
	ElapsedMS int64                     `json:"elapsed_ms"`
	Polls     int                       `json:"polls"`
	Inactive  bool                      `json:"inactive"`
	LastProbe ownedComposeSyscallResult `json:"last_probe"`
}

func waitOwnedComposeGroup(group int, budget time.Duration, observation *ownedComposeGroupObservation) bool {
	started := time.Now()
	deadline := started.Add(budget)
	observation.Attempted, observation.BudgetMS = true, budget.Milliseconds()
	for {
		err := syscall.Kill(-group, 0)
		observation.Polls++
		observation.LastProbe = ownedComposeSyscallObservation(err)
		if err == syscall.ESRCH {
			observation.Inactive, observation.ElapsedMS = true, time.Since(started).Milliseconds()
			return true
		}
		now := time.Now()
		if now.After(deadline) {
			observation.ElapsedMS = now.Sub(started).Milliseconds()
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const (
	ownedComposeProcessBytes = 64 << 10
	ownedComposeProcessRows  = 64
)

type ownedComposeProcessRow struct {
	PID        int    `json:"pid"`
	PPID       int    `json:"ppid"`
	PGID       int    `json:"pgid"`
	UID        int    `json:"uid"`
	State      string `json:"state"`
	StartLocal string `json:"start_local"`
}

type ownedComposeProcessObservation struct {
	Status          string                   `json:"status"`
	StartedUTC      string                   `json:"started_utc"`
	ElapsedMS       int64                    `json:"elapsed_ms"`
	OutputTruncated bool                     `json:"output_truncated"`
	RowLimit        bool                     `json:"row_limit"`
	MalformedRows   int                      `json:"malformed_rows"`
	Rows            []ownedComposeProcessRow `json:"rows"`
}

// Retain at most the byte allowance without blocking ps on an unread pipe or
// treating truncated output as a complete process observation.
type ownedComposeProcessOutput struct {
	data      []byte
	truncated bool
}

func (output *ownedComposeProcessOutput) Write(data []byte) (int, error) {
	n := len(data)
	retained := min(n, ownedComposeProcessBytes-len(output.data))
	output.data = append(output.data, data[:retained]...)
	output.truncated = output.truncated || retained != n
	return n, nil
}

// ps uses fixed numeric/state/start columns only. Unrelated groups and raw
// output are never retained; malformed, empty and truncated observations prove
// no inactivity. StartLocal is ps's local wall time, not qualified UTC.
func parseOwnedComposeProcesses(raw []byte, group int) ownedComposeProcessObservation {
	result := ownedComposeProcessObservation{Status: "empty_unknown"}
	if group <= 0 {
		result.Status = "invalid_scope"
		return result
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 {
			result.MalformedRows++
			continue
		}
		pgid, err := strconv.Atoi(fields[2])
		if err != nil {
			result.MalformedRows++
			continue
		}
		if pgid != group {
			continue
		}
		if len(fields) != 10 {
			result.MalformedRows++
			continue
		}
		row := ownedComposeProcessRow{PGID: pgid, State: fields[4], StartLocal: strings.Join(fields[5:], " ")}
		var pidErr, ppidErr, uidErr error
		row.PID, pidErr = strconv.Atoi(fields[0])
		row.PPID, ppidErr = strconv.Atoi(fields[1])
		row.UID, uidErr = strconv.Atoi(fields[3])
		_, startErr := time.Parse("Mon Jan 2 15:04:05 2006", row.StartLocal)
		stateValid := row.State != "" && len(row.State) <= 16
		for _, character := range row.State {
			if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || strings.ContainsRune("+<>", character) {
				continue
			}
			stateValid = false
		}
		if pidErr != nil || ppidErr != nil || uidErr != nil || row.PID <= 0 || row.PPID < 0 || row.UID < 0 || !stateValid || startErr != nil {
			result.MalformedRows++
			continue
		}
		if len(result.Rows) == ownedComposeProcessRows {
			result.RowLimit = true
			continue
		}
		result.Rows = append(result.Rows, row)
	}
	switch {
	case result.RowLimit:
		result.Status = "row_limit"
	case result.MalformedRows != 0:
		result.Status = "parse_error"
	case len(result.Rows) != 0:
		result.Status = "observed"
	}
	return result
}

func observeOwnedComposeProcesses(group int) ownedComposeProcessObservation {
	started := time.Now()
	if group <= 0 {
		return ownedComposeProcessObservation{Status: "invalid_scope"}
	}
	// This independent observation allowance never extends fixture execution or
	// any signal/reap/inactivity wait. ps itself has no shell or descendant work.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,pgid=,uid=,stat=,lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var output ownedComposeProcessOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	cmd.WaitDelay = 250 * time.Millisecond
	err := cmd.Run()
	result := parseOwnedComposeProcesses(output.data, group)
	result.StartedUTC, result.ElapsedMS = started.UTC().Format(time.RFC3339Nano), time.Since(started).Milliseconds()
	result.OutputTruncated = output.truncated
	switch {
	case err != nil && ctx.Err() != nil:
		result.Status = "timeout"
	case err != nil:
		result.Status = "command_error"
	case output.truncated:
		result.Status = "output_truncated"
	}
	return result
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

func TestOwnedComposeFixtureSyscallDiagnosticsKeepESRCHOnlyProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want ownedComposeSyscallResult
	}{
		{"success", nil, ownedComposeSyscallResult{Outcome: "success", ErrnoKnown: true}},
		{"absent", syscall.ESRCH, ownedComposeSyscallResult{Outcome: "esrch", Errno: int(syscall.ESRCH), ErrnoKnown: true}},
		{"permission", syscall.EPERM, ownedComposeSyscallResult{Outcome: "error", Errno: int(syscall.EPERM), ErrnoKnown: true}},
		{"interrupted", syscall.EINTR, ownedComposeSyscallResult{Outcome: "error", Errno: int(syscall.EINTR), ErrnoKnown: true}},
		{"wrapped absent is not the original predicate", fmt.Errorf("wrapped: %w", syscall.ESRCH), ownedComposeSyscallResult{Outcome: "error", Errno: int(syscall.ESRCH), ErrnoKnown: true}},
		{"unknown", errors.New("never-retain-arbitrary-error-text"), ownedComposeSyscallResult{Outcome: "error"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ownedComposeSyscallObservation(tc.err)
			if got != tc.want || (got.Outcome == "esrch") != (tc.err == syscall.ESRCH) {
				t.Fatalf("syscall observation changed proof: got=%+v want=%+v", got, tc.want)
			}
		})
	}
}

func TestOwnedComposeFixtureProcessObservationIsScopedAndNonAuthoritative(t *testing.T) {
	const group = 42000
	const start = "Fri Jan 2 03:04:05 2026"
	const owned = "42001 1 42000 501 Z " + start + "\n"
	for _, tc := range []struct {
		name, raw, status string
		rows, malformed   int
	}{
		{"matching Z-state row", owned, "observed", 1, 0},
		{"unrelated metadata excluded", "43000 1 43000 999 private-other-group never-copy\n" + owned, "observed", 1, 0},
		{"no rows is unknown", "", "empty_unknown", 0, 0},
		{"other group only", "43000 1 43000 999 S " + start + "\n", "empty_unknown", 0, 0},
		{"partial row", "42001 1\n", "parse_error", 0, 1},
		{"malformed group", "42001 1 unknown 501 S " + start + "\n", "parse_error", 0, 1},
		{"malformed identity", "42001 1 42000 unknown S " + start + "\n", "parse_error", 0, 1},
		{"no process command retained", "42001 1 42000 501 S private-command-arguments\n", "parse_error", 0, 1},
		{"malformed start", "42001 1 42000 501 S Fri Invalid 2 03:04:05 2026\n", "parse_error", 0, 1},
		{"malformed state", "42001 1 42000 501 S/private " + start + "\n", "parse_error", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOwnedComposeProcesses([]byte(tc.raw), group)
			if got.Status != tc.status || len(got.Rows) != tc.rows || got.MalformedRows != tc.malformed {
				t.Fatalf("process observation = %+v; want status=%s rows=%d malformed=%d", got, tc.status, tc.rows, tc.malformed)
			}
			if len(got.Rows) != 0 && (got.Rows[0].PID != 42001 || got.Rows[0].PGID != group || got.Rows[0].UID != 501 || got.Rows[0].State != "Z" || got.Rows[0].StartLocal != start) {
				t.Fatal("metadata from another group was retained", got)
			}
			raw, err := json.Marshal(got)
			if err != nil || strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "never-copy") {
				t.Fatal("observation retained non-metadata fields", err)
			}
			// Neither a Z row nor an empty post-failure observation can override
			// the independently failed process-group inactivity predicate.
			fixture := ownedComposeFixtureResult{controlComplete: true, reaped: true}
			if fixture.controlQualified() {
				t.Fatal("metadata observation qualified failed inactivity")
			}
		})
	}
	if got := parseOwnedComposeProcesses([]byte(owned), 0); got.Status != "invalid_scope" || len(got.Rows) != 0 {
		t.Fatal("invalid group scope admitted metadata", got)
	}
	var rows strings.Builder
	for index := range ownedComposeProcessRows + 1 {
		fmt.Fprintf(&rows, "%d 1 %d 501 S %s\n", 1000+index, group, start)
	}
	got := parseOwnedComposeProcesses([]byte(rows.String()), group)
	if got.Status != "row_limit" || !got.RowLimit || len(got.Rows) != ownedComposeProcessRows {
		t.Fatal("process metadata row bound was lost", got)
	}
}

func TestOwnedComposeFixtureProcessOutputHasFiniteRetention(t *testing.T) {
	var output ownedComposeProcessOutput
	first := []byte(strings.Repeat("x", ownedComposeProcessBytes-1))
	if n, err := output.Write(first); err != nil || n != len(first) || output.truncated {
		t.Fatal("bounded writer rejected in-budget output", n, err)
	}
	if n, err := output.Write([]byte("x")); err != nil || n != 1 || output.truncated {
		t.Fatal("exact byte limit was treated as truncation", n, err)
	}
	if n, err := output.Write([]byte("discarded")); err != nil || n != len("discarded") || !output.truncated {
		t.Fatal("oversize output was not consumed and marked incomplete", n, err)
	}
	if len(output.data) != ownedComposeProcessBytes || string(output.data) != strings.Repeat("x", ownedComposeProcessBytes) {
		t.Fatal("bounded writer retained extra or changed bytes", len(output.data))
	}
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
( trap '' TERM; exec sleep 30 ) &
printf 'deadline worker started\n'
wait`, budget: 2 * time.Second, timedOut: true, leftOver: true},
		{name: "leader-exit-live-output-writer", script: `( trap '' TERM; exec sleep 30 ) &
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
		"wrong-probe-missing-request-target", "wrong-probe-duplicate-request-target",
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
  elif $case=="wrong-probe-missing-request-target" then .HostConfig.Mounts[1].Target="/proof/unexpected"
  elif $case=="wrong-probe-duplicate-request-target" then .HostConfig.Mounts[1].Target=.HostConfig.Mounts[0].Target
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

func TestOwnedComposeMeasurementSecurityMountProfile(t *testing.T) {
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"normal", "retained", "retained-omitted-false", "retained-explicit-false", "normal-extra-bind", "foreign-label", "missing-label", "wrong-source", "readonly-request", "readonly-effective", "aliased-effective", "extra-bind", "unsafe-bind-options", "wrong-declared-source", "directory-mode", "directory-symlink", "directory-not-empty", "wrong-owner", "clone-request-drift", "clone-effective-alias", "clone-readonly", "clone-volume-copy", "desktop-mixed", "desktop-request", "desktop-effective", "desktop-two-paths", "clone-proved-alias", "clone-new-alias", "host-directory-replaced", "original-daemon-drift", "stat-mismatch", "heap-and-ro-drift"} {
		t.Run(name, func(t *testing.T) {
			const script = `set -euo pipefail
umask 077
source "$1"
case_name="$2"
project=lantern-bench-owned-fixture
export COMPOSE_PROJECT_NAME="$project"
owner="$(id -u):$(id -g)"
image="sha256:$(printf 'a%.0s' {1..64})"
export LANTERN_BENCH_RETAINED_HEAP=1
[[ "$case_name" != normal && "$case_name" != normal-extra-bind ]] || export LANTERN_BENCH_RETAINED_HEAP=0
export LANTERN_BENCH_HEAP_CONTROL_ROOT="$(cd "$OUTDIR" && pwd -P)/heap-control-evidence"
owned_compose_heap_controller init --directory "$LANTERN_BENCH_HEAP_CONTROL_ROOT"
case "$case_name" in
 directory-mode) chmod 755 "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-1";;
 directory-symlink) mv "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-1" "$OUTDIR/held-control"; ln -s "$OUTDIR/held-control" "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-1";;
 directory-not-empty) printf occupied > "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-1/foreign";;
 wrong-owner) owner="$(( $(id -u)+1 )):$(id -g)";;
esac
fixture_configuration="$(jq -nc --arg project "$project" --arg image "$image" --arg owner "$owner" --arg root "$LANTERN_BENCH_HEAP_CONTROL_ROOT" --arg case "$case_name" '
 {name:$project,volumes:([range(0;3)|("state-"+tostring),("backup-"+tostring)]|map({key:.,value:{name:.}})|from_entries),
 services:([range(0;3)|. as $i|("lantern-"+tostring) as $s|{key:$s,value:{image:$image,user:$owner,
 environment:{LANTERN_AUTH_MODE:"oidc",LANTERN_SECURITY_CONFIG_JSON:"secret-value"},
 volumes:([{type:"volume",source:("state-"+($i|tostring)),target:"/state",volume:{nocopy:true}},
 {type:"volume",source:("backup-"+($i|tostring)),target:"/data",volume:{nocopy:true}},
 {type:"bind",source:"/private/config",target:"/run/lantern-config",read_only:true},
 {type:"bind",source:"/private/config/ntp.conf",target:"/etc/ntp.conf",read_only:true}]+
 (if $case=="normal" then [] else [{type:"bind",source:(if $case=="wrong-declared-source" and $i==0 then "/foreign/control" else $root+"/"+$s end),target:"/run/lantern-bench-heap",read_only:false,bind:{create_host_path:false,propagation:"rprivate"}}] end))}}]|from_entries)}')"
for index in 0 1 2; do
 jq -nc --arg project "$project" --arg image "$image" --arg owner "$owner" --arg root "$LANTERN_BENCH_HEAP_CONTROL_ROOT" --arg case "$case_name" --argjson i "$index" '
 ("lantern-"+($i|tostring)) as $service |
 [{Type:"volume",Source:("state-"+($i|tostring)),Target:"/state",VolumeOptions:{NoCopy:true}},
 {Type:"volume",Source:("backup-"+($i|tostring)),Target:"/data",VolumeOptions:{NoCopy:true}}] as $volumes |
 (if $case=="normal" then [] else [{Type:"bind",Source:($root+"/"+$service),Target:"/run/lantern-bench-heap",ReadOnly:false,BindOptions:{Propagation:"rprivate"}}] end) as $heap |
 {Id:(("0"*63)+(($i+1)|tostring)),Image:$image,Name:("/"+$project+"-"+$service+"-1"),
 Config:{Image:$image,User:$owner,WorkingDir:"/app",Entrypoint:["/app/lantern"],Cmd:null,Env:["LANTERN_AUTH_MODE=oidc","LANTERN_SECURITY_CONFIG_JSON=secret-value"],Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$service,"com.docker.compose.oneoff":"False"}},
 RestartCount:0,State:{Status:"created",Running:false,StartedAt:"0001-01-01T00:00:00Z",Pid:0,ExitCode:0,OOMKilled:false,Restarting:false,Paused:false},HostConfig:{NetworkMode:($project+"-default"),Binds:["/private/config:/run/lantern-config:ro","/private/config/ntp.conf:/etc/ntp.conf:ro"],Mounts:($volumes+$heap),RestartPolicy:{Name:"no",MaximumRetryCount:0},IpcMode:"private",
 ReadonlyRootfs:false,Privileged:false,AutoRemove:false,CapAdd:null,CapDrop:null,SecurityOpt:null,PidMode:"",UTSMode:"",UsernsMode:"",Tmpfs:null},
 Mounts:([{Type:"bind",Source:"/private/config",Destination:"/run/lantern-config",Mode:"ro",RW:false,Propagation:"rprivate"},
 {Type:"bind",Source:"/private/config/ntp.conf",Destination:"/etc/ntp.conf",Mode:"ro",RW:false,Propagation:"rprivate"}]+
 [$volumes[]|{Type:"volume",Name:.Source,Source:("/var/lib/docker/volumes/"+.Source+"/_data"),Destination:.Target,Driver:"local",Mode:"z",RW:true,Propagation:""}]+
 [$heap[]|{Type:"bind",Source:.Source,Destination:.Target,Mode:"",RW:true,Propagation:"rprivate"}])} |
 if $case=="retained-omitted-false" then .HostConfig.Mounts|=map(if .Type=="bind" then del(.ReadOnly) else . end)
 elif $case=="retained-explicit-false" then .HostConfig.Mounts|=map(if .Type=="bind" then .BindOptions.CreateMountpoint=false else . end)
 elif $i==0 and $case=="wrong-source" then .HostConfig.Mounts[2].Source=$root+"/lantern-1"
 elif $i==0 and $case=="readonly-request" then .HostConfig.Mounts[2].ReadOnly=true
 elif $i==0 and $case=="readonly-effective" then .Mounts[4].RW=false
 elif $i==0 and $case=="aliased-effective" then .Mounts[4].Source="/mapped/control"
 elif $i==0 and $case=="extra-bind" then .HostConfig.Mounts+=[.HostConfig.Mounts[2]] | .Mounts+=[.Mounts[4]]
 elif $i==0 and $case=="unsafe-bind-options" then .HostConfig.Mounts[2].BindOptions.NonRecursive=true
 elif (($case=="desktop-mixed" and $i<2) or (($case=="clone-proved-alias" or $case=="heap-and-ro-drift") and $i==0)) then
   .HostConfig.Mounts[2].Source=("/host_mnt"+.HostConfig.Mounts[2].Source) | .Mounts[4].Source=("/host_mnt"+.Mounts[4].Source)
 elif $case=="desktop-request" and $i==0 then .HostConfig.Mounts[2].Source=("/host_mnt"+.HostConfig.Mounts[2].Source)
 elif ($case=="desktop-effective" or $case=="stat-mismatch") and $i==0 then .Mounts[4].Source=("/host_mnt"+.Mounts[4].Source)
 elif $case=="desktop-two-paths" and $i==0 then .HostConfig.Mounts[2].Source=("/observed-a"+.HostConfig.Mounts[2].Source) | .Mounts[4].Source=("/observed-b"+.Mounts[4].Source)
 else . end
 ' > "$OUTDIR/original-$index.json"
done
printf 'complete\n' > "$OUTDIR/fixture-assembled"
docker() {
 if [[ "$1" == info ]]; then
  count="$(cat "$OUTDIR/daemon-count" 2>/dev/null || printf 0)"; printf '%d\n' "$((count+1))" > "$OUTDIR/daemon-count"
  if [[ "$case_name" == original-daemon-drift && "$count" != 0 ]]; then printf other-daemon; else printf fixture-daemon; fi
 elif [[ "$1" == image && "$*" == *'.Config.Env'* ]]; then printf '["PATH=/bin"]\n'
 elif [[ "$1" == image ]]; then
  case "$case_name" in foreign-label) printf foreign;; missing-label) :;; *) printf symmetric_post_gc_2_v1;; esac
 elif [[ "$1" == compose && "$*" == *"config --format json" ]]; then printf '%s\n' "$fixture_configuration"
 elif [[ "$1" == compose ]]; then local service="${@: -1}"; printf '%064d\n' "$(( ${service##*-}+1 ))"
 elif [[ "$1" == inspect ]]; then local id="${@: -1}"; cat "$OUTDIR/original-$((10#${id: -1}-1)).json"
 else return 88; fi
}
prove_owned_compose_bind_identity() { printf 'unexpected-proof\n' >> "$OUTDIR/actions"; return 91; }
run_owned_compose_initializer() {
 if [[ "$4" == compose-bind-identity-probe ]]; then
  [[ "$1" == "$project" && "$2" == "$image" && "$3" == "$owner" && "$5" == "$original_service-bind-proof" && "$6" == 100 && "$7" == validate_owned_compose_bind_probe ]] || return 92
  local probe="$(printf '%064d' "$(( ${original_service##*-}+21 ))")" fake="$OUTDIR/fake-stat-$original_service"
  mkdir -m 700 "$fake"
  initialized="$(jq -nc --arg id "$probe" --arg image "$image" --arg owner "$owner" --arg project "$project" --arg service "$original_service-bind-proof" --argjson env "$bind_probe_environment" --argjson plan "$bind_proof_plan" --argjson targets "$bind_probe_targets" '
   {Id:$id,Name:("/"+$project+"-"+$service),Image:$image,RestartCount:0,
    Config:{Image:$image,User:$owner,WorkingDir:"/app",Entrypoint:["/bin/busybox"],Cmd:(["stat","-c","%d:%i:%F"]+$targets),Env:$env,
      Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$service,"com.docker.compose.oneoff":"True","lantern.owner":"compose-bind-identity-probe","lantern.initializer":"lantern-owned-init.FIXTURE"}},
    HostConfig:{NetworkMode:"none",ReadonlyRootfs:true,Privileged:false,AutoRemove:false,CapDrop:["ALL"],CapAdd:null,SecurityOpt:["no-new-privileges:true"],Memory:33554432,NanoCpus:250000000,PidsLimit:16,
      PidMode:"",IpcMode:"private",UTSMode:"",UsernsMode:"",Tmpfs:null,Binds:null,VolumesFrom:null,
      Devices:null,DeviceRequests:null,DeviceCgroupRules:null,PortBindings:null,PublishAllPorts:false,RestartPolicy:{Name:"no",MaximumRetryCount:0},
      Mounts:([range(0;$plan|length)|. as $i|$plan[$i] as $p|[{Type:"bind",Source:$p.original_source,Target:("/proof/original-"+($i|tostring)),ReadOnly:true,BindOptions:{Propagation:"rprivate"}},
       {Type:"bind",Source:$p.clone_source,Target:("/proof/clone-"+($i|tostring)),ReadOnly:true,BindOptions:{Propagation:"rprivate"}}]]|add)},
    Mounts:([range(0;$plan|length)|. as $i|$plan[$i] as $p|["original","clone"][]|{Type:"bind",Source:$p.clone_source,Destination:("/proof/"+.+"-"+($i|tostring)),Mode:"ro",RW:false,Propagation:"rprivate"}]),
    State:{Status:"created",Running:false,Pid:0,StartedAt:"0001-01-01T00:00:00Z",ExitCode:0,OOMKilled:false,Restarting:false,Paused:false}}
  ')" || return 92
  validate_owned_compose_bind_probe || return 92
  printf '%s\n' "$initialized" > "$fake/created.json"
  jq -c '.State.Status="exited" | .State.StartedAt="2026-10-30T00:00:00Z"' <<<"$initialized" > "$fake/completed.json"
  : > "$fake/start.log"
  for (( pair=0; pair<$(jq -r length <<<"$bind_proof_plan"); pair++ )); do
   printf '9:123:directory\n' >> "$fake/start.log"
   if [[ "$case_name" == wrong-source || "$case_name" == aliased-effective || "$case_name" == stat-mismatch ]]; then printf '9:124:directory\n'; else printf '9:123:directory\n'; fi >> "$fake/start.log"
  done
  printf '0\n' > "$fake/wait.log"
  retain_owned_compose_bind_probe "$fake" || return 92
  printf '%s\n' "$probe" > "$bind_proof_directory/probe-remove.log"
  printf 'probe %s\n' "$original_service" >> "$OUTDIR/actions"
  return 0
 fi
 [[ "$1" == "$project" && "$2" == "$image" && "$3" == "$owner" && "$4" == compose-security-initializer && "$6" == 900 && "$7" == validate_owned_compose_security_initializer ]] || return 92
 local init_service="$5"; shift 7
 printf 'clone %s\n' "$init_service" >> "$OUTDIR/actions"
 local argument; local -a replayed=()
 for argument in "$@"; do [[ "$argument" != --mount=* ]] || replayed+=("${argument#--mount=}"); done
 [[ "$(jq -nc --args '$ARGS.positional' -- "${replayed[@]}")" == "$(jq -c '[.HostConfig.Mounts[]|if .Type=="volume" then "type=volume,source="+.Source+",target="+.Target+",volume-nocopy" else "type=bind,source="+.Source+",target="+.Target+",bind-propagation=rprivate" end]' <<<"$original")" ]] || return 93
 initialized="$(jq -c --arg case "$case_name" '.Id=("8"*63+(.Id[-1:])) | .Name|=sub("-1$";"-security-init") | .Config.Cmd=["init-security"] |
  .Config.Labels["com.docker.compose.oneoff"]="True" | .Config.Labels["lantern.owner"]="compose-security-initializer" | .Config.Labels["lantern.initializer"]="lantern-owned-init.FIXTURE" |
  if $case=="clone-request-drift" then .HostConfig.Mounts[2].BindOptions.CreateMountpoint=false
  elif $case=="clone-effective-alias" then .Mounts[4].Source="/mapped/control"
  elif $case=="clone-readonly" then .Mounts[4].RW=false
  elif $case=="clone-volume-copy" then .HostConfig.Mounts[0].VolumeOptions.NoCopy=false
  elif $case=="clone-proved-alias" then .HostConfig.Mounts[2].Source=(.HostConfig.Mounts[2].Source|ltrimstr("/host_mnt")) | .Mounts[4].Source=(.Mounts[4].Source|ltrimstr("/host_mnt"))
  elif $case=="clone-new-alias" then .Mounts[4].Source="/new/unproved/control"
  elif $case=="heap-and-ro-drift" then .HostConfig.Mounts[2].Source=(.HostConfig.Mounts[2].Source|ltrimstr("/host_mnt")) | .Mounts[4].Source=(.Mounts[4].Source|ltrimstr("/host_mnt")) | .Mounts[0].Source="/other/config" else . end' <<<"$original")"
 if [[ "$case_name" == host-directory-replaced && "$init_service" == lantern-0-security-init ]]; then
  mv "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-0" "$OUTDIR/old-control"
  mkdir -m 700 "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-0"
  chgrp "$(id -g)" "$LANTERN_BENCH_HEAP_CONTROL_ROOT/lantern-0"
 fi
 validate_owned_compose_security_initializer || return 94
 if [[ "$LANTERN_BENCH_RETAINED_HEAP" == 1 ]]; then
  for node in lantern-0 lantern-1 lantern-2; do [[ -f "$OUTDIR/heap-mount-staging/$node/original-after.json" ]] || return 95; done
 fi
 printf 'start %s\n' "$init_service" >> "$OUTDIR/actions"
 if [[ "$LANTERN_BENCH_RETAINED_HEAP" == 1 ]]; then
  initialized="$(jq -c '.State.Status="exited" | .State.StartedAt="2026-10-30T00:00:00Z"' <<<"$initialized")"
  printf '%s\n' "$initialized" | owned_compose_heap_controller bind-clone-after --directory "$heap_root" --service "${init_service%-security-init}" || return 96
 fi
}
initialize_owned_compose_security "$project" "$image" "$owner" -f fixture.yml
`
			fixture := runOwnedComposeFixture(t, script, []string{helper, name}, nil, 10*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("profile fixture custody failed: %v %s", fixture.controlErr, fixture.output)
			}
			if raw, err := os.ReadFile(filepath.Join(fixture.directory, "fixture-assembled")); err != nil || string(raw) != "complete\n" {
				t.Fatalf("profile fixture never assembled valid configuration: %v %s", err, fixture.output)
			}
			accepted := name == "normal" || name == "retained" || name == "retained-omitted-false" || name == "retained-explicit-false" || name == "desktop-mixed" || name == "desktop-request" || name == "desktop-effective" || name == "desktop-two-paths" || name == "clone-proved-alias"
			if (fixture.commandErr == nil) != accepted {
				t.Fatalf("profile result=%v want accepted=%t: %s", fixture.commandErr, accepted, fixture.output)
			}
			actions, _ := os.ReadFile(filepath.Join(fixture.directory, "actions"))
			if strings.Contains(string(actions), "unexpected-proof") && name != "heap-and-ro-drift" {
				t.Fatalf("heap identity bypassed its fixed producer or swallowed a remaining RO discrepancy: %s", actions)
			}
			if name == "heap-and-ro-drift" && !strings.Contains(string(actions), "unexpected-proof") {
				t.Fatalf("remaining config RO drift skipped its existing proof: %s", actions)
			}
			if name == "clone-new-alias" && strings.Contains(string(actions), "probe ") {
				t.Fatalf("new clone alias triggered another heap probe: %s", actions)
			}
			if name == "desktop-mixed" && strings.Count(string(actions), "probe ") != 2 {
				t.Fatalf("mixed representation did not prove exactly two originals: %s", actions)
			}
			if accepted && (strings.Count(string(actions), "clone ") != 3 || strings.Count(string(actions), "start ") != 3) {
				t.Fatalf("three exact node profiles not replayed: %s", actions)
			}
			if !accepted && strings.Contains(string(actions), "start ") {
				t.Fatalf("unsupported profile initialized a node: %s", actions)
			}
		})
	}
}

func TestOwnedComposeLocalTimeInitializerRouting(t *testing.T) {
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	timeHelper, err := filepath.Abs("local_time.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		ip           string
		accepted     bool
		cloneRefusal bool
	}{
		{"private10", "10.31.0.2", true, false},
		{"private172", "172.23.0.2", true, false},
		{"private192", "192.168.33.2", true, false},
		{"created-network-pending", "172.23.0.2", true, false},
		{"missing-map", "172.23.0.2", false, false},
		{"wrong-map", "172.23.0.2", false, false},
		{"multiple-map", "172.23.0.2", false, false},
		{"extra-network", "172.23.0.2", false, false},
		{"wrong-network-id", "172.23.0.2", false, false},
		{"wrong-network-mode", "172.23.0.2", false, false},
		{"foreign-profile", "172.23.0.2", false, false},
		{"writable-time-input", "172.23.0.2", false, false},
		{"public-ip", "8.8.8.8", false, false},
		{"loopback-ip", "127.0.0.1", false, false},
		{"link-local-ip", "169.254.1.2", false, false},
		{"invalid-octet", "172.23.999.2", false, false},
		{"noncanonical-ip", "10.031.0.2", false, false},
		{"clone-missing-map", "172.23.0.2", false, true},
		{"clone-wrong-map", "172.23.0.2", false, true},
		{"clone-extra-network", "172.23.0.2", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const script = `set -euo pipefail
umask 077
source "$1"
source "$2"
case_name="$3"
export COMPOSE_PROJECT_NAME=lantern-local-time-fixture
export LANTERN_BENCH_RETAINED_HEAP=0
LOCAL_TIME_ACTIVE=1
LOCAL_TIME_FIXTURE_IP="$4"
LOCAL_TIME_NETWORK_ID="$(printf 'c%.0s' {1..64})"
LOCAL_TIME_NETWORK_NAME="$COMPOSE_PROJECT_NAME-default"
LOCAL_TIME_IMAGE_ID="sha256:$(printf 'a%.0s' {1..64})"
fixture_owner="$(id -u):$(id -g)"
fixture_configuration="$(jq -nc --arg project "$COMPOSE_PROJECT_NAME" --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$fixture_owner" '
 {name:$project,services:([range(0;3)|. as $i|("lantern-"+tostring) as $s|
   {key:$s,value:{image:$image,user:$owner,environment:{LANTERN_AUTH_MODE:"oidc",LANTERN_SECURITY_CONFIG_JSON:"secret-value"},
    volumes:[{type:"volume",source:("state-"+($i|tostring)),target:"/state",volume:{nocopy:true}},
             {type:"volume",source:("backup-"+($i|tostring)),target:"/data",volume:{nocopy:true}}]}}]|from_entries),
  volumes:([range(0;3)|("state-"+tostring),("backup-"+tostring)]|map({key:.,value:{name:.}})|from_entries)}')"
for index in 0 1 2; do
 jq -nc --arg project "$COMPOSE_PROJECT_NAME" --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$fixture_owner" \
   --arg ip "$LOCAL_TIME_FIXTURE_IP" --arg network "$LOCAL_TIME_NETWORK_NAME" --arg id "$LOCAL_TIME_NETWORK_ID" --arg case "$case_name" --argjson i "$index" '
  ("lantern-"+($i|tostring)) as $service |
  {Id:(("0"*63)+(($i+1)|tostring)),Image:$image,Name:("/"+$project+"-"+$service+"-1"),RestartCount:0,
   Config:{Image:$image,User:$owner,WorkingDir:"/app",Entrypoint:["/app/lantern"],Cmd:null,
    Env:["LANTERN_AUTH_MODE=oidc","LANTERN_SECURITY_CONFIG_JSON=secret-value"],
    Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$service,"com.docker.compose.oneoff":"False","io.lantern.bench-time.profile":"bench_local_udp_v1"}},
   HostConfig:{NetworkMode:$network,ExtraHosts:[("time.asia.apple.com:"+$ip)],
    Binds:["/private/config:/run/lantern-config:ro","/private/config/ntp.conf:/etc/ntp.conf:ro"],
    Mounts:[{Type:"volume",Source:("state-"+($i|tostring)),Target:"/state",VolumeOptions:{NoCopy:true}},
            {Type:"volume",Source:("backup-"+($i|tostring)),Target:"/data",VolumeOptions:{NoCopy:true}}]},
   Mounts:[{Type:"bind",Source:"/private/config",Destination:"/run/lantern-config",RW:false,Mode:"ro",Propagation:"rprivate"},
           {Type:"bind",Source:"/private/config/ntp.conf",Destination:"/etc/ntp.conf",RW:false,Mode:"ro",Propagation:"rprivate"},
           {Type:"volume",Name:("state-"+($i|tostring)),Destination:"/state",RW:true},
           {Type:"volume",Name:("backup-"+($i|tostring)),Destination:"/data",RW:true}],
   NetworkSettings:{Networks:{($network):{NetworkID:$id}}},State:{Status:"created",Running:false,Pid:0,ExitCode:0,OOMKilled:false}} |
  if $case=="created-network-pending" then .NetworkSettings.Networks[$network].NetworkID=""
  elif $i!=2 then .
  elif $case=="missing-map" then .HostConfig.ExtraHosts=[]
  elif $case=="wrong-map" then .HostConfig.ExtraHosts=["time.asia.apple.com:172.23.0.99"]
  elif $case=="multiple-map" then .HostConfig.ExtraHosts+=["other.invalid:172.23.0.2"]
  elif $case=="extra-network" then .NetworkSettings.Networks.foreign={NetworkID:("d"*64)}
  elif $case=="wrong-network-id" then .NetworkSettings.Networks[$network].NetworkID=("d"*64)
  elif $case=="wrong-network-mode" then .HostConfig.NetworkMode="bridge"
  elif $case=="foreign-profile" then .Config.Labels["io.lantern.bench-time.profile"]="foreign"
  elif $case=="writable-time-input" then .Mounts[1].RW=true
  else . end' > "$OUTDIR/original-$index.json"
done
printf 'complete\n' > "$OUTDIR/fixture-assembled"
# Keep only the fake CLI scheduling fast; production custody code and its
# identity/validator calls execute unchanged.
sleep() { command sleep 0.005; }
docker() {
 if [[ "$1" == compose ]]; then
  if [[ "$*" == *"config --format json" ]]; then printf '%s\n' "$fixture_configuration"
  else local node="${@: -1}"; printf '%064d\n' "$(( ${node##*-}+1 ))"; fi
  return
 fi
 if [[ "$1" == create ]]; then
  local previous="" argument name="" nonce_value="" service_value="" network_value="" profile_value=""
  local -a hosts=() profiles=()
  for argument in "$@"; do
   case "$previous" in
    --name) name="$argument";; --network) network_value="$argument";;
    --add-host) hosts+=("$argument");;
    --label) case "$argument" in
      lantern.initializer=*) nonce_value="${argument#*=}";;
      com.docker.compose.service=*) service_value="${argument#*=}";;
      io.lantern.bench-time.profile=*) profiles+=("${argument#*=}");;
     esac;;
   esac
   previous="$argument"
  done
  [[ "${#hosts[@]}" == 1 && "${hosts[0]}" == "time.asia.apple.com:$LOCAL_TIME_FIXTURE_IP" &&
     "${#profiles[@]}" == 1 && "${profiles[0]}" == bench_local_udp_v1 && "$network_value" == "$LOCAL_TIME_NETWORK_NAME" ]] || return 91
  local i="${service_value##*-}" clone
  clone="$(printf '%064d' "$((i+11))")"
  jq -c --arg id "$clone" --arg name "$name" --arg nonce "$nonce_value" --arg case "$case_name" \
    --argjson hosts "$(jq -nc --args '$ARGS.positional' -- "${hosts[@]}")" --arg profile "${profiles[0]}" '
   .Id=$id | .Name=("/"+$name) | .Config.Cmd=["init-security"] |
   .Config.Labels["com.docker.compose.oneoff"]="True" | .Config.Labels["lantern.owner"]="compose-security-initializer" |
   .Config.Labels["lantern.initializer"]=$nonce | .Config.Labels["io.lantern.bench-time.profile"]=$profile |
   .HostConfig.ExtraHosts=$hosts |
   if $case=="clone-missing-map" then .HostConfig.ExtraHosts=[]
   elif $case=="clone-wrong-map" then .HostConfig.ExtraHosts=["time.asia.apple.com:172.23.0.99"]
   elif $case=="clone-extra-network" then .NetworkSettings.Networks.foreign={NetworkID:("d"*64)}
   else . end' <<<"$original" > "$OUTDIR/clone-$i.json"
  printf 'create %s host=%s\n' "$i" "${hosts[0]}" >> "$OUTDIR/actions"
  printf '%s\n' "$clone"
  return
 fi
 local target="${@: -1}"
 local number="$((10#${target: -2}))"
 if [[ "$1" == inspect ]]; then
  if ((number<=3)); then
   printf 'original %s\n' "$((number-1))" >> "$OUTDIR/actions"
   cat "$OUTDIR/original-$((number-1)).json"
  else cat "$OUTDIR/clone-$((number-11)).json"; fi
 elif [[ "$1" == start ]]; then
  printf 'start %s\n' "$((number-11))" >> "$OUTDIR/actions"
  jq -c '.State.Status="exited" | .State.StartedAt="2026-10-30T00:00:00Z"' "$OUTDIR/clone-$((number-11)).json" > "$OUTDIR/completed.json"
  mv "$OUTDIR/completed.json" "$OUTDIR/clone-$((number-11)).json"
 elif [[ "$1" == wait ]]; then printf '0\n'
 elif [[ "$1" == rm ]]; then printf 'remove %s\n' "$((number-11))" >> "$OUTDIR/actions"
 elif [[ "$1" == logs ]]; then printf 'bounded fake initializer log\n'
 else return 92; fi
}
local_time_ntp_admit() {
 local node="$1" role="$2" raw phase
 raw="$(cat)" || return 1
 [[ "$node" =~ ^lantern-[012]$ && "$role" == initializer ]] || return 94
 phase="$(jq -er --arg node "$node" '
  select(.Config.Labels["com.docker.compose.service"]==$node) |
  .State.Status | select(.=="created" or .=="exited")' <<<"$raw")" || return 94
 printf 'ntp-%s %s\n' "$phase" "${node##*-}" >> "$OUTDIR/actions"
}
prove_owned_compose_bind_identity() { printf 'unexpected-proof\n' >> "$OUTDIR/actions"; return 93; }
mkdir -m 700 "$OUTDIR/time-source-evidence"
initialize_owned_compose_security "$COMPOSE_PROJECT_NAME" "$LOCAL_TIME_IMAGE_ID" "$fixture_owner" -f fixture.yml
`
			fixture := runOwnedComposeFixture(t, script, []string{helper, timeHelper, tc.name, tc.ip}, nil, 10*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("local time fixture custody failed: %v %s", fixture.controlErr, fixture.output)
			}
			if raw, err := os.ReadFile(filepath.Join(fixture.directory, "fixture-assembled")); err != nil || string(raw) != "complete\n" {
				t.Fatalf("route fixture was not assembled: %v %s", err, fixture.output)
			}
			if (fixture.commandErr == nil) != tc.accepted {
				t.Fatalf("route admission=%v want %v: %s", fixture.commandErr, tc.accepted, fixture.output)
			}
			actions, _ := os.ReadFile(filepath.Join(fixture.directory, "actions"))
			if strings.Contains(string(fixture.output), "secret-value") || strings.Contains(string(actions), "unexpected-proof") {
				t.Fatalf("route check leaked material or entered a mount fallback: %s", actions)
			}
			if tc.accepted {
				if strings.Count(string(actions), "create ") != 3 || strings.Count(string(actions), "start ") != 3 || strings.Count(string(actions), " host=time.asia.apple.com:"+tc.ip) != 3 {
					t.Fatalf("three exact mappings were not replayed by actual initializer custody: %s", actions)
				}
				if strings.Count(string(actions), "ntp-created ") != 3 || strings.Count(string(actions), "ntp-exited ") != 3 {
					t.Fatalf("created and completed initializers did not enter NTP admission: %s", actions)
				}
				before, first := strings.Index(string(actions), "original 2\n"), strings.Index(string(actions), "create 0 ")
				if before < 0 || first < 0 || before > first {
					t.Fatalf("native clone was created before all three routes were admitted: %s", actions)
				}
				for i := range 3 {
					admitted := strings.Index(string(actions), fmt.Sprintf("ntp-created %d\n", i))
					started := strings.Index(string(actions), fmt.Sprintf("start %d\n", i))
					if admitted < 0 || started < 0 || admitted > started {
						t.Fatalf("native initializer started before NTP admission: %s", actions)
					}
					if _, err := os.Stat(filepath.Join(fixture.directory, "time-source-evidence", fmt.Sprintf("init-%d.json", i))); err != nil {
						t.Fatalf("successful initializer route proof missing: %v", err)
					}
				}
			} else {
				if strings.Contains(string(actions), "start ") || !tc.cloneRefusal && strings.Contains(string(actions), "create ") {
					t.Fatalf("unadmitted route reached native initialization: %s", actions)
				}
				if tc.cloneRefusal && strings.Count(string(actions), "create ") != 1 {
					t.Fatalf("negative clone case did not reach its created-object route check: %s", actions)
				}
			}
		})
	}
}

func TestOwnedComposeLocalTimeNTPFileBinding(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the host file contract requires the current nonroot owner")
	}
	helper, err := filepath.Abs("owned_compose.sh")
	if err != nil {
		t.Fatal(err)
	}
	timeHelper, err := filepath.Abs("local_time.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		accepted bool
		probes   int
		volume   bool
	}{
		{"literal", true, 0, true},
		{"original-alias", true, 1, true},
		{"requested-alias", true, 1, true},
		{"effective-alias", true, 1, true},
		{"clone-first-alias", true, 1, true},
		{"clone-first-wrong-cid", false, 1, true},
		{"original-rw", false, 0, false},
		{"original-destination", false, 0, false},
		{"original-third-source", false, 0, false},
		{"original-foreign-image", false, 0, false},
		{"original-foreign-project", false, 0, false},
		{"probe-directory", false, 1, false},
		{"probe-different-inode", false, 1, false},
		{"probe-missing-row", false, 1, false},
		{"probe-duplicate-row", false, 1, false},
		{"probe-writable", false, 1, false},
		{"probe-third-source", false, 1, false},
		{"probe-exit", false, 1, false},
		{"probe-wait", false, 1, false},
		{"probe-remove", false, 1, false},
		{"daemon-drift", false, 1, false},
		{"host-hash-drift", false, 1, false},
		{"host-inode-drift", false, 1, false},
		{"missing-proof", false, 1, true},
		{"foreign-proof", false, 1, true},
		{"recheck-third-source", false, 1, true},
		{"recheck-daemon-drift", false, 1, true},
		{"post-host-hash-drift", false, 1, true},
		{"post-host-inode-drift", false, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const script = `set -euo pipefail
umask 077
source "$1"
source "$2"
case_name="$3"
OUTDIR="$(cd "$OUTDIR" && pwd -P)"
docker() { printf 'unexpected\n' > "$OUTDIR/prepare-docker"; return 97; }
export COMPOSE_PROJECT_NAME=lantern-ntp-file-fixture
runtime_owner="$(id -u):$(id -g)"
receipt_image_id="sha256:$(printf 'a%.0s' {1..64})"
receipt_image_commit="$(printf 'b%.0s' {1..40})"
EXPECTED_LANTERN_TREE="$(printf 'c%.0s' {1..40})"
SCENARIO_NAME=many_subscribers
retained_heap=0
LOCAL_TIME_ACTIVE=1
LOCAL_TIME_IMAGE_ID="$receipt_image_id"
LOCAL_TIME_FIXTURE_IP=172.23.0.2
LOCAL_TIME_NETWORK_ID="$(printf 'd%.0s' {1..64})"
LOCAL_TIME_NETWORK_NAME="$COMPOSE_PROJECT_NAME-default"
PEER_TLS_DIR="$OUTDIR/trust"
mkdir -m 700 "$PEER_TLS_DIR" "$OUTDIR/time-source-evidence"
for node in lantern-0 lantern-1 lantern-2; do
 mkdir -m 700 "$PEER_TLS_DIR/$node"
 printf 'server time.asia.apple.com\n' > "$PEER_TLS_DIR/$node/ntp.conf"
 chmod 644 "$PEER_TLS_DIR/$node/ntp.conf"
done
local_time_ntp_prepare
canonical="$PEER_TLS_DIR/lantern-1/ntp.conf"
alternate="/host_mnt$canonical"
third="/unproved$canonical"
fixture_configuration="$(jq -nc --arg project "$COMPOSE_PROJECT_NAME" --arg image "$receipt_image_id" --arg owner "$runtime_owner" --arg trust "$PEER_TLS_DIR" '
 {name:$project,services:([range(0;3)|. as $i|("lantern-"+tostring) as $s|
  {key:$s,value:{image:$image,user:$owner,environment:{LANTERN_AUTH_MODE:"oidc",LANTERN_SECURITY_CONFIG_JSON:"private-current-config"},
   volumes:[{type:"bind",source:($trust+"/"+$s),target:"/run/lantern-config",read_only:true},
    {type:"bind",source:($trust+"/"+$s+"/ntp.conf"),target:"/etc/ntp.conf",read_only:true},
    {type:"volume",source:("state-"+($i|tostring)),target:"/state",volume:{nocopy:true}},
    {type:"volume",source:("backup-"+($i|tostring)),target:"/data",volume:{nocopy:true}}]}}]|from_entries),
  volumes:([range(0;3)|("state-"+tostring),("backup-"+tostring)]|map({key:.,value:{name:.}})|from_entries)}')"
for index in 0 1 2; do
 jq -nc --arg project "$COMPOSE_PROJECT_NAME" --arg image "$receipt_image_id" --arg owner "$runtime_owner" \
  --arg trust "$PEER_TLS_DIR" --arg network "$LOCAL_TIME_NETWORK_NAME" --arg network_id "$LOCAL_TIME_NETWORK_ID" \
  --arg case "$case_name" --arg alternate "$alternate" --arg third "$third" --argjson i "$index" '
  ("lantern-"+($i|tostring)) as $s | ($trust+"/"+$s) as $config |
  {Id:(("0"*63)+(($i+1)|tostring)),Name:("/"+$project+"-"+$s+"-1"),Image:$image,RestartCount:0,
   Config:{Image:$image,User:$owner,WorkingDir:"/app",Entrypoint:["/app/lantern"],Cmd:null,Volumes:null,
    Env:["LANTERN_AUTH_MODE=oidc","LANTERN_SECURITY_CONFIG_JSON=private-current-config"],
    Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$s,"com.docker.compose.oneoff":"False","io.lantern.bench-time.profile":"bench_local_udp_v1"}},
   State:{Status:"created",Running:false,Pid:0,StartedAt:"",OOMKilled:false,ExitCode:0},
   HostConfig:{NetworkMode:$network,ExtraHosts:["time.asia.apple.com:172.23.0.2"],
    Binds:[($config+":/run/lantern-config:ro"),($config+"/ntp.conf:/etc/ntp.conf:ro")],
    Mounts:[{Type:"volume",Source:("state-"+($i|tostring)),Target:"/state",VolumeOptions:{NoCopy:true}},
     {Type:"volume",Source:("backup-"+($i|tostring)),Target:"/data",VolumeOptions:{NoCopy:true}}],VolumesFrom:null,Tmpfs:null},
   Mounts:[{Type:"bind",Source:$config,Destination:"/run/lantern-config",Mode:"ro",RW:false,Propagation:"rprivate"},
    {Type:"bind",Source:($config+"/ntp.conf"),Destination:"/etc/ntp.conf",Mode:"ro",RW:false,Propagation:"rprivate"},
    {Type:"volume",Name:("state-"+($i|tostring)),Destination:"/state",RW:true},
    {Type:"volume",Name:("backup-"+($i|tostring)),Destination:"/data",RW:true}],
   NetworkSettings:{Networks:{($network):{NetworkID:$network_id}},Ports:{}}} |
  if $i!=1 or $case=="literal" or $case=="clone-first-alias" or $case=="clone-first-wrong-cid" then . else
   .HostConfig.Binds[1]=($alternate+":/etc/ntp.conf:ro") | .Mounts[1].Source=$alternate |
   if $case=="requested-alias" then .Mounts[1].Source=($config+"/ntp.conf")
   elif $case=="effective-alias" then .HostConfig.Binds[1]=($config+"/ntp.conf:/etc/ntp.conf:ro")
   elif $case=="original-rw" then .Mounts[1].RW=true
   elif $case=="original-destination" then .Mounts[1].Destination="/etc/other.conf"
   elif $case=="original-third-source" then .Mounts[1].Source=$third
   elif $case=="original-foreign-image" then .Image="foreign-image"
   elif $case=="original-foreign-project" then .Config.Labels["com.docker.compose.project"]="foreign-project"
   else . end
  end' > "$OUTDIR/original-$index.json"
done
docker() {
 if [[ "$1" == compose ]]; then
  if [[ "$*" == *"config --format json" ]]; then printf '%s\n' "$fixture_configuration"
  elif [[ "$*" == *"create lantern-0 lantern-1 lantern-2" ]]; then printf 'originals-created\n' >> "$OUTDIR/actions"
  else local node="${@: -1}"; printf '%064d\n' "$(( ${node##*-}+1 ))"; fi
  return
 fi
 if [[ "$1" == volume ]]; then
  if [[ "$2" == inspect ]]; then printf '%s\n' "$COMPOSE_PROJECT_NAME"; fi
  return
 fi
 if [[ "$1" == info ]]; then
  local count=0
  [[ ! -f "$OUTDIR/daemon-count" ]] || read -r count < "$OUTDIR/daemon-count"
  count=$((count+1)); printf '%s\n' "$count" > "$OUTDIR/daemon-count"
  if [[ "$case_name" == daemon-drift && "$count" -gt 1 || -f "$OUTDIR/change-daemon" ]]; then printf 'foreign-daemon\n'; else printf 'same-daemon\n'; fi
  return
 fi
 if [[ "$1" == image ]]; then printf '["PATH=/usr/bin:/bin"]\n'; return; fi
 if [[ "$1" == inspect ]]; then
  local id="${@: -1}" index
  [[ "$id" =~ ^0{63}[123]$ ]] || return 91
  index="$((10#${id: -1}-1))"
  cat "$OUTDIR/original-$index.json"
  return
 fi
 return 92
}
local_time_assert_fixture_only() {
 [[ ! -e "$OUTDIR/actions" ]] || return 93
 for node in lantern-0 lantern-1 lantern-2; do
  [[ -f "$OUTDIR/time-source-evidence/ntp-mounts/$node/host-before.json" &&
     ! -e "$OUTDIR/time-source-evidence/ntp-mounts/$node/original-before.json" ]] || return 93
 done
}
# Preserve the real plan builder, probe validator and raw-output retainer.
# Only the owned utility execution is replaced by finite created/completed data.
run_owned_compose_initializer() {
 local project="$1" image="$2" owner="$3" kind="$4" service="$5" validator="$7"
 shift 7
 if [[ "$kind" == compose-volume-initializer ]]; then
  for node in lantern-0 lantern-1 lantern-2; do
   [[ -f "$OUTDIR/time-source-evidence/ntp-mounts/$node/original-before.json" ]] || return 93
  done
  printf 'volume-init\n' >> "$OUTDIR/actions"
  return
 fi
 [[ "$kind" == compose-bind-identity-probe && "$validator" == validate_owned_compose_bind_probe ]] || return 93
 printf 'probe %s\n' "$service" >> "$OUTDIR/actions"
 local arguments previous="" argument
 local -a binds=()
 arguments="$(jq -nc --args '$ARGS.positional' -- "$@")"
 jq -e --arg image "$image" --argjson targets "$bind_probe_targets" '
  index($image) as $i | .[$i+1:]==(["stat","-c","%d:%i:%F"]+$targets) and
  (index("--env-file")==null) and (index("--network") as $n|.[$n+1]=="none") and
  (index("--read-only")!=null) and (index("--cap-drop") as $n|.[$n+1]=="ALL") and
  (index("--security-opt") as $n|.[$n+1]=="no-new-privileges:true") and
  (index("--memory") as $n|.[$n+1]=="32m") and (index("--cpus") as $n|.[$n+1]=="0.25") and
  (index("--pids-limit") as $n|.[$n+1]=="16")' <<<"$arguments" >/dev/null || return 94
 for argument in "$@"; do case "$argument" in --mount=*) binds+=("${argument#*=}");; esac; done
 local private="$OUTDIR/probe-private" initialized probe_id
 mkdir -m 700 "$private"
 probe_id="$(printf 'e%.0s' {1..64})"
 initialized="$(jq -nc --arg project "$project" --arg image "$image" --arg owner "$owner" --arg service "$service" \
  --arg id "$probe_id" --arg case "$case_name" --argjson targets "$bind_probe_targets" \
  --argjson binds "$(jq -nc --args '$ARGS.positional' -- "${binds[@]}")" '
  ($binds|map(split(",")|{Type:"bind",Source:.[1][7:],Target:.[2][7:],ReadOnly:true,BindOptions:{Propagation:"rprivate"}})) as $requests |
  {Id:$id,Name:("/"+$project+"-"+$service),Image:$image,
   Config:{Image:$image,User:$owner,Entrypoint:["/bin/busybox"],Cmd:(["stat","-c","%d:%i:%F"]+$targets),Env:["PATH=/usr/bin:/bin"],Volumes:null,
    Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":$service,"com.docker.compose.oneoff":"True","lantern.owner":"compose-bind-identity-probe","lantern.initializer":"lantern-owned-init.fixture"}},
   State:{Status:"created",Running:false,ExitCode:0},
   HostConfig:{NetworkMode:"none",ReadonlyRootfs:true,Privileged:false,AutoRemove:false,CapDrop:["ALL"],CapAdd:null,SecurityOpt:["no-new-privileges:true"],Memory:33554432,NanoCpus:250000000,PidsLimit:16,
    Binds:null,Mounts:$requests,VolumesFrom:null,Tmpfs:null,Devices:null,DeviceRequests:null,DeviceCgroupRules:null,PidMode:"",UTSMode:"",UsernsMode:"",PublishAllPorts:false,PortBindings:{},RestartPolicy:{Name:"no",MaximumRetryCount:0}},
   Mounts:[$requests[]|{Type:"bind",Source:.Source,Destination:.Target,Mode:"",RW:false,Propagation:"rprivate"}]} |
  if $case=="probe-writable" then .Mounts[0].RW=true
  elif $case=="probe-third-source" then .Mounts[0].Source="/third/lantern-1/ntp.conf" | .HostConfig.Mounts[0].Source="/third/lantern-1/ntp.conf"
  else . end')"
 "$validator" || return 95
 printf '%s\n' "$initialized" > "$private/created.json"
 jq -c --arg case "$case_name" '.State={Status:"exited",Running:false,ExitCode:(if $case=="probe-exit" then 17 else 0 end)}' <<<"$initialized" > "$private/completed.json"
 case "$case_name" in
  probe-directory) printf '5:100:directory\n5:100:directory\n';;
  probe-different-inode) printf '5:100:regular file\n5:101:regular file\n';;
  probe-missing-row) printf '5:100:regular file\n';;
  probe-duplicate-row) printf '5:100:regular file\n5:100:regular file\n5:100:regular file\n';;
  *) printf '5:100:regular file\n5:100:regular file\n';;
 esac > "$private/start.log"
 if [[ "$case_name" == probe-wait ]]; then printf '1\n'; else printf '0\n'; fi > "$private/wait.log"
 retain_owned_compose_bind_probe "$private" || return 96
 if [[ "$case_name" == probe-remove ]]; then printf 'foreign-id\n'; else printf '%s\n' "$probe_id"; fi > "$bind_proof_directory/probe-remove.log"
 if [[ "$case_name" == host-hash-drift ]]; then printf 'changed time source\n' > "$canonical"; fi
 if [[ "$case_name" == host-inode-drift ]]; then
  printf 'server time.asia.apple.com\n' > "$PEER_TLS_DIR/lantern-1/replacement"
  chmod 644 "$PEER_TLS_DIR/lantern-1/replacement"
  mv "$PEER_TLS_DIR/lantern-1/replacement" "$canonical"
 fi
}
initialize_owned_compose_volumes "$COMPOSE_PROJECT_NAME" "$receipt_image_id" "$runtime_owner" -f fixture.yml
node_directory="$OUTDIR/time-source-evidence/ntp-mounts/lantern-1"
if [[ "$case_name" == missing-proof ]]; then rm "$node_directory/probe-output.log"; fi
if [[ "$case_name" == foreign-proof ]]; then
 jq -c '.Image="foreign-image"' "$node_directory/probe-created.json" > "$OUTDIR/foreign.json"
 mv "$OUTDIR/foreign.json" "$node_directory/probe-created.json"
fi
original="$(cat "$OUTDIR/original-1.json")"
configuration="$fixture_configuration"
service=lantern-1-security-init
initialized="$(jq -c --arg project "$COMPOSE_PROJECT_NAME" --arg case "$case_name" --arg alternate "$alternate" '
 .Id=("f"*64) | .Name=("/"+$project+"-lantern-1-security-init") | .Config.Cmd=["init-security"] |
 .Config.Labels["com.docker.compose.oneoff"]="True" | .Config.Labels["lantern.owner"]="compose-security-initializer" |
 if $case=="clone-first-alias" or $case=="clone-first-wrong-cid" then .Mounts[1].Source=$alternate else . end' <<<"$original")"
validate_owned_compose_security_initializer
printf 'native-init\n' >> "$OUTDIR/actions"
initialized="$(jq -c '.State.Status="exited" | .State.ExitCode=0' <<<"$initialized")"
local_time_record_initializer lantern-1 <<<"$initialized"
if [[ "$case_name" == clone-first-wrong-cid ]]; then
 changed_clone="$(jq -c '.Id=("9"*64)' <<<"$initialized")"
 local_time_ntp_admit lantern-1 initializer <<<"$changed_clone"
fi
if [[ "$case_name" == recheck-daemon-drift ]]; then : > "$OUTDIR/change-daemon"; fi
if [[ "$case_name" == post-host-hash-drift ]]; then printf 'changed time source\n' > "$canonical"; fi
if [[ "$case_name" == post-host-inode-drift ]]; then
 printf 'server time.asia.apple.com\n' > "$PEER_TLS_DIR/lantern-1/replacement"
 chmod 644 "$PEER_TLS_DIR/lantern-1/replacement"
 mv "$PEER_TLS_DIR/lantern-1/replacement" "$canonical"
fi
running="$(jq -c --arg case "$case_name" --arg third "$third" '
 .State.Status="running" | .State.Running=true |
 if $case=="recheck-third-source" then .Mounts[1].Source=$third else . end' <<<"$original")"
local_time_ntp_admit lantern-1 serving <<<"$running"
local_time_ntp_host_record lantern-1 "$OUTDIR/time-source-evidence/host-current.json"
printf 'complete\n' > "$OUTDIR/ntp-complete"
`
			fixture := runOwnedComposeFixture(t, script, []string{helper, timeHelper, tc.name}, nil, 15*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("NTP file fixture custody failed: %v %s\n%s", fixture.controlErr, fixture.output, ownedComposeFixtureDiagnostics(fixture.directory))
			}
			if (fixture.commandErr == nil) != tc.accepted {
				t.Fatalf("NTP file admission=%v want accepted=%t: %s", fixture.commandErr, tc.accepted, fixture.output)
			}
			actions, _ := os.ReadFile(filepath.Join(fixture.directory, "actions"))
			if _, err := os.Stat(filepath.Join(fixture.directory, "prepare-docker")); !os.IsNotExist(err) {
				t.Fatalf("host preparation attempted Docker: %v", err)
			}
			if strings.Count(string(actions), "probe ") != tc.probes || strings.Contains(string(actions), "volume-init\n") != tc.volume {
				t.Fatalf("NTP proof/init boundary differs: %s", actions)
			}
			_, completed := os.Stat(filepath.Join(fixture.directory, "ntp-complete"))
			if (completed == nil) != tc.accepted {
				t.Fatalf("NTP file completion=%v want accepted=%t", completed, tc.accepted)
			}
			if strings.Contains(string(fixture.output), "private-current-config") {
				t.Fatal("current security configuration leaked to diagnostics")
			}
			if !tc.accepted {
				return
			}
			for i := range 3 {
				path := filepath.Join(fixture.directory, "time-source-evidence", "ntp-mounts", fmt.Sprintf("lantern-%d", i), "original-before.json")
				data, err := os.ReadFile(path)
				if err != nil || strings.Contains(string(data), "private-current-config") || strings.Contains(string(data), "\"Env\"") {
					t.Fatalf("safe created-original record missing or contains Env: %v %s", err, data)
				}
			}
			before, err := os.ReadFile(filepath.Join(fixture.directory, "time-source-evidence", "ntp-mounts", "lantern-1", "host-before.json"))
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(fixture.directory, "time-source-evidence", "host-current.json"))
			if err != nil || string(before) != string(after) {
				t.Fatalf("stable host fingerprint changed: %v", err)
			}
			bindingPath := filepath.Join(fixture.directory, "time-source-evidence", "ntp-mounts", "lantern-1", "binding.json")
			bindingRaw, bindingErr := os.ReadFile(bindingPath)
			if tc.name == "literal" {
				if !os.IsNotExist(bindingErr) || string(actions) != "originals-created\nvolume-init\nnative-init\n" {
					t.Fatalf("literal C/C path created a binding or probe: %v %s", bindingErr, actions)
				}
				return
			}
			var binding map[string]any
			if bindingErr != nil || json.Unmarshal(bindingRaw, &binding) != nil {
				t.Fatalf("immutable NTP file binding missing: %v", bindingErr)
			}
			role := "serving"
			if tc.name == "clone-first-alias" {
				role = "initializer"
			}
			if binding["alias_role"] != role || binding["service"] != "lantern-1" || binding["profile"] != "bench_local_ntp_file_bind_v1" {
				t.Fatalf("NTP binding identity differs: %s", bindingRaw)
			}
			probe := strings.Index(string(actions), "probe lantern-1-bind-proof\n")
			native := strings.Index(string(actions), "native-init\n")
			volume := strings.Index(string(actions), "volume-init\n")
			if probe < 0 || native < probe || tc.name == "clone-first-alias" && volume > probe || tc.name != "clone-first-alias" && volume < probe {
				t.Fatalf("NTP alias proof crossed its initialization boundary: %s", actions)
			}
			for _, name := range []string{"binding.json", "host-before.json", "host-after.json", "original-before.json", "original-after.json", "alias-target-before.json", "daemon-before.txt", "daemon-after.txt", "requested-pairs.json", "image-environment.json", "probe-created.json", "probe-completed.json", "probe-output.log", "probe-wait.log", "probe-remove.log"} {
				path := filepath.Join(filepath.Dir(bindingPath), name)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
					t.Fatalf("private NTP proof %s missing or unsafe: %v", name, err)
				}
			}
		})
	}
}

func TestOwnedComposeLocalTimeCaptureStartAdmission(t *testing.T) {
	timeHelper, err := filepath.Abs("local_time.sh")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(timeHelper)
	if err != nil {
		t.Fatal(err)
	}
	_, relay, found := strings.Cut(string(source), "local_time_start_relay() {\n")
	if !found {
		t.Fatal("actual local relay startup missing")
	}
	relay, _, found = strings.Cut(relay, "\nlocal_time_capture() {\n")
	if !found || strings.Contains(relay, "ntp-daemon.txt") {
		t.Fatal("phase daemon capture entered relay startup")
	}
	for _, tc := range []struct {
		name     string
		phase    string
		mode     string
		retained string
		wantExit int
		want     string
	}{
		{"PRE emits before admission", "pre", "pass", "0", 0, "emit\nvalidate-start\n"},
		{"PRE emission failure blocks admission", "pre", "emit-failure", "0", 1, "emit\n"},
		{"PRE admission refusal propagates", "pre", "admission-failure", "1", 1, "emit\nvalidate-start\n"},
		{"POST never enters start admission", "post", "admission-failure", "1", 0, "emit\n"},
		{"PRE and POST each capture their daemon", "both", "pass", "0", 0, "emit\nvalidate-start\nemit\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const script = `set -euo pipefail
source "$1"
phase="$2"; mode="$3"; retained_heap="$4"
mkdir -m 700 "$OUTDIR/time-source-evidence"
REPO_ROOT="$OUTDIR/current-source"
PEER_TLS_DIR="$OUTDIR/private-trust"
COMPOSE_PROJECT_NAME=lantern-current-capture-fixture
COMPOSE_FILES=(-f current-fixture.yml)
receipt_image_commit="$(printf 'b%.0s' {1..40})"
EXPECTED_LANTERN_TREE="$(printf 'c%.0s' {1..40})"
receipt_image_id="sha256:$(printf 'a%.0s' {1..64})"
LOCAL_TIME_IMAGE_ID="$receipt_image_id"
runtime_owner=501:20
SCENARIO_NAME=many_subscribers
LANTERN_BENCH_CPUSET=0-3
LOCAL_TIME_PLACEMENT=external-4
receipt_driver=1
LOCAL_TIME_FIXTURE_ID=fixture-id
LOCAL_TIME_RELAY_ID=relay-id
LOCAL_TIME_FRONTEND_ID=frontend-id
docker() {
 if [[ "$1" == compose ]]; then printf '%s\n' "${@: -1}"; return; fi
 if [[ "$1" == info ]]; then
  [[ "$*" == "info --format {{.ID}}" ]] || return 90
  printf '%s\n' "$phase" >> "$OUTDIR/daemon-events"
  printf 'same-daemon\n'
  return
 fi
 [[ "$1" == inspect ]] || return 90
 printf '{"observed":"%s"}\n' "${@: -1}"
}
local_time_validate_fixture() { cat >/dev/null; }
local_time_validate_relay() { cat >/dev/null; }
local_time_assert_route() { cat >/dev/null; }
local_time_ntp_admit() { cat >/dev/null; }
local_time_ntp_host_record() { printf '{}\n' | local_time_write_new "$2"; }
local_time_safe_inspect() { cat; }
local_time_network() { printf '{}\n'; }
local_time_status() { printf '{}\n'; }
local_time_relay_publishers() { printf '{}\n'; }
python3() {
 if [[ "$1" == - ]]; then
  cat >/dev/null
  [[ "$#" == 14 && "$2" == "$OUTDIR" && "$3" == "$phase" && "$4" == "$receipt_image_commit" &&
     "$5" == "$EXPECTED_LANTERN_TREE" && "$6" == "$SCENARIO_NAME" && "$7" == "$COMPOSE_PROJECT_NAME" &&
     "$8" == "$retained_heap" && "$9" == "$receipt_image_id" && "${10}" == "$runtime_owner" &&
     "${11}" == "$LANTERN_BENCH_CPUSET" && "${12}" == "$receipt_driver" &&
     "${13}" == "$LOCAL_TIME_PLACEMENT" && "${14}" == "$PEER_TLS_DIR" ]] || return 91
  printf 'emit\n' >> "$OUTDIR/capture-events"
  [[ "$mode" != emit-failure ]] || return 27
  printf '{"phase":"%s"}\n' "$phase" > "$OUTDIR/time_source_$phase.json"
  return
 fi
 [[ "$#" == 19 && "$1" == -B && "$2" == "$REPO_ROOT/.github/scripts/bench_time_profile.py" &&
    "$3" == validate-start && "$4" == --root && "$5" == "$OUTDIR" &&
    "$6" == --source-commit && "$7" == "$receipt_image_commit" &&
    "$8" == --source-tree && "$9" == "$EXPECTED_LANTERN_TREE" &&
    "${10}" == --image-id && "${11}" == "$receipt_image_id" &&
    "${12}" == --runtime-user && "${13}" == "$runtime_owner" &&
    "${14}" == --project && "${15}" == "$COMPOSE_PROJECT_NAME" &&
    "${16}" == --scenario && "${17}" == "$SCENARIO_NAME" && "${18}" == --scope ]] || return 92
 local expected_scope=normal
 [[ "$retained_heap" != 1 ]] || expected_scope=retained
 [[ "${19}" == "$expected_scope" && "$phase" == pre && -f "$OUTDIR/time_source_pre.json" ]] || return 93
 [[ "$(cat "$OUTDIR/time_source_pre.json")" == '{"phase":"pre"}' ]] || return 93
 printf 'validate-start\n' >> "$OUTDIR/capture-events"
 [[ "$mode" != admission-failure ]]
}
if [[ "$phase" == both ]]; then
 local_time_capture pre
 local_time_capture post
else
 local_time_capture "$phase"
fi
`
			fixture := runOwnedComposeFixture(t, script, []string{timeHelper, tc.phase, tc.mode, tc.retained}, nil, 5*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("capture fixture custody failed: %v %s", fixture.controlErr, fixture.output)
			}
			if fixture.exitCode != tc.wantExit {
				t.Fatalf("capture exit=%d want=%d: %v %s", fixture.exitCode, tc.wantExit, fixture.commandErr, fixture.output)
			}
			events, err := os.ReadFile(filepath.Join(fixture.directory, "capture-events"))
			if err != nil || string(events) != tc.want {
				t.Fatalf("capture/start admission order=%q want=%q: %v", events, tc.want, err)
			}
			phases := []string{tc.phase}
			if tc.phase == "both" {
				phases = []string{"pre", "post"}
			}
			daemonEvents, err := os.ReadFile(filepath.Join(fixture.directory, "daemon-events"))
			if err != nil || string(daemonEvents) != strings.Join(phases, "\n")+"\n" {
				t.Fatalf("phase daemon capture order=%q: %v", daemonEvents, err)
			}
			for _, phase := range phases {
				daemon, err := os.ReadFile(filepath.Join(fixture.directory, "time-source-evidence", phase, "ntp-daemon.txt"))
				if err != nil || string(daemon) != "same-daemon\n" {
					t.Fatalf("phase daemon receipt %s differs: %q %v", phase, daemon, err)
				}
				_, emitted := os.Stat(filepath.Join(fixture.directory, "time_source_"+phase+".json"))
				if (emitted == nil) != (tc.mode != "emit-failure") {
					t.Fatalf("phase receipt emission differs: %v", emitted)
				}
			}
		})
	}
}

func TestOwnedComposeLocalTimeRelayDiscovery(t *testing.T) {
	source, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	const start = "discover_ports() {\n"
	begin := strings.Index(string(source), start)
	if begin < 0 {
		t.Fatal("actual discover_ports function missing")
	}
	end := strings.Index(string(source[begin:]), "\n}\ndiscover_ports\n")
	if end < 0 {
		t.Fatal("actual discover_ports function boundary missing")
	}
	function := string(source[begin : begin+end+3])
	for _, tc := range []struct {
		name     string
		receipt  string
		accepted bool
	}{
		{"canonical", "0", true},
		{"receipt", "1", true},
		{"missing-api", "1", false},
		{"missing-metrics", "0", false},
		{"publisher-refused", "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `set -euo pipefail
receipt_driver="$1"
case_name="$2"
REPLICA_GRPC_PORTS=(6380 6381 6382)
REPLICA_METRICS_PORTS=(19390 19391 19392)
PROM_URL=original
log() { :; }
die() { printf '%s\n' "$*" >&2; exit 2; }
local_time_relay_publishers() {
 [[ "$case_name" != publisher-refused ]] || return 1
 jq -nc --arg case "$case_name" '
   ([range(0;3)|{key:((6380+.|tostring)+"/tcp"),value:[{HostIp:"127.0.0.1",HostPort:(16380+.|tostring)}]}]+
    [range(0;3)|{key:((9090+.|tostring)+"/tcp"),value:[{HostIp:"127.0.0.1",HostPort:(29390+.|tostring)}]}]+
    [{key:"9093/tcp",value:[{HostIp:"127.0.0.1",HostPort:"29091"}]}]|from_entries) |
   if $case=="missing-api" then del(."6381/tcp")
   elif $case=="missing-metrics" then del(."9091/tcp") else . end'
}
` + function + `
discover_ports
[[ "${REPLICA_GRPC_PORTS[*]}" == '16380 16381 16382' ]]
if [[ "$receipt_driver" == 1 ]]; then
 [[ "${REPLICA_METRICS_PORTS[*]}" == '19390 19391 19392' && "$PROM_URL" == original ]]
else
 [[ "${REPLICA_METRICS_PORTS[*]}" == '29390 29391 29392' && "$PROM_URL" == http://127.0.0.1:29091 ]]
fi
printf 'complete\n' > "$OUTDIR/discovery-complete"
`
			fixture := runOwnedComposeFixture(t, script, []string{tc.receipt, tc.name}, nil, 10*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("discovery fixture custody failed: %v %s", fixture.controlErr, fixture.output)
			}
			if (fixture.commandErr == nil) != tc.accepted {
				t.Fatalf("discovery result=%v want accepted=%t: %s", fixture.commandErr, tc.accepted, fixture.output)
			}
			_, completed := os.Stat(filepath.Join(fixture.directory, "discovery-complete"))
			if (completed == nil) != tc.accepted {
				t.Fatalf("discovery completion differs: %v %s", completed, fixture.output)
			}
		})
	}
}

func TestOwnedComposeLocalTimeRelayMemoryProfile(t *testing.T) {
	helper, err := filepath.Abs("local_time.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"created", "running"} {
		for _, tc := range []struct {
			name     string
			accepted bool
		}{
			{"exact-512", true},
			{"old-128", false},
			{"missing", false},
			{"null", false},
			{"drift", false},
			{"larger", false},
			{"string", false},
			{"cpu-drift", false},
			{"pids-drift", false},
			{"cpuset-drift", false},
		} {
			t.Run(state+"/"+tc.name, func(t *testing.T) {
				script := `set -euo pipefail
source "$1"
state="$2"; case_name="$3"
LOCAL_TIME_IMAGE_ID="sha256:$(printf 'a%.0s' {1..64})"
runtime_owner="$(id -u):$(id -g)"
COMPOSE_PROJECT_NAME=lantern-relay-memory-fixture
LOCAL_TIME_HELPER="$OUTDIR/helper"
LOCAL_TIME_RELAY_CONFIG="$OUTDIR/relay.json"
LOCAL_TIME_CPU=4
LOCAL_TIME_NETWORK_NAME="${COMPOSE_PROJECT_NAME}_default"
jq -n --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$runtime_owner" --arg project "$COMPOSE_PROJECT_NAME" \
 --arg helper "$LOCAL_TIME_HELPER" --arg config "$LOCAL_TIME_RELAY_CONFIG" --arg state "$state" --arg case "$case_name" '
 {Image:$image,Name:("/"+$project+"-time-relay"),RestartCount:0,
  Config:{Image:$image,User:$owner,Entrypoint:["/fixture/ntpfixture"],Cmd:["-relay-config","/fixture/relay.json"],
   Labels:{"com.docker.compose.project":$project,"com.docker.compose.service":"time-relay",
    "com.docker.compose.oneoff":"False","lantern.owner":"bench-local-tcp-relay","io.lantern.bench-time.profile":"bench_local_udp_v1"}},
  State:{Status:$state,Running:($state=="running"),Pid:(if $state=="running" then 123 else 0 end),OOMKilled:false},
  HostConfig:{ReadonlyRootfs:true,Privileged:false,AutoRemove:false,CapDrop:["ALL"],CapAdd:null,
   SecurityOpt:["no-new-privileges:true"],Sysctls:{"net.ipv4.ip_forward":"0","net.ipv6.conf.all.forwarding":"0"},
   PidMode:"",IpcMode:"private",UTSMode:"",UsernsMode:"",Devices:[],DeviceRequests:null,DeviceCgroupRules:null,
   VolumesFrom:null,Binds:[],Tmpfs:{},PublishAllPorts:false,ExtraHosts:[],RestartPolicy:{Name:"no",MaximumRetryCount:0},
   CpusetCpus:"4",NanoCpus:1000000000,Memory:536870912,PidsLimit:128,NetworkMode:($project+"_default"),
   Mounts:[{Type:"bind",Source:$helper,Target:"/fixture/ntpfixture",ReadOnly:true,BindOptions:{Propagation:"rprivate",CreateMountpoint:false}},
           {Type:"bind",Source:$config,Target:"/fixture/relay.json",ReadOnly:true,BindOptions:{Propagation:"rprivate",CreateMountpoint:false}}]},
  Mounts:[{Type:"bind",Destination:"/fixture/ntpfixture",RW:false,Mode:"ro",Propagation:"rprivate"},
          {Type:"bind",Destination:"/fixture/relay.json",RW:false,Mode:"ro",Propagation:"rprivate"}],
  NetworkSettings:{Networks:{($project+"_default"):{},($project+"_frontend"): {}}}} |
 if $case=="old-128" then .HostConfig.Memory=134217728
 elif $case=="missing" then del(.HostConfig.Memory)
 elif $case=="null" then .HostConfig.Memory=null
 elif $case=="drift" then .HostConfig.Memory=536870911
 elif $case=="larger" then .HostConfig.Memory=1073741824
 elif $case=="string" then .HostConfig.Memory="536870912"
 elif $case=="cpu-drift" then .HostConfig.NanoCpus=2000000000
 elif $case=="pids-drift" then .HostConfig.PidsLimit=256
 elif $case=="cpuset-drift" then .HostConfig.CpusetCpus="0-3"
 else . end' > "$OUTDIR/relay-profile.json"
local_time_validate_relay "$state" < "$OUTDIR/relay-profile.json"
`
				fixture := runOwnedComposeFixture(t, script, []string{helper, state, tc.name}, nil, 10*time.Second)
				if !fixture.controlQualified() {
					t.Fatalf("relay profile fixture custody failed: %v %s", fixture.controlErr, fixture.output)
				}
				if (fixture.commandErr == nil) != tc.accepted {
					t.Fatalf("relay profile result=%v want accepted=%t: %s", fixture.commandErr, tc.accepted, fixture.output)
				}
			})
		}
	}
}

func TestOwnedComposeLocalTimeFailureDiagnostics(t *testing.T) {
	helper, err := filepath.Abs("local_time.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		complete bool
		terminal bool
		logs     bool
	}{
		{name: "terminal-exit", complete: true, terminal: true, logs: true},
		{name: "inspect-fails"},
		{name: "setup-register-fails"},
		{name: "wrong-id"},
		{name: "wrong-image"},
		{name: "foreign-project"},
		{name: "missing-bootstrap"},
		{name: "changed-start", terminal: true, logs: true},
		{name: "changed-pid", terminal: true, logs: true},
		{name: "logs-fail", terminal: true, logs: true},
		{name: "oversize", terminal: true, logs: true},
		{name: "timeout", terminal: true, logs: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `set -euo pipefail
source "$1"
export CASE_NAME="$2"
export COMPOSE_PROJECT_NAME=lantern-terminal-fixture
LOCAL_TIME_IMAGE_ID="sha256:$(printf 'a%.0s' {1..64})"
runtime_owner="$(id -u):$(id -g)"
LOCAL_TIME_RELAY_ID="$(printf '1%.0s' {1..64})"
LOCAL_TIME_FIXTURE_ID="$(printf '2%.0s' {1..64})"
LOCAL_TIME_RELAY_DECLARED=1
mkdir -m 700 "$OUTDIR/private" "$OUTDIR/time-source-evidence" "$OUTDIR/time-source-evidence/bootstrap" "$OUTDIR/bin"
python3 - "$runtime_owner" <<'PY_FIXTURE'
import json,os,pathlib,sys
root=pathlib.Path(os.environ['OUTDIR']); project=os.environ['COMPOSE_PROJECT_NAME']; case=os.environ['CASE_NAME']
for i,role in enumerate(('relay','fixture'),1):
    cid=str(i)*64
    value={'Id':cid,'Name':'/'+project+'-time-'+role,'Image':'sha256:'+'a'*64,'RestartCount':0,
        'Config':{'User':sys.argv[1],'Env':['SECRET=never-copy-this-env'],'Labels':{'com.docker.compose.project':project,'com.docker.compose.service':'time-'+role,
            'com.docker.compose.oneoff':'False','lantern.owner':'bench-local-tcp-relay' if role=='relay' else 'bench-local-udp-fixture','io.lantern.bench-time.profile':'bench_local_udp_v1'}},
        'State':{'Status':'running','Running':True,'Pid':100+i,'StartedAt':'2026-10-30T09:41:12Z','FinishedAt':'0001-01-01T00:00:00Z','OOMKilled':False,'ExitCode':0,'Error':''}}
    path=root/'time-source-evidence'/'bootstrap'/(role+'-running.json')
    if not (role=='relay' and case=='missing-bootstrap'):
        path.write_text(json.dumps(value)); path.chmod(0o600)
    if role=='relay':
        value['State'].update(Status='exited',Running=False,Pid=0,FinishedAt='2026-10-30T09:41:55Z',OOMKilled=True,ExitCode=137,Error='observed terminal error')
        if case=='wrong-id': value['Id']='3'*64
        elif case=='wrong-image': value['Image']='sha256:'+'b'*64
        elif case=='foreign-project': value['Config']['Labels']['com.docker.compose.project']='foreign'
        elif case=='changed-start': value['State']['StartedAt']='2026-10-30T09:41:53Z'
        elif case=='changed-pid': value['State'].update(Status='running',Running=True,Pid=999)
    (root/(role+'.json')).write_text(json.dumps(value))
PY_FIXTURE
cat > "$OUTDIR/bin/docker" <<'PY_DOCKER'
#!/usr/bin/env python3
import json,os,pathlib,sys,time
root=pathlib.Path(os.environ['OUTDIR']); case=os.environ['CASE_NAME']; args=sys.argv[1:]
role={'1'*64:'relay','2'*64:'fixture'}.get(args[-1])
if role is None: raise SystemExit(91)
with (root/'commands').open('a') as f: f.write(' '.join(args)+'\n')
if args==['inspect','--format','{{json .}}',args[-1]]:
    if role=='relay' and case=='inspect-fails': raise SystemExit(17)
    if role=='relay' and case=='setup-register-fails': time.sleep(60)
    sys.stdout.write((root/(role+'.json')).read_text())
elif args==['logs','--timestamps','--tail','256',args[-1]]:
    if role=='relay' and case=='timeout':
        (root/'timed-child-pid').write_text(str(os.getpid())); time.sleep(60)
    if role=='relay' and case=='oversize':
        sys.stdout.buffer.write(b'o'*200000); sys.stdout.flush(); sys.stderr.buffer.write(b'e'*200000)
    else:
        print('2026-10-30T09:41:55Z bounded '+role+' stdout')
        print('2026-10-30T09:41:55Z bounded '+role+' stderr',file=sys.stderr)
    if role=='relay' and case=='logs-fail': raise SystemExit(19)
else: raise SystemExit(92)
PY_DOCKER
chmod 700 "$OUTDIR/bin/docker"
export PATH="$OUTDIR/bin:$PATH"
if [[ "$CASE_NAME" == setup-register-fails ]]; then
 cat > "$OUTDIR/bin/sitecustomize.py" <<'PY_INJECT'
import os,pathlib,selectors,subprocess
root=pathlib.Path(os.environ['OUTDIR']); original_popen=subprocess.Popen; original_selector=selectors.DefaultSelector
failed=False
def popen(*args,**kwargs):
    child=original_popen(*args,**kwargs)
    with (root/'setup-child-pids').open('a') as output: output.write(str(child.pid)+'\n')
    return child
class FailFirstRegister(original_selector):
    def register(self,*args,**kwargs):
        global failed
        if not failed:
            failed=True; (root/'setup-registration-failed').write_text('after-spawn\n')
            raise OSError('injected selector registration failure')
        return super().register(*args,**kwargs)
subprocess.Popen=popen; selectors.DefaultSelector=FailFirstRegister
PY_INJECT
 export PYTHONPATH="$OUTDIR/bin"
fi
if local_time_capture_failure "$OUTDIR/private"; then exit 0; else exit 23; fi
`
			fixture := runOwnedComposeFixture(t, script, []string{helper, tc.name}, nil, 15*time.Second)
			if !fixture.controlQualified() {
				t.Fatalf("terminal diagnostic fixture custody failed: %v %s", fixture.controlErr, fixture.output)
			}
			if (fixture.commandErr == nil) != tc.complete {
				t.Fatalf("capture completion=%v want %t: %s", fixture.commandErr, tc.complete, fixture.output)
			}
			private := filepath.Join(fixture.directory, "private")
			var record struct {
				Complete          bool  `json:"complete"`
				ProcessContinuity *bool `json:"process_continuity"`
				Commands          map[string]struct {
					ExitCode        int  `json:"exit_code"`
					TimedOut        bool `json:"timed_out"`
					DirectReaped    bool `json:"direct_reaped"`
					StdoutTruncated bool `json:"stdout_truncated"`
					StderrTruncated bool `json:"stderr_truncated"`
				} `json:"commands"`
			}
			raw, err := os.ReadFile(filepath.Join(private, "time-relay.capture.json"))
			if err != nil || json.Unmarshal(raw, &record) != nil || record.Complete != tc.complete {
				t.Fatalf("explicit relay capture result differs: %s %v", raw, err)
			}
			var other struct {
				Complete bool `json:"complete"`
			}
			raw, err = os.ReadFile(filepath.Join(private, "time-fixture.capture.json"))
			if err != nil || json.Unmarshal(raw, &other) != nil || !other.Complete {
				t.Fatalf("relay failure prevented independent fixture observation: %s %v", raw, err)
			}
			entries, err := os.ReadDir(private)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				info, err := entry.Info()
				data, readErr := os.ReadFile(filepath.Join(private, entry.Name()))
				if err != nil || readErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || strings.Contains(string(data), "never-copy-this-env") || strings.Contains(string(data), `"Env"`) {
					t.Fatalf("private bounded projection leaked context or permissions: %s %v %v", entry.Name(), err, readErr)
				}
				if strings.HasSuffix(entry.Name(), ".log") && len(data) > 65536 {
					t.Fatalf("unbounded stream: %s %d", entry.Name(), len(data))
				}
			}
			terminal, terminalErr := os.ReadFile(filepath.Join(private, "time-relay.terminal.json"))
			if (terminalErr == nil) != tc.terminal {
				t.Fatalf("terminal ownership admission differs: %s %v", terminal, terminalErr)
			}
			if tc.name == "terminal-exit" && (!strings.Contains(string(terminal), `"OOMKilled":true`) || !strings.Contains(string(terminal), `"ExitCode":137`) || !strings.Contains(string(terminal), `"FinishedAt":"2026-10-30T09:41:55Z"`) || record.ProcessContinuity == nil || !*record.ProcessContinuity) {
				t.Fatalf("unhealthy original terminal state was lost: %s", terminal)
			}
			commands, _ := os.ReadFile(filepath.Join(fixture.directory, "commands"))
			relayLogs := strings.Contains(string(commands), "logs --timestamps --tail 256 "+strings.Repeat("1", 64))
			if relayLogs != tc.logs {
				t.Fatalf("unadmitted identity reached logs: %s", commands)
			}
			if strings.Contains(string(commands), "start ") || strings.Contains(string(commands), "stop ") {
				t.Fatalf("observation mutated target: %s", commands)
			}
			if tc.name == "oversize" {
				logs := record.Commands["logs"]
				if !logs.StdoutTruncated || !logs.StderrTruncated || !logs.DirectReaped {
					t.Fatalf("bounded streams not explicit: %+v", logs)
				}
			}
			if tc.name == "setup-register-fails" {
				marker, err := os.ReadFile(filepath.Join(fixture.directory, "setup-registration-failed"))
				if err != nil || string(marker) != "after-spawn\n" {
					t.Fatalf("actual registration failure not reached: %q %v", marker, err)
				}
				pids, err := os.ReadFile(filepath.Join(fixture.directory, "setup-child-pids"))
				if err != nil || len(strings.Fields(string(pids))) == 0 {
					t.Fatalf("actual query child not recorded: %q %v", pids, err)
				}
				for _, rawPID := range strings.Fields(string(pids)) {
					pid, err := strconv.Atoi(rawPID)
					if err != nil {
						t.Fatal(err)
					}
					if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
						t.Fatalf("query child survived registration failure: %d %v", pid, err)
					}
				}
			}
			if tc.name == "timeout" {
				logs := record.Commands["logs"]
				if !logs.TimedOut || !logs.DirectReaped {
					t.Fatalf("timed child not reaped: %+v", logs)
				}
				pidRaw, err := os.ReadFile(filepath.Join(fixture.directory, "timed-child-pid"))
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(pidRaw))
				if err != nil {
					t.Fatal(err)
				}
				if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
					t.Fatalf("timed Docker query remains: %d %v", pid, err)
				}
			}
		})
	}
}
