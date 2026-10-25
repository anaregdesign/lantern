package main

import (
	"encoding/json"
	"fmt"
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
			if err := exportComposeFixture(&metadata, dir); err != nil {
				t.Fatal(err)
			}
			for _, node := range metadata.Nodes {
				env := node.Environment
				if receipt {
					if env["LANTERN_RECEIPT_WAL_PATH"] != "/state/receipts.wal" {
						t.Fatal("receipt Compose fixture lost its durable state path")
					}
					for _, key := range []string{"LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"} {
						if _, exists := env[key]; exists {
							t.Fatal("receipt fixture retained graph Snapshot storage", key)
						}
					}
				} else if env["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] != "" || node.StartupFile == "" || env["LANTERN_RECEIPT_WAL_PATH"] != "" {
					t.Fatal("graph fixture mixed private scratch with receipt storage")
				}
				if _, err := os.Lstat(filepath.Join(dir, node.Name+"-graph-snapshot-spool")); !os.IsNotExist(err) {
					t.Fatal("Compose fixture prepared an unused host spool", err)
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
			if env["LANTERN_PEER_WORKLOAD_ID"] != metadata.Nodes[1].Environment["LANTERN_PEER_WORKLOAD_ID"] {
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

func TestGraphComposeFixtureMapsSnapshotStorageBeforeMountedFileExport(t *testing.T) {
	for _, mode := range []string{"", "graph-only"} {
		t.Run(map[string]string{"": "default graph", "graph-only": "explicit graph"}[mode], func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "fixture")
			result, err := generateTopology(directory, []int{6380, 6381, 6382}, []int{17380, 17381, 17382}, "off", "", true)
			if err != nil {
				t.Fatal(err)
			}
			for i, node := range result.Nodes {
				env := node.Environment
				if env["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] != composeGraphSnapshotSpoolDirectory || env["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"] != "134217728" || env["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"] != "33554432" {
					t.Fatal("Compose generation lost its explicit container spool profile")
				}
				if _, err := os.Lstat(filepath.Join(directory, node.Name+"-graph-snapshot-spool")); !os.IsNotExist(err) {
					t.Fatal("Compose generation created unused host scratch", err)
				}
				// A stale host path must be translated before the generic config
				// copier tries to open it. No such source file/directory exists.
				env["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] = filepath.Join(directory, fmt.Sprintf("must-not-copy-spool-%d", i))
				if mode != "" {
					env["LANTERN_RECEIPT_WAL_MODE"] = mode
				}
			}
			if err := exportComposeFixture(&result, directory); err != nil {
				t.Fatal("graph Compose export treated scratch as mounted configuration", err)
			}
			for i, node := range result.Nodes {
				service := fmt.Sprintf("lantern-%d", i)
				config, err := os.ReadFile(filepath.Join(directory, service+".env"))
				if err != nil || !strings.Contains(string(config), "LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR='"+composeGraphSnapshotSpoolDirectory+"'") || strings.Contains(string(config), directory) {
					t.Fatal("graph Compose environment retained host scratch", err)
				}
				if node.Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] != composeGraphSnapshotSpoolDirectory || node.Environment["LANTERN_RECEIPT_WAL_PATH"] != "" {
					t.Fatal("export did not preserve the graph-only storage boundary")
				}
				for _, path := range []string{filepath.Join(directory, fmt.Sprintf("must-not-copy-spool-%d", i)), filepath.Join(directory, service, "lantern-snapshot-spool")} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("export copied or provisioned scratch in fixture/config storage", err)
					}
				}
			}
		})
	}
}

func TestGraphComposeFixtureRefusesMissingExplicitSnapshotSettingsBeforeExport(t *testing.T) {
	for _, missing := range []string{"LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"} {
		t.Run(missing, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "fixture")
			result, err := generateTopology(directory, []int{6380, 6381, 6382}, []int{17380, 17381, 17382}, "off", "", true)
			if err != nil {
				t.Fatal(err)
			}
			delete(result.Nodes[2].Environment, missing)
			if err := exportComposeFixture(&result, directory); err == nil || !strings.Contains(err.Error(), "explicit Snapshot spool") {
				t.Fatal("graph Compose export inferred missing scratch configuration", err)
			}
			for i := range result.Nodes {
				service := fmt.Sprintf("lantern-%d", i)
				for _, path := range []string{filepath.Join(directory, service+".env"), filepath.Join(directory, service)} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("invalid final node partially exported earlier workloads", err)
					}
				}
			}
		})
	}
}
