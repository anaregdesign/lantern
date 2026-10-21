package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"gopkg.in/yaml.v3"
)

func TestMembershipContainerMounts(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// Older Compose renderers omit false create_host_path values from JSON.
	// Require explicit false in our owned source overlays before accepting that
	// representation; an absent source setting must never become a safe default.
	for name, mounts := range map[string]int{
		"docker-compose.membership.yml":                3,
		"docker-compose.graph-snapshot.membership.yml": 1,
	} {
		raw, err := os.ReadFile(filepath.Join(root, "deploy/compose", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := membershipBindSource(raw, mounts); err != nil {
			t.Fatalf("unsafe source overlay %s: %v", name, err)
		}
	}
	docker, err := exec.LookPath("docker")
	if err != nil || exec.CommandContext(t.Context(), docker, "compose", "version", "--short").Run() != nil {
		t.Skip("Docker Compose required for rendered deployment contract; source overlays checked")
	}
	for _, graph := range []bool{false, true} {
		t.Run(map[bool]string{false: "membership base", true: "graph Snapshot bind"}[graph], func(t *testing.T) {
			dir := t.TempDir()
			var environment []byte
			if graph {
				// These explicit operator inputs are retained by the mount-only
				// overlay. Rendering does not prove the host leaf's real owner,
				// permissions or the installer's ancestor checks.
				environment = []byte("LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR=/var/lib/lantern-snapshot-spool\nLANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES=134217728\nLANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES=33554432\n")
			}
			for i := 0; i < 3; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("lantern-%d.env", i)), environment, 0600); err != nil {
					t.Fatal(err)
				}
			}
			files := []string{"docker-compose.yml", "docker-compose.ha.yml", "docker-compose.membership.yml"}
			if graph {
				// The final bind overlay is independent of the image-owned
				// named-volume overlay; the operator provisions this host leaf
				// for the membership workload's explicit runtime UID.
				files = append(files, "docker-compose.graph-snapshot.membership.yml")
			}
			args := []string{"compose"}
			for _, name := range files {
				args = append(args, "-f", filepath.Join(root, "deploy/compose", name))
			}
			cmd := exec.CommandContext(t.Context(), docker, append(args, "config", "--format", "json")...)
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, "LANTERN_") && !strings.HasPrefix(v, "COMPOSE_") {
					cmd.Env = append(cmd.Env, v)
				}
			}
			cmd.Env = append(cmd.Env, "LANTERN_HA_CONFIG_DIR="+dir, "LANTERN_MEMBERSHIP_ROOT="+dir, "LANTERN_SERVICE_UID=1001", "LANTERN_SERVICE_GID=1001", "LANTERN_HA_ADMIN_UPSTREAM=https://lantern-0:6380")
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("render private mounts: %v: %s", err, raw)
			}
			var rendered struct {
				Services map[string]struct {
					User        string
					Restart     string
					ReadOnly    bool `json:"read_only"`
					Environment map[string]string
					Volumes     []struct {
						Type, Source, Target string
						ReadOnly             bool `json:"read_only"`
						Bind                 struct {
							Create *bool `json:"create_host_path"`
						}
					}
				}
			}
			if err = json.Unmarshal(raw, &rendered); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				node, ok := rendered.Services[fmt.Sprintf("lantern-%d", i)]
				mounts := 3
				if graph {
					mounts++
				}
				if !ok || node.User != "1001:1001" || node.Restart != "no" || !node.ReadOnly || len(node.Volumes) != mounts {
					t.Fatal("unsafe supervisor/mount defaults", i)
				}
				seen := map[string]bool{}
				for _, mount := range node.Volumes {
					parts := map[string]string{"/run/lantern-config": "static", "/run/lantern-renewal": "renewable", "/state": "state"}
					if graph {
						parts["/var/lib/lantern-snapshot-spool"] = "snapshot-spool"
					}
					part := parts[mount.Target]
					if part == "" || mount.Type != "bind" || mount.Source != filepath.Join(dir, fmt.Sprintf("node-%d", i), part) || (mount.Bind.Create != nil && *mount.Bind.Create) || mount.ReadOnly != (part == "static" || part == "renewable") {
						t.Fatal("broad, writable-input or auto-created mount", i, mount.Target)
					}
					seen[part] = true
				}
				if len(seen) != mounts {
					t.Fatal("missing private mount")
				}
				spoolEnvironment := map[string]string{
					"LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR":             "/var/lib/lantern-snapshot-spool",
					"LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES":  "134217728",
					"LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES": "33554432",
				}
				settings := 0
				for key, value := range node.Environment {
					if strings.HasPrefix(key, "LANTERN_GRAPH_SNAPSHOT_SPOOL_") {
						settings++
						if !graph || value == "" || value != spoolEnvironment[key] {
							t.Fatal("unexpected graph Snapshot spool setting", i, key, value)
						}
					}
				}
				if graph && settings != len(spoolEnvironment) {
					t.Fatal("graph Snapshot spool settings missing", i)
				}
			}
		})
	}
}

