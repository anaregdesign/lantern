package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This gate exercises the collector's real normal-build child only before App
// construction. It never opens a configured time owner or contacts NTP.
func TestOFFSourceCollectorOfflineContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("OFF collector uses POSIX child pipes; existing Windows runtime gates remain independent")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-B", "-m", "unittest", "discover", "-s", "testbed/timeproof", "-p", "*_test.py")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("offline collector contracts: %v\n%s", err, output)
	}
	if os.Getenv("GOFLAGS") != "" {
		t.Fatal("normal child contract does not support GOFLAGS overrides")
	}
	binary := filepath.Join(t.TempDir(), "off-child.test")
	command = exec.CommandContext(ctx, "go", "test", "-c", "-ldflags=-X github.com/anaregdesign/lantern/server/cmd.offSourceRevision=offline-contract", "-o", binary, "./server/cmd")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("normal child compilation: %v\n%s", err, output)
	}
	nonce := strings.Repeat("1", 64)
	var environment []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "LANTERN_") && !strings.HasPrefix(key, "OFF_SOURCE_") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "OFF_SOURCE_CHILD_NONCE="+nonce)
	command = exec.CommandContext(ctx, binary, "-test.run=^TestOFFConfiguredSourceChild$", "-test.timeout=10s")
	command.Env = append(append([]string{}, environment...), "OFF_SOURCE_CHILD_IDENTITY=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("identity-only child: %v\n%s", err, output)
	}
	var identity map[string]any
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(line, "off_source ") {
			if identity != nil || json.Unmarshal([]byte(strings.TrimPrefix(line, "off_source ")), &identity) != nil {
				t.Fatal("missing/duplicate child identity")
			}
		}
	}
	if identity["event"] != "identity" || identity["revision"] != "offline-contract" || identity["normal_build"] != true || identity["nonce"] != nonce {
		t.Fatal("controlled or unprepared binary masqueraded as a normal child", identity)
	}
	command = exec.CommandContext(ctx, binary, "-test.run=^TestOFFConfiguredSourceChild$", "-test.timeout=10s")
	command.Env = environment
	command.Stdin = strings.NewReader(`{"phase":"reserve","nonce":"` + nonce + `","private":true}` + "\n")
	output, err = command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("invalid source configuration")) || bytes.Count(output, []byte("off_source ")) != 1 || !bytes.Contains(output, []byte(`"event":"reserved"`)) {
		t.Fatalf("unconfigured child reached App/source construction: %v\n%s", err, output)
	}
	if runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") || runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		command = exec.CommandContext(ctx, "go", "test", "-json", "-tags", "lantern_test_time", "-race", "-count=1", "-timeout=60s", "./server/cmd", "-run", "^TestOFFQualifiedAppFinalCounterLifecycle$")
		command.Dir = root
		command.Env = environment[:len(environment)-1] // No child-entry nonce.
		output, err = command.CombinedOutput()
		if err != nil {
			t.Fatalf("controlled OFF App lifecycle: %v\n%s", err, output)
		}
		executed := false
		for line := range strings.SplitSeq(string(output), "\n") {
			var event struct{ Action, Test string }
			if json.Unmarshal([]byte(line), &event) == nil && event.Test == "TestOFFQualifiedAppFinalCounterLifecycle" {
				if event.Action == "skip" || event.Action == "fail" {
					t.Fatal("controlled OFF App lifecycle did not qualify")
				}
				executed = executed || event.Action == "pass"
			}
		}
		if !executed {
			t.Fatal("controlled OFF App lifecycle selection was empty")
		}
		t.Log("controlled OFF App lifecycle PASS; synthetic loopback UTC only")
	}
}
