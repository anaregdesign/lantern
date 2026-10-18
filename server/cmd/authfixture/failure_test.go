package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
