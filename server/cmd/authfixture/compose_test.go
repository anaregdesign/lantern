package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
			if receipt {
				if err := exportComposeFixture(&metadata, dir); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := exportComposeFixture(&metadata, dir); err == nil || !strings.Contains(err.Error(), "dedicated private Snapshot spool storage") {
					t.Fatal("graph Compose export did not require explicit container spool preparation", err)
				}
				// Restart identity policy is a separate pure fixture boundary.
				// Seed only its owned metadata; this does not qualify or launch
				// the unprepared graph-only Compose topology.
				if _, err := writeJSON(dir, "fixture.json", metadata); err != nil {
					t.Fatal(err)
				}
				config, err := composeEnvFile(metadata.Nodes[1].Environment)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := writeFile(dir, "lantern-1.env", config); err != nil {
					t.Fatal(err)
				}
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

func TestGraphComposeFixtureRefusesImplicitSnapshotStorageBeforeExport(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "fixture")
	result, err := generateTopology(directory, []int{6380, 6381, 6382}, []int{17380, 17381, 17382}, "off", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := exportComposeFixture(&result, directory); err == nil || !strings.Contains(err.Error(), "not qualified") || !strings.Contains(err.Error(), "dedicated private Snapshot spool storage") {
		t.Fatal("unprepared graph Compose topology was exported as usable", err)
	}
	for i, node := range result.Nodes {
		spool := node.Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"]
		resolved, err := filepath.EvalSymlinks(spool)
		if err != nil || spool != resolved || node.Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"] != "134217728" {
			t.Fatal("failed export changed its explicit host-only storage owner", err)
		}
		for _, path := range []string{filepath.Join(directory, "lantern-"+string(rune('0'+i))+".env"), filepath.Join(directory, "lantern-"+string(rune('0'+i)))} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("unprepared Compose export created runtime material", err)
			}
		}
	}
}