func membershipBindSource(raw []byte, mounts int) error {
	var source struct {
		Services map[string]struct {
			Volumes []struct {
				Type string
				Bind struct {
					Create *bool `yaml:"create_host_path"`
				}
			}
		}
	}
	if err := yaml.Unmarshal(raw, &source); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("lantern-%d", i)
		node, ok := source.Services[name]
		if !ok || len(node.Volumes) != mounts {
			return fmt.Errorf("%s: missing private mount", name)
		}
		for _, mount := range node.Volumes {
			if mount.Type != "bind" || mount.Bind.Create == nil || *mount.Bind.Create {
				return fmt.Errorf("%s: bind must explicitly disable host-path creation", name)
			}
		}
	}
	return nil
}

func TestMembershipBindSource(t *testing.T) {
	for _, tc := range []struct {
		name, mount string
		valid       bool
	}{
		{"explicit false", "{type: bind, bind: {create_host_path: false}}", true},
		{"explicit true", "{type: bind, bind: {create_host_path: true}}", false},
		{"missing setting", "{type: bind, bind: {}}", false},
		{"null setting", "{type: bind, bind: {create_host_path: null}}", false},
		{"missing bind", "{type: bind}", false},
		{"short syntax", "'/source:/target:ro'", false},
		{"wrong type", "{type: volume, bind: {create_host_path: false}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "services:\n"
			for i := 0; i < 3; i++ {
				raw += fmt.Sprintf("  lantern-%d:\n    volumes: !override\n      - %s\n", i, tc.mount)
			}
			if err := membershipBindSource([]byte(raw), 1); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

// The existing private TLS controller qualifies ordinary public reads only.
// Publication/accepted checkpoint digests and CLEAN are observed independently
// by the host harness. No new login, management change or data write is needed.
func (g *custodyProcessGate) membershipPhase(phase string) {
	g.t.Helper()
	if phase == "finish" {
		return
	}
	if phase == "delayed" {
		// The stream retains one exact candidate until every target is durable.
		// Holding it past that candidate's expiry may affect other data owners
		// too; successful control publication is not uninterrupted availability.
		_, err := g.data[0].GetVertex(g.t.Context(), securityWireRequest(g.token, &pb.GetVertexRequest{Key: "orders:membership-probe"}))
		if connect.CodeOf(err) != connect.CodeUnavailable {
			g.t.Fatal("expired target did not refuse protected read", err)
		}
		return
	}
	switch phase {
	case "ready", "recovered", "crossed-window", "resumed":
		g.waitReady(0, 1, 2)
		for _, data := range g.data {
			_, err := data.GetVertex(g.t.Context(), securityWireRequest(g.token, &pb.GetVertexRequest{Key: "orders:membership-probe"}))
			if connect.CodeOf(err) != connect.CodeNotFound {
				g.t.Fatal("ordinary protected read was not admitted", err)
			}
		}
	default:
		g.t.Fatal("unknown membership phase")
	}
}
