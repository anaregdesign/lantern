//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFixtureReportDescriptorIsPrivateToSupervisor(t *testing.T) {
	const role = "LANTERN_FIXTURE_REPORT_ROLE"
	const expectedPath = "LANTERN_FIXTURE_TEST_REPORT"
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestFixtureReportDescriptorIsPrivateToSupervisor$"}
	switch os.Getenv(role) {
	case "child":
		original, err := os.Stat(os.Getenv(expectedPath))
		if err != nil {
			t.Fatal(err)
		}
		inherited, err := os.NewFile(3, "inherited").Stat()
		if err == nil && os.SameFile(original, inherited) {
			t.Fatal("supervised child inherited the failure descriptor")
		}
	case "supervisor":
		report, err := openFixtureFailureReport()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = report.Close() }()
		child := exec.CommandContext(t.Context(), binary, args...)
		child.Env = append(os.Environ(), role+"=child")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("child descriptor check: %v: %s", err, output)
		}
		writeFixtureFailure(report, os.ErrInvalid)
	default:
		path := filepath.Join(t.TempDir(), "report")
		report, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = report.Close() }()
		supervisor := exec.CommandContext(t.Context(), binary, args...)
		supervisor.ExtraFiles = []*os.File{report}
		supervisor.Env = append(os.Environ(), role+"=supervisor", expectedPath+"="+path)
		if output, err := supervisor.CombinedOutput(); err != nil {
			t.Fatalf("supervisor descriptor check: %v: %s", err, output)
		}
		if raw, err := os.ReadFile(path); err != nil || string(raw) != "authfixture_failure:configuration\n" {
			t.Fatalf("report after child join: %v: %q", err, raw)
		}
	}
}
