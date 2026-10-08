package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/provider"
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

func TestLocalFixtureCreatesOwnedHomogeneousWorkloadConfig(t *testing.T) {
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			for _, entry := range os.Environ() {
				for index, ch := range entry {
					if ch == '=' {
						name := entry[:index]
						if len(name) >= 8 && name[:8] == "LANTERN_" {
							t.Setenv(name, "")
							_ = os.Unsetenv(name)
						}
						break
					}
				}
			}
			dir := filepath.Join(t.TempDir(), "new-fixture")
			result, err := generate(dir, []int{16380, 16381}, []int{17380, 17381}, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Nodes) != 2 {
				t.Fatal("node count")
			}
			output, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "oidc" {
				raw, err := os.ReadFile(result.TokenFile)
				if err != nil {
					t.Fatal(err)
				}
				var tokens []string
				if json.Unmarshal(raw, &tokens) != nil || len(tokens) != 1 {
					t.Fatal("machine token file")
				}
				if bytes.Contains(output, []byte(tokens[0])) {
					t.Fatal("fixture output contains credential material")
				}
			}
			for _, node := range result.Nodes {
				t.Run(node.Name, func(t *testing.T) {
					for name, value := range node.Environment {
						t.Setenv(name, value)
					}
					config, err := provider.NewConfig()
					if err != nil {
						t.Fatal(err)
					}
					if config.Security.Mode != mode || config.PeerPlane.Identity.SelfIdentity == "" || config.Changes.Options.CurrentKeyVersion != 1 {
						t.Fatal("incomplete trust composition")
					}
					peer, cleanup, err := provider.NewConfiguredPeerIdentity(config.PeerPlane)
					if err != nil {
						t.Fatal(err)
					}
					defer cleanup()
					if peer.CheckWorkload(context.Background()) != nil || peer.TLSConfig().MinVersion != tls.VersionTLS13 {
						t.Fatal("unqualified workload fixture")
					}
				})
			}
			if _, err := generate(dir, []int{16380}, nil, mode, ""); err == nil {
				t.Fatal("existing fixture directory overwritten")
			}
		})
	}
}
func TestFixturePortsRejectAmbiguousOrDuplicateInput(t *testing.T) {
	for _, raw := range []string{"0", "65536", "06380", "6380,6380", "6380,"} {
		if _, err := ports(raw); err == nil {
			t.Fatal("invalid fixture port accepted", raw)
		}
	}
}
