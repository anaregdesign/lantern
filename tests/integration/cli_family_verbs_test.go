package integration_test

import (
	"context"
	"testing"
	"time"

	cliservice "github.com/anaregdesign/lantern/cli/service"
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

	for _, k := range []string{"a", "b", "c"} {
		if err := l.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if err := l.PutEdge(ctx, "a", "b", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge a->b: %v", err)
	}
	if err := l.PutEdge(ctx, "b", "c", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge b->c: %v", err)
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
			if err := svc.RunArgs(ctx, tc.args); err != tc.want {
				t.Errorf("RunArgs(%v) = %v, want %v", tc.args, err, tc.want)
			}
		})
	}
}
