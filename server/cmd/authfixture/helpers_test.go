package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func fixtureServerBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("LANTERN_TEST_LAUNCH_SERVER"); binary != "" {
		return binary
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary := filepath.Join(t.TempDir(), "server"+suffix)
	command := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "..")
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build production child: %v\n%s", err, raw)
	}
	return binary
}
