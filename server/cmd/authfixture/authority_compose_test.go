package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeFixtureIsolatesWorkloadSecretsAndRenewsSameCohort(t *testing.T) {
	for _, profile := range []struct {
		name, mode string
		receipt    bool
	}{
		{name: "OFF graph", mode: "off"},
		{name: "OIDC graph", mode: "oidc"},
		{name: "OIDC receipt", mode: "oidc", receipt: true},
	} {
		t.Run(profile.name, func(t *testing.T) {
			mode := profile.mode
			dir := filepath.Join(t.TempDir(), "private")
			result, err := generateTopology(dir, []int{6380, 6381, 6382}, []int{17380, 17381, 17382}, mode, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if profile.receipt {
				if err := addFixtureReceipts(&result, dir); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "oidc" {
				if err := provisionFixtureAuthority(t.Context(), &result, dir, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := exportComposeFixture(&result, dir); err != nil {
				t.Fatal(err)
			}
			for i, node := range result.Nodes {
				service := "lantern-" + string(rune('0'+i))
				mount := filepath.Join(dir, service)
				if node.PeerOrigin != "https://"+service+":6381" || node.Environment["LANTERN_PORT"] != "6380" || node.Environment["LANTERN_PEER_LISTEN_ADDR"] != ":6381" {
					t.Fatal("wrong container origins/listeners")
				}
				for _, name := range []string{"operator.key", "tokens.json", "fixture.json", "control-operator.key", "authority-control-operator.key"} {
					if _, err := os.Stat(filepath.Join(mount, name)); !os.IsNotExist(err) {
						t.Fatal("host-only material mounted", name)
					}
				}
				if i > 0 {
					if _, err := os.Stat(filepath.Join(mount, "writer.key")); !os.IsNotExist(err) {
						t.Fatal("replica possesses authority signing key")
					}
				}
				for other := range result.Nodes {
					if other == i {
						continue
					}
					for _, suffix := range []string{"-peer.key", "-public.key"} {
						if _, err := os.Stat(filepath.Join(mount, result.Nodes[other].Name+suffix)); !os.IsNotExist(err) {
							t.Fatal("workload mount contains another workload's private key", other, suffix)
						}
					}
				}
				if mode == "oidc" {
					original, err := os.ReadFile(filepath.Join(dir, "authority", fmt.Sprintf("node-%d", i+1), "vote.key"))
					if err != nil {
						t.Fatal(err)
					}
					mounted, err := os.ReadFile(filepath.Join(mount, "authority-vote.key"))
					if err != nil || !bytes.Equal(original, mounted) {
						t.Fatal("authority mount does not contain exactly its own voter key", err)
					}
				}
				files, err := os.ReadDir(mount)
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					info, err := file.Info()
					if err != nil || !info.Mode().IsRegular() {
						t.Fatal("workload material must be a regular file", file.Name(), err)
					}
					private := strings.HasPrefix(file.Name(), "authority-") || strings.HasSuffix(file.Name(), ".key") || file.Name() == "machines.json" || file.Name() == "cdc-keys.json"
					if private && info.Mode().Perm() != 0600 {
						t.Fatal("workload secret is not owner-only", file.Name(), info.Mode())
					}
				}
				cert, err := tls.LoadX509KeyPair(filepath.Join(mount, "server.pem"), filepath.Join(mount, "server.key"))
				if err != nil || cert.Leaf.VerifyHostname(service) != nil {
					t.Fatal("public certificate does not bind explicit workload DNS", err)
				}
				config, err := os.ReadFile(filepath.Join(dir, service+".env"))
				if err != nil || bytes.Contains(config, []byte("LANTERN_AUTH_TOKENS")) || bytes.Contains(config, []byte(dir)) || bytes.Contains(config, []byte("operator.key")) {
					t.Fatal("unsafe container configuration", err)
				}
				if profile.receipt {
					if bytes.Contains(config, []byte("LANTERN_GRAPH_SNAPSHOT_SPOOL_")) || node.Environment["LANTERN_RECEIPT_WAL_PATH"] != "/state/receipts.wal" {
						t.Fatal("receipt Compose configuration acquired graph-only scratch")
					}
				} else if node.Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] != composeGraphSnapshotSpoolDirectory || node.Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"] != "134217728" || node.Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"] != "33554432" {
					t.Fatal("graph Compose configuration lost explicit container scratch")
				}
				if mode == "off" && (strings.HasPrefix(node.PublicOrigin, "https:") || node.Environment["LANTERN_AUTH_MODE"] != "") {
					t.Fatal("OFF benchmark changed public auth/transport baseline")
				}
			}
			before, err := os.ReadFile(filepath.Join(dir, "membership.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := renewComposeMembership(dir, time.Now().UTC().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(dir, "membership.json"))
			if err != nil || bytes.Equal(before, after) {
				t.Fatal("membership was not renewed")
			}
			if mode == "oidc" {
				for i := range 3 {
					info, err := os.Stat(filepath.Join(dir, fmt.Sprintf("lantern-%d", i), "authority-membership.signed"))
					if err != nil || info.Mode().Perm() != 0600 {
						t.Fatal("renewal changed private authority input permissions", err)
					}
				}
			}
			var previous, next struct {
				Payload struct {
					Version uint64
					Domain  json.RawMessage
					Members json.RawMessage
				}
			}
			if json.Unmarshal(before, &previous) != nil || json.Unmarshal(after, &next) != nil || next.Payload.Version != previous.Payload.Version+1 || !bytes.Equal(previous.Payload.Domain, next.Payload.Domain) || !bytes.Equal(previous.Payload.Members, next.Payload.Members) {
				t.Fatal("renewal changed domain/workload or failed monotonic version")
			}
			if err := renewComposeMembership(dir, time.Now().UTC().Add(time.Hour)); err == nil {
				t.Fatal("expired operator continuity resumed implicitly")
			}
			if err := os.WriteFile(filepath.Join(dir, "lantern-1", "membership.json"), before, 0644); err != nil {
				t.Fatal(err)
			}
			if err := renewComposeMembership(dir, time.Now().UTC().Add(2*time.Minute)); err == nil {
				t.Fatal("mounted rollback/drift accepted")
			}
		})
	}
}
