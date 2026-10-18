package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestFixtureFailureCategory(t *testing.T) {
	private := errors.New("private credential and key content")
	for _, stage := range []string{"spawn", "readiness", "exit", "private credential"} {
		failure := &fixtureFailure{stage: stage, cause: private}
		wrapped := fmt.Errorf("wrapper: %w", failure)
		category := fixtureFailureCategory(wrapped)
		if strings.Contains(category, "private") || strings.Contains(failure.Error(), "credential") || !errors.Is(wrapped, private) {
			t.Fatal("public category exposed arbitrary data or lost its private cause")
		}
		if stage == "private credential" && category != "configuration" || stage != "private credential" && category != stage {
			t.Fatal("wrong fixed category", category)
		}
	}
	if fixtureFailureCategory(private) != "configuration" {
		t.Fatal("arbitrary error changed the category")
	}
}

func TestFixtureStartupFailureRetainsValidatedRecord(t *testing.T) {
	state := decisiontrace.StartupSnapshot{Count: 4, PublishedAttempt: 4, StartedAttempt: 4, CompletedAttempt: 4,
		AnchoringContext: decisiontrace.ContextDeadlineExceeded, IncomingContext: decisiontrace.ContextLive, OwnerContext: decisiontrace.ContextLive}
	for i := range state.Attempts {
		state.Attempts[i] = decisiontrace.StartupAttempt{Attempt: uint64(i + 1), Stage: "read", Reason: "read_timeout", HasReadBudget: true,
			ReadRemainingNS: 3_000_000_000, ExchangeContext: decisiontrace.ContextDeadlineExceeded, IncomingContext: decisiontrace.ContextLive,
			ReadCount: 2, FirstReadError: "aborted", FinalReadError: "timeout"}
	}
	raw, err := json.Marshal(decisiontrace.StartupRecord{Message: decisiontrace.StartupMessage, Version: 1, State: state})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "child")
	for _, tail := range []string{"", "private-token-body", strings.Repeat("x", 64<<10)} {
		if err := os.WriteFile(path, append(append([]byte("private-token-body\n"), raw...), []byte("\n"+tail)...), 0600); err != nil {
			t.Fatal(err)
		}
		observed := fixtureStartupFailure(path)
		if observed.Status != "observed" || observed.State == nil || *observed.State != state {
			t.Fatal("lost complete bounded snapshot", observed)
		}
		var output bytes.Buffer
		writeFixtureFailure(&output, &fixtureFailure{stage: "spawn", cause: &fixtureReadinessError{cause: errors.New("private-token-body"), diagnostic: fixtureReadinessDiagnostic{Startup: observed}}})
		if output.Len() > 4096 || strings.Contains(output.String(), "private-token-body") || !strings.Contains(output.String(), `"first_read_error":"aborted"`) {
			t.Fatal("unsafe fixture record")
		}
	}
	for _, tc := range []struct {
		raw    []byte
		status string
	}{
		{append(bytes.Replace(raw, []byte(`"first_read_error":"aborted"`), []byte(`"first_read_error":"private-token"`), 1), '\n'), "invalid"},
		{append(bytes.Replace(raw, []byte(`"state":{`), []byte(`"body":"private-token","state":{`), 1), '\n'), "invalid"},
		{append(append(append([]byte{}, raw...), '\n'), append(raw, '\n')...), "invalid"},
		{raw[:len(raw)-1], "incomplete"},
		{bytes.Repeat([]byte("x"), (64<<10)+1), "truncated"},
		{[]byte("private-token-body\n"), "not_observed"},
	} {
		if err := os.WriteFile(path, tc.raw, 0600); err != nil {
			t.Fatal(err)
		}
		got := fixtureStartupFailure(path)
		if got.Status != tc.status || got.State != nil {
			t.Fatal("invalid private record escaped", got.Status)
		}
	}
}

func TestFixtureServerLogClassificationIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.log")
	for _, tc := range []struct{ raw, want string }{
		{`{"msg":"server exited with error","err":"private-token-key-body"}`, "server_exit"},
		{`{"msg":"server exited with error","err":"initialize app: private-token-key-body"}`, "initialization"},
		{`{"msg":"server exited with error","err":"initialize app: open /etc/ntp.conf: no such file or directory; private-token"}`, "source_input_missing"},
		{`{"msg":"server exited with error","err":"initialize app: unusable current-authority time measurement; private-token"}`, "authority_time"},
		{`{"msg":"server exited with error","err":"initialize app: listen tcp :1234: bind: address already in use; private-token"}`, "bind_address_in_use"},
		{`{"msg":"server exited with error","err":"run: unusable current-authority time measurement; private-token"}`, "server_exit"},
		{"private-token-key-body", "none"},
		{strings.Repeat("x", 64<<10) + "\n" + `{"msg":"server exited with error","err":"initialize app: bind: address already in use"}`, "none"},
	} {
		if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		if got := fixtureServerLogCategory(path); got != tc.want {
			t.Fatal("wrong bounded classification", got)
		}
	}
	var output bytes.Buffer
	writeFixtureFailure(&output, &fixtureFailure{stage: "spawn", cause: errors.New("private-token-key-body")})
	if output.String() != "authfixture_failure:spawn\n" {
		t.Fatal("private failure escaped fixed category")
	}
}

func TestFixtureAdoptionFailureRetainsOnlyBoundedPassiveEvidence(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "child")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, _ = file.WriteString("{\"msg\":\"server exited with error\",\"err\":\"initialize app: open /etc/ntp.conf: no such file or directory; private-token\"}\n")
	done := make(chan struct{})
	close(done)
	private := errors.New("private-token-key-body")
	failure := fixtureAdoptionFailure(private, &fixtureProcess{done: done, command: &exec.Cmd{}, log: file}, 2, 12345, 37*time.Millisecond)
	var output bytes.Buffer
	writeFixtureFailure(&output, failure)
	if !errors.Is(failure, private) || strings.Contains(output.String(), "private-token") || strings.Contains(output.String(), file.Name()) {
		t.Fatal("lost cause or disclosed private child text")
	}
	for _, expected := range []string{"authfixture_failure:spawn", `"server_log":"source_input_missing"`, `"child_exited":true`, `"port":12345`, `"elapsed_ms":37`, `"health_probes":0`, `"observation":{"status":"not_observed"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatal("missing adoption evidence", expected)
		}
	}
}
