package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBenchCleanupRemovesRealProfileResources(t *testing.T) {
	if os.Getenv("RUN_BENCH_CLEANUP_GATE") != "1" {
		t.Skip("set RUN_BENCH_CLEANUP_GATE=1 and LANTERN_BENCH_CLEANUP_IMAGE for the real profile lifecycle")
	}
	image := os.Getenv("LANTERN_BENCH_CLEANUP_IMAGE")
	if image == "" || strings.ContainsAny(image, "\n\r\" ") {
		t.Fatal("an explicit local immutable image is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "docker", "image", "inspect", image).CombinedOutput(); err != nil {
		t.Fatalf("local image unavailable: %v %s", err, output)
	}
	project := fmt.Sprintf("lantern-bench-cleanup-%d", time.Now().UnixNano())
	dir := t.TempDir()
	compose := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  diagnostics:\n    profiles: [off-diagnostics]\n    image: \""+image+"\"\n    command: [sh, -c, 'sleep 120']\n    volumes: [state:/owned]\nvolumes:\n  state:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "testbed", "bench", "owned_compose.sh"))
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "COMPOSE_PROJECT_NAME="+project)
	down := func(ctx context.Context) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "bash", "-c", `source "$1"; down_owned_compose "$COMPOSE_PROJECT_NAME" -f "$2"`, "bench-cleanup", helper, compose)
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if output, err := down(cleanupCtx); err != nil {
			t.Errorf("owned residue cleanup: %v %s", err, output)
		}
	})
	up := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "--profile", "off-diagnostics", "up", "-d", "--pull", "never")
	up.Env = env
	if output, err := up.CombinedOutput(); err != nil {
		t.Fatalf("real profile startup: %v %s", err, output)
	}
	for _, args := range [][]string{{"ps", "-aq"}, {"volume", "ls", "-q"}, {"network", "ls", "-q"}} {
		args = append(args, "--filter", "label=com.docker.compose.project="+project)
		output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil || len(strings.TrimSpace(string(output))) == 0 {
			t.Fatalf("profile lifecycle control did not create resources: %v %s", err, output)
		}
	}
	if output, err := down(ctx); err != nil {
		t.Fatalf("real profile cleanup/residue proof: %v %s", err, output)
	}
}
