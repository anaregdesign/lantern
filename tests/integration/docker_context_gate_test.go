package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This input-boundary gate uses Docker's real ignore/export implementation;
// ordinary unit runs remain independent of a local Docker daemon.
func TestDockerContextExcludesNativeCachesAndPrivateBenchState(t *testing.T) {
	if os.Getenv("RUN_DOCKER_CONTEXT_GATE") != "1" {
		t.Skip("set RUN_DOCKER_CONTEXT_GATE=1 for the real local BuildKit context gate")
	}
	ignore, err := os.ReadFile(filepath.Join("..", "..", ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	required := []string{"core/go.mod", "server/cmd/server.go", "pb/graph/v1/graph.pb.go", "proto/graph/v1/graph.proto", "sdks/node/src/client.ts", "admin/app/root.tsx"}
	private := []string{"sdks/rust/target/debug/cache", "sdks/rust/xtask/target/cache", "sdks/dart/.dart_tool/cache", "sdks/dart/offline/.dart_tool/cache", "sdks/dart/example/build/cache", "sdks/dart/offline_sqlite/build/cache", "testbed/bench/out/.peer-tls.example/operator.key", "testbed/bench/out/example/tokens.json"}
	for _, enforce := range []bool{false, true} {
		name := "unfiltered_control"
		if enforce {
			name = "repository_boundary"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "input"), filepath.Join(dir, "export")
			write := func(path string, data []byte) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range append(append([]string(nil), required...), private...) {
				write(filepath.Join(input, path), []byte("synthetic marker\n"))
			}
			if enforce {
				write(filepath.Join(input, ".dockerignore"), ignore)
			}
			dockerfile := filepath.Join(dir, "Dockerfile")
			write(dockerfile, []byte("FROM scratch\nCOPY . /\n"))
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "docker", "build", "--progress=plain", "--output", "type=local,dest="+output, "--file", dockerfile, input)
			if log, err := command.CombinedOutput(); err != nil {
				t.Fatalf("actual context export: %v\n%s", err, log)
			}
			for _, path := range required {
				if _, err := os.Stat(filepath.Join(output, path)); err != nil {
					t.Errorf("required image source excluded: %s: %v", path, err)
				}
			}
			for _, path := range private {
				_, err := os.Stat(filepath.Join(output, path))
				if enforce && !os.IsNotExist(err) || !enforce && err != nil {
					t.Errorf("private marker %s: boundary=%t: %v", path, enforce, err)
				}
			}
		})
	}
}
