package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{`{"msg":"failed to initialize app","err":"private-token-key-body"}`, "initialization"},
		{`{"msg":"server exited with error","err":"private-token-key-body"}`, "server_exit"},
		{"private-token-key-body", "none"},
		{strings.Repeat("x", 64<<10) + "\n" + `{"msg":"failed to initialize app","err":"bind: address already in use"}`, "none"},
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
