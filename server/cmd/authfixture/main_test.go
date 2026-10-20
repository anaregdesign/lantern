package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedQueryCLIRejectsOtherProfiles(t *testing.T) {
	if os.Getenv("LANTERN_QUERY_CLI_TEST") == "1" {
		flag.CommandLine = flag.NewFlagSet("authfixture", flag.ExitOnError)
		os.Args = append([]string{"authfixture"}, strings.Split(os.Getenv("LANTERN_QUERY_CLI_ARGS"), " ")...)
		main()
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, arguments := range []string{
		"-protected-query -mode off -public-ports 16380",
		"-protected-query -mode oidc -public-ports 16380 -peer-ports 17380 -serve /missing",
		"-protected-query -mode oidc -public-ports 16380 -receipt -serve /missing",
		"-protected-query -mode oidc -public-ports 16380 -transport-probe -serve /missing",
		"-protected-query -compose -public-ports 16380,16381,16382 -serve /missing",
		"-protected-query -compose -renew -directory /missing",
	} {
		command := exec.CommandContext(t.Context(), binary, "-test.run=^TestProtectedQueryCLIRejectsOtherProfiles$")
		command.Env = append(os.Environ(), "LANTERN_QUERY_CLI_TEST=1", "LANTERN_QUERY_CLI_ARGS="+arguments)
		output, err := command.CombinedOutput()
		exit, failed := err.(*exec.ExitError)
		if !failed || exit.ExitCode() != 1 || (!strings.Contains(string(output), "authfixture:") && !strings.Contains(string(output), "authfixture_failure:configuration")) {
			t.Fatalf("profile boundary accepted %q: %v %s", arguments, err, output)
		}
	}
}

func TestFixturePortsRejectAmbiguousOrDuplicateInput(t *testing.T) {
	for _, raw := range []string{"0", "65536", "06380", "6380,6380", "6380,"} {
		if _, err := ports(raw); err == nil {
			t.Fatal("invalid fixture port accepted", raw)
		}
	}
}

func TestNativeFixtureGraphSnapshotSpoolIsExplicitPrivateAndResolved(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "standalone"
		var peerPorts []int
		if native {
			name, peerPorts = "private peer", []int{17380}
		}
		t.Run(name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "fixture")
			result, err := generate(directory, []int{16380}, peerPorts, "off", "")
			if err != nil {
				t.Fatal(err)
			}
			env := result.Nodes[0].Environment
			if !native {
				if env["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] != "" || env["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"] != "" || env["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"] != "" {
					t.Fatal("standalone fixture acquired unnecessary Snapshot storage")
				}
				return
			}
			spool := env["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"]
			resolved, err := filepath.EvalSymlinks(spool)
			if err != nil || spool != resolved || !filepath.IsAbs(spool) || filepath.Base(spool) != "node-0-graph-snapshot-spool" {
				t.Fatal("native fixture did not name its dedicated resolved Snapshot directory", err)
			}
			info, err := os.Stat(spool)
			if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || env["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"] != "134217728" || env["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"] != "33554432" {
				t.Fatal("native fixture Snapshot storage lost its private mode or explicit capacity", err)
			}
		})
	}
}
