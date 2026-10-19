package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
			if err := provisionFixtureAuthority(t.Context(), &metadata, dir, ""); err != nil {
				t.Fatal(err)
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
			if env["LANTERN_PEER_MEMBERSHIP_MODE"] != "resume" || env["LANTERN_SECURITY_STORE_MODE"] != "resume" || env["LANTERN_PEER_WORKLOAD_ID"] != metadata.Nodes[1].Environment["LANTERN_PEER_WORKLOAD_ID"] {
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
