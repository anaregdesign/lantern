package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeFixtureIsolatesWorkloadSecretsAndRenewsSameCohort(t *testing.T) {
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private")
			result, err := generateTopology(dir, []int{6380, 6381, 6382}, []int{17380, 17381, 17382}, mode, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "oidc" {
				if err := addFixtureReceipts(&result, dir); err != nil {
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
				for _, name := range []string{"operator.key", "tokens.json", "fixture.json"} {
					if _, err := os.Stat(filepath.Join(mount, name)); !os.IsNotExist(err) {
						t.Fatal("host-only material mounted", name)
					}
				}
				if i > 0 {
					if _, err := os.Stat(filepath.Join(mount, "writer.key")); !os.IsNotExist(err) {
						t.Fatal("replica possesses authority signing key")
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
					private := strings.HasSuffix(file.Name(), ".key") || file.Name() == "machines.json" || file.Name() == "cdc-keys.json"
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

func TestComposeRestartSeparatesWorkloadAndGraphContinuity(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "ephemeral graph", true: "durable receipt WAL"}[receipt], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private")
			metadata, err := generateTopology(dir, []int{6380, 6381, 6382}, []int{17380, 17381, 17382}, "oidc", "", true)
			if err != nil {
				t.Fatal(err)
			}
			if receipt {
				if err := addFixtureReceipts(&metadata, dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := exportComposeFixture(&metadata, dir); err != nil {
				t.Fatal(err)
			}
			original := metadata.Nodes[1].Environment["LANTERN_NODE_ID"]
			if err := prepareComposeRestart(dir, "lantern-1"); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
			if err != nil {
				t.Fatal(err)
			}
			var current fixture
			if json.Unmarshal(raw, &current) != nil {
				t.Fatal("invalid restart metadata")
			}
			env := current.Nodes[1].Environment
			if env["LANTERN_PEER_MEMBERSHIP_MODE"] != "resume" || env["LANTERN_SECURITY_STORE_MODE"] != "restart" || env["LANTERN_PEER_WORKLOAD_ID"] != metadata.Nodes[1].Environment["LANTERN_PEER_WORKLOAD_ID"] {
				t.Fatal("explicit runtime continuity changed workload authority")
			}
			if (env["LANTERN_NODE_ID"] == original) != receipt || receipt && env["LANTERN_RECEIPT_WAL_MODE"] != "restart" {
				t.Fatal("graph origin and WAL continuity were conflated")
			}
			if err := prepareComposeRestart(dir, "lantern-1"); err == nil {
				t.Fatal("restart was silently prepared twice")
			}
			if err := prepareComposeRestart(dir, "unrelated-workload"); err == nil {
				t.Fatal("unowned workload restart accepted")
			}
		})
	}
}
