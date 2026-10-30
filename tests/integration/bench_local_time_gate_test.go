//go:build unix

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestBenchLocalTimeProductionWire is the one opt-in, no-load Docker mechanism
// check. The normal source gate never enables it or acquires another source.
func TestBenchLocalTimeProductionWire(t *testing.T) {
	if os.Getenv("RUN_BENCH_LOCAL_TIME_GATE") != "1" {
		t.Skip("set RUN_BENCH_LOCAL_TIME_GATE=1 for the bounded local UDP mechanism")
	}
	image, result := os.Getenv("EXPECTED_LANTERN_IMAGE_ID"), os.Getenv("LANTERN_BENCH_LOCAL_TIME_RESULT")
	if !strings.HasPrefix(image, "sha256:") || len(image) != 71 || result == "" || !filepath.IsAbs(result) {
		t.Fatal("explicit immutable image and new absolute result path required")
	}
	if _, err := os.Lstat(result); !os.IsNotExist(err) {
		t.Fatal("mechanism result must not already exist")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "testbed/bench/run.sh", "receipt_admission_lookup")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LANTERN_BENCH_LOCAL_TIME_MECHANISM=1", "LEAK_GATE_ONLY=1", "KEEP_UP=0", "SKIP_UP=0", "LANTERN_BENCH_RETAINED_HEAP=0")
	// Regular inherited output avoids pipe-copy descendants delaying Wait.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 150 * time.Second
	if err := cmd.Run(); err != nil {
		t.Fatalf("local UDP mechanism: %v", err)
	}
	info, err := os.Lstat(result)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("private regular mechanism receipt required", err)
	}
	body, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Protocol                  string `json:"protocol"`
		Verdict                   string `json:"verdict"`
		Qualification             *bool  `json:"qualification"`
		InitializedMembers        int    `json:"initialized_members"`
		ReadyMembers              int    `json:"ready_members"`
		AuthenticatedHTTP2Members int    `json:"authenticated_http2_members"`
		UnanchoredResumeRefused   bool   `json:"unanchored_resume_refused"`
		ExternalFallback          *bool  `json:"external_fallback"`
		InitializationRepeated    *bool  `json:"initialization_repeated"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Protocol != "bench_local_udp_mechanism_v1" || report.Verdict != "pass" || report.Qualification == nil || *report.Qualification ||
		report.InitializedMembers != 3 || report.ReadyMembers != 3 || report.AuthenticatedHTTP2Members != 3 ||
		!report.UnanchoredResumeRefused || report.ExternalFallback == nil || *report.ExternalFallback ||
		report.InitializationRepeated == nil || *report.InitializationRepeated {
		t.Fatalf("incomplete local UDP mechanism: %+v", report)
	}
}
