package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cliservice "github.com/anaregdesign/lantern/cli/service"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	serverservice "github.com/anaregdesign/lantern/server/service"
)

// TestCLI_FamilyVerbs_RealWire exercises the #975 family verbs (bfs / pagerank /
// community) end-to-end through the CLI's REPL dispatcher (RunArgs) against a
// real in-process server. The dispatcher parses the family grammar, builds the
// typed SDK family option (BfsOption / PagerankOption / CommunityOption), and
// forwards it over the real Connect/h2c wire — guarding the CLI grammar → SDK →
// wire path that the per-parser unit tests and the SDK-level option tests only
// cover in isolation.
func TestCLI_FamilyVerbs_RealWire(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, k := range []string{"a", "b", "c", "cm:a1", "cm:a2", "cm:a3", "cm:b1", "cm:b2", "cm:b3"} {
		if _, err := l.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if _, err := l.PutEdge(ctx, "a", "b", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge a->b: %v", err)
	}
	if _, err := l.PutEdge(ctx, "b", "c", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge b->c: %v", err)
	}
	for _, pair := range [][2]string{
		{"cm:a1", "cm:a2"}, {"cm:a2", "cm:a1"}, {"cm:a2", "cm:a3"}, {"cm:a3", "cm:a2"}, {"cm:a1", "cm:a3"}, {"cm:a3", "cm:a1"},
		{"cm:b1", "cm:b2"}, {"cm:b2", "cm:b1"}, {"cm:b2", "cm:b3"}, {"cm:b3", "cm:b2"}, {"cm:b1", "cm:b3"}, {"cm:b3", "cm:b1"},
	} {
		if _, err := l.PutEdge(ctx, pair[0], pair[1], 5, time.Minute); err != nil {
			t.Fatalf("PutEdge %s->%s: %v", pair[0], pair[1], err)
		}
	}
	if _, err := l.PutEdge(ctx, "cm:a1", "cm:b1", 0.1, time.Minute); err != nil {
		t.Fatalf("PutEdge cm:a1->cm:b1: %v", err)
	}
	if _, err := l.PutEdge(ctx, "cm:b1", "cm:a1", 0.1, time.Minute); err != nil {
		t.Fatalf("PutEdge cm:b1->cm:a1: %v", err)
	}

	svc := cliservice.NewCLIService(l)

	// Happy path: each family verb dispatches over the real wire without error.
	// The bare `<verb> <seed>` form exercises each family's defaults (#975: bfs
	// step=5/fan_out=3, pagerank top_n=10, community max_size=0); the longer
	// forms exercise the positional + kwarg knobs specific to each family
	// (bfs step/fan_out + reduction/objective, pagerank top_n + α/ε, community
	// max_size + reduction).
	for _, args := range [][]string{
		{"bfs", "a"},
		{"bfs", "a", "3", "10", "reduction=mst", "objective=min"},
		{"pagerank", "a"},
		{"pagerank", "a", "5", "restart_prob=0.25", "epsilon=0.001"},
		{"community", "a"},
		{"community", "a", "5", "reduction=spt"},
	} {
		if err := svc.RunArgs(ctx, args); err != nil {
			t.Errorf("RunArgs(%v) = %v, want nil", args, err)
		}
	}

	// Family-specific result shapes are an external contract, not just a
	// no-error condition: BFS keeps traversal edges, PPR returns a seed-star,
	// and community plus reduction returns its induced cluster as a tree.
	bfs := runCLIForGraph(t, svc, ctx, []string{"bfs", "a", "3", "10"})
	if _, ok := bfs.Edges["b"]["c"]; !ok {
		t.Fatalf("BFS must retain traversal edge b->c, got %+v", bfs.Edges)
	}
	ppr := runCLIForGraph(t, svc, ctx, []string{"pagerank", "a", "2", "restart_prob=0.25", "epsilon=0.001"})
	if len(ppr.Edges) != 1 || ppr.Edges["a"] == nil {
		t.Fatalf("PPR must return a seed-star, got %+v", ppr.Edges)
	}
	if _, ok := ppr.Edges["b"]; ok {
		t.Fatalf("PPR must not retain BFS edge b->c, got %+v", ppr.Edges)
	}
	community := runCLIForGraph(t, svc, ctx, []string{"community", "cm:a1", "3", "restart_prob=0.25", "epsilon=0.001", "reduction=mst"})
	for _, key := range []string{"cm:a1", "cm:a2", "cm:a3"} {
		if _, ok := community.Vertices[key]; !ok {
			t.Fatalf("community result missing %q: %+v", key, community.Vertices)
		}
	}
	if _, ok := community.Vertices["cm:b1"]; ok {
		t.Fatalf("community crossed weak bridge: %+v", community.Vertices)
	}
	if got := graphEdgeCount(community); got != 2 {
		t.Fatalf("reduced 3-member community edge count = %d, want 2: %+v", got, community.Edges)
	}

	// Grammar failure contract: pagerank has no reduction axis (#975), so the
	// parser rejects `reduction=` before any RPC — RunArgs surfaces the error
	// rather than silently dropping the unknown knob.
	if err := svc.RunArgs(ctx, []string{"pagerank", "a", "reduction=mst"}); err == nil {
		t.Error("RunArgs(pagerank a reduction=mst) = nil, want a parse error")
	}

	// Numeric-domain failure contract (#980): the CLI parser rejects out-of-range
	// family knobs before constructing SDK options or reaching the server.
	for _, tc := range []struct {
		name string
		args []string
		want error
	}{
		{name: "BfsZeroStep", args: []string{"bfs", "a", "0"}, want: cliservice.ErrBFS},
		{name: "BfsZeroFanOut", args: []string{"bfs", "a", "1", "0"}, want: cliservice.ErrBFS},
		{name: "PagerankNegativeTopN", args: []string{"pagerank", "a", "-1"}, want: cliservice.ErrPagerank},
		{name: "PagerankRestartProbBoundary", args: []string{"pagerank", "a", "restart_prob=1"}, want: cliservice.ErrPagerank},
		{name: "PagerankZeroEpsilon", args: []string{"pagerank", "a", "epsilon=0"}, want: cliservice.ErrPagerank},
		{name: "CommunityNegativeMaxSize", args: []string{"community", "a", "-1"}, want: cliservice.ErrCommunity},
		{name: "CommunityRestartProbBoundary", args: []string{"community", "a", "restart_prob=0"}, want: cliservice.ErrCommunity},
		{name: "CommunityZeroEpsilon", args: []string{"community", "a", "epsilon=0"}, want: cliservice.ErrCommunity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.RunArgs(ctx, tc.args); !errors.Is(err, tc.want) {
				t.Errorf("RunArgs(%v) = %v, want an error matching %v", tc.args, err, tc.want)
			}
		})
	}
}

