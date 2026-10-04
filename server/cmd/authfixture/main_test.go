package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/server/provider"
)

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
