package service

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/cli/parser"
)

// TestFormatWriteEcho pins the REPL success-echo formatting (#653): a
// mutating write must surface its applied TTL and absolute expiry so a
// decaying write is never silent. A fixed clock keeps the expiry
// deterministic.
func TestFormatWriteEcho(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 34, 56, 0, time.UTC)

	t.Run("PositiveTTLEchoesDurationAndAbsoluteExpiry", func(t *testing.T) {
		got := formatWriteEcho(`put vertex "a"`, 1*time.Second, now)
		want := `put vertex "a" (ttl 1s, expires 2026-06-16T12:34:57Z)`
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})

	t.Run("ZeroTTLIsPermanentSentinel", func(t *testing.T) {
		got := formatWriteEcho(`put vertex "permkey"`, 0, now)
		want := `put vertex "permkey" (no ttl)`
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})

	t.Run("NegativeTTLIsAlsoPermanent", func(t *testing.T) {
		got := formatWriteEcho("add edge", -5*time.Second, now)
		want := "add edge (no ttl)"
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})

	t.Run("MultiUnitTTLRendersViaDurationString", func(t *testing.T) {
		got := formatWriteEcho(`put edge "a" -> "b" (weight 1.5)`, 90*time.Second, now)
		want := `put edge "a" -> "b" (weight 1.5) (ttl 1m30s, expires 2026-06-16T12:36:26Z)`
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})
}

// TestRunArgs pins the one-liner dispatch entry point (#672). RunArgs feeds
// pre-split argv through the same runSource dispatcher Run uses for a raw
// REPL line. The help and invalid-verb branches never touch the client, so
// a nil-client service exercises the shared dispatch without a live server.
func TestRunArgs(t *testing.T) {
	svc := NewCLIService(nil)
	ctx := context.Background()

	t.Run("HelpVerbReturnsNil", func(t *testing.T) {
		if err := svc.RunArgs(ctx, []string{"help"}); err != nil {
			t.Errorf("RunArgs([help]) = %v, want nil", err)
		}
	})
	t.Run("UnknownVerbReturnsErrInvalidVerb", func(t *testing.T) {
		if err := svc.RunArgs(ctx, []string{"bogus"}); err != ErrInvalidVerb {
			t.Errorf("RunArgs([bogus]) = %v, want ErrInvalidVerb", err)
		}
	})
	t.Run("EmptyArgsReturnsErrInvalidVerb", func(t *testing.T) {
		if err := svc.RunArgs(ctx, nil); err != ErrInvalidVerb {
			t.Errorf("RunArgs(nil) = %v, want ErrInvalidVerb", err)
		}
	})

	// Forward parity guard (#672/#674): RunArgs backs every verb-first
	// one-liner, so the shared dispatcher must RECOGNISE every verb the REPL
	// grammar accepts (parser.Verbs). Feeding just the bare verb fails at
	// objective/parameter parsing and returns a verb-specific sentinel (or nil
	// for help), never ErrInvalidVerb and never dereferencing the nil client.
	// Carveout: exit is consumed by the REPL read loop, not this dispatcher.
	t.Run("EveryREPLVerbIsRecognisedByTheOneLinerDispatcher", func(t *testing.T) {
		for _, verb := range parser.Verbs {
			err := svc.RunArgs(ctx, []string{verb})
			if verb == "exit" {
				if err != ErrInvalidVerb {
					t.Errorf("RunArgs([%s]) = %v, want ErrInvalidVerb (exit is REPL-loop only)", verb, err)
				}
				continue
			}
			if err == ErrInvalidVerb {
				t.Errorf("one-liner dispatcher rejected REPL verb %q as invalid", verb)
			}
		}
	})
}

// TestRunArgs_AddDecayingEdge exercises the decay verb's dispatch branches
// that do not touch the client (#952): a malformed decay line fails at parse
// time and returns ErrAddDecayingEdge, and a bad add-objective returns
// ErrInvalidObjective — both before any RPC, so a nil-client service proves
// the parse guards fire ahead of the client dereference. The happy-path
// round-trip lives in tests/integration.
func TestRunArgs_AddDecayingEdge(t *testing.T) {
	svc := NewCLIService(nil)
	ctx := context.Background()

	t.Run("MalformedDecayLineReturnsErrAddDecayingEdge", func(t *testing.T) {
		// Missing steps + interval — parse fails before c.client is used.
		err := svc.RunArgs(ctx, []string{"add", "decaying-edge", "a", "b", "16", "0.5"})
		if err != ErrAddDecayingEdge {
			t.Errorf("RunArgs(add decaying-edge a b 16 0.5) = %v, want ErrAddDecayingEdge", err)
		}
	})

	t.Run("UnknownAddObjectiveReturnsErrInvalidObjective", func(t *testing.T) {
		if err := svc.RunArgs(ctx, []string{"add", "bogus", "a", "b"}); err != ErrInvalidObjective {
			t.Errorf("RunArgs(add bogus ...) = %v, want ErrInvalidObjective", err)
		}
	})
}

func TestRunArgs_FamilyParseErrorsReturnSpecificSentinels(t *testing.T) {
	svc := NewCLIService(nil)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		args []string
		want error
	}{
		{name: "BfsMissingSeed", args: []string{"bfs"}, want: ErrBFS},
		{name: "BfsInvalidStep", args: []string{"bfs", "alice", "0"}, want: ErrBFS},
		{name: "PagerankMissingSeed", args: []string{"pagerank"}, want: ErrPagerank},
		{name: "PagerankInvalidRestartProb", args: []string{"pagerank", "alice", "restart_prob=1"}, want: ErrPagerank},
		{name: "CommunityMissingSeed", args: []string{"community"}, want: ErrCommunity},
		{name: "CommunityInvalidEpsilon", args: []string{"community", "alice", "epsilon=0"}, want: ErrCommunity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.RunArgs(ctx, tc.args); err != tc.want {
				t.Errorf("RunArgs(%v) = %v, want %v", tc.args, err, tc.want)
			}
		})
	}
}