// TestCLI_FamilyVerbs_RPCFailuresUseStderrAndExit2 exercises the actual CLI
// executable against the real Connect/h2c handler. It protects the published
// script contract from drifting separately between the flag and positional
// family paths (#986): a server-side validation failure keeps its Connect
// detail, prints only on stderr, and exits 2.
func TestCLI_FamilyVerbs_RPCFailuresUseStderrAndExit2(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := serverservice.NewLanternService(cache)
	validator := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, validator.ConnectInterceptor())
	binary := buildLanternCLI(t)
	endpoint := strings.TrimPrefix(srv.url, "http://")

	for _, tc := range []struct {
		name       string
		args       []string
		wantDetail string
	}{
		{
			name:       "BFS/Flag",
			args:       []string{"bfs", "any", "--step", "99", "--fan-out", "1"},
			wantDetail: "bfs.step 99 exceeds max",
		},
		{
			name:       "BFS/Positional",
			args:       []string{"bfs", "any", "99", "1"},
			wantDetail: "bfs.step 99 exceeds max",
		},
		{
			name:       "PageRank/Flag",
			args:       []string{"pagerank", "any", "--top-n", "999"},
			wantDetail: "ppr.top_n 999 exceeds max",
		},
		{
			name:       "PageRank/Positional",
			args:       []string{"pagerank", "any", "999"},
			wantDetail: "ppr.top_n 999 exceeds max",
		},
		{
			name:       "Community/Flag",
			args:       []string{"community", "any", "--max-size", "999"},
			wantDetail: "community.max_size 999 exceeds max",
		},
		{
			name:       "Community/Positional",
			args:       []string{"community", "any", "999"},
			wantDetail: "community.max_size 999 exceeds max",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runLanternCLI(t, binary, endpoint, tc.args...)
			if code != 2 {
				t.Errorf("exit code = %d, want 2; stderr = %q", code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty so machine-readable output is not contaminated", stdout)
			}
			if !strings.Contains(stderr, "invalid_argument") {
				t.Errorf("stderr = %q, want the Connect InvalidArgument code", stderr)
			}
			if !strings.Contains(stderr, tc.wantDetail) {
				t.Errorf("stderr = %q, want preserved server detail %q", stderr, tc.wantDetail)
			}
			if strings.Contains(stderr, "connection error") {
				t.Errorf("stderr = %q, must not replace the RPC cause with connection error", stderr)
			}
		})
	}

	// The same executable seam distinguishes local family validation from an
	// RPC rejection. These arguments fail before Illuminate is invoked, so the
	// documented local-error exit code remains 1 and stdout stays clean.
	for _, tc := range []struct {
		name     string
		args     []string
		wantText string
	}{
		{
			name:     "BFS/PositionalParse",
			args:     []string{"bfs", "any", "0", "1"},
			wantText: "step 0 (want a positive integer)",
		},
		{
			name:     "PageRank/FlagValidation",
			args:     []string{"pagerank", "any", "--restart-prob", "1"},
			wantText: "--restart-prob must be a float in (0,1)",
		},
		{
			name:     "Community/PositionalParse",
			args:     []string{"community", "any", "restart_prob=1"},
			wantText: "restart_prob=1 (want a float in (0,1))",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runLanternCLI(t, binary, endpoint, tc.args...)
			if code != 1 {
				t.Errorf("exit code = %d, want 1; stderr = %q", code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty for local errors too", stdout)
			}
			if !strings.Contains(stderr, tc.wantText) {
				t.Errorf("stderr = %q, want local error %q", stderr, tc.wantText)
			}
		})
	}
}

func buildLanternCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "lantern-cli")
	build := exec.Command("go", "build", "-o", binary, "./cli")
	build.Dir = filepath.Clean(filepath.Join("..", ".."))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cli: %v\n%s", err, output)
	}
	return binary
}

func runLanternCLI(t *testing.T, binary, endpoint string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	command := exec.Command(binary, append([]string{"--address", endpoint}, args...)...)
	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer
	err := command.Run()
	if err == nil {
		return stdoutBuffer.String(), stderrBuffer.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run lantern-cli %v: %v", args, err)
	}
	return stdoutBuffer.String(), stderrBuffer.String(), exitErr.ExitCode()
}

// runCLIForGraph captures the CLI's JSON output while retaining the real
// Connect/h2c request underneath. Integration tests in this package are not
// parallel, so temporarily redirecting process stdout is safe here.
type cliGraph struct {
	Vertices map[string]json.RawMessage    `json:"vertices"`
	Edges    map[string]map[string]float32 `json:"edges"`
}

func runCLIForGraph(t *testing.T, svc *cliservice.CLIService, ctx context.Context, args []string) *cliGraph {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = write
	err = svc.RunArgs(ctx, args)
	_ = write.Close()
	os.Stdout = previous
	if err != nil {
		_ = read.Close()
		t.Fatalf("RunArgs(%v): %v", args, err)
	}
	output, readErr := io.ReadAll(read)
	_ = read.Close()
	if readErr != nil {
		t.Fatalf("read CLI output: %v", readErr)
	}
	var graph cliGraph
	if err := json.Unmarshal(output, &graph); err != nil {
		t.Fatalf("decode CLI output %q: %v", output, err)
	}
	return &graph
}

func graphEdgeCount(graph *cliGraph) int {
	count := 0
	for _, heads := range graph.Edges {
		count += len(heads)
	}
	return count
}
