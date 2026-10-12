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
)

func TestMembershipContainerMounts(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil || exec.CommandContext(t.Context(), docker, "compose", "version", "--short").Run() != nil {
		t.Skip("Docker Compose required for rendered deployment contract")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("lantern-%d.env", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"compose"}
	for _, name := range []string{"docker-compose.yml", "docker-compose.ha.yml", "docker-compose.membership.yml"} {
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
			User     string
			Restart  string
			ReadOnly bool `json:"read_only"`
			Volumes  []struct {
				Type, Source, Target string
				ReadOnly             bool `json:"read_only"`
				Bind                 struct {
					Create bool `json:"create_host_path"`
				}
			}
		}
	}
	if err = json.Unmarshal(raw, &rendered); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		node := rendered.Services[fmt.Sprintf("lantern-%d", i)]
		if node.User != "1001:1001" || node.Restart != "no" || !node.ReadOnly || len(node.Volumes) != 3 {
			t.Fatal("unsafe supervisor/mount defaults", i)
		}
		seen := map[string]bool{}
		for _, mount := range node.Volumes {
			part := map[string]string{"/run/lantern-config": "static", "/run/lantern-renewal": "renewable", "/state": "state"}[mount.Target]
			if part == "" || mount.Type != "bind" || mount.Source != filepath.Join(dir, fmt.Sprintf("node-%d", i), part) || mount.Bind.Create || mount.ReadOnly != (part != "state") {
				t.Fatal("broad, writable-input or auto-created mount", i, mount.Target)
			}
			seen[part] = true
		}
		if len(seen) != 3 {
			t.Fatal("missing private mount")
		}
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
