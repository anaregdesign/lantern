package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/cli/parser"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
)

type contributionCLIHandler struct {
	graphv1connect.UnimplementedLanternServiceHandler
	contributions map[string]bool
	addCalls      int
	deleteCalls   int
	wholeCalls    int
}

func (h *contributionCLIHandler) AddEdges(_ context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	h.addCalls++
	if len(req.Msg.GetEdges()) != 1 || len(req.Msg.GetContribIds()) != 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad Add request"))
	}
	e := req.Msg.GetEdges()[0]
	id := req.Msg.GetContribIds()[0]
	h.contributions[e.GetTail()+"\x00"+e.GetHead()+"\x00"+hex.EncodeToString(id)] = true
	return connect.NewResponse(&pb.AddEdgesResponse{Written: 1, EffectiveWeights: []float32{e.GetWeight()}}), nil
}

func (h *contributionCLIHandler) DeleteEdgeContributions(_ context.Context, req *connect.Request[pb.DeleteEdgeContributionsRequest]) (*connect.Response[pb.DeleteEdgeContributionsResponse], error) {
	h.deleteCalls++
	existed := make([]bool, len(req.Msg.GetContributions()))
	var deleted int32
	for i, ref := range req.Msg.GetContributions() {
		key := ref.GetTail() + "\x00" + ref.GetHead() + "\x00" + hex.EncodeToString(ref.GetContribId())
		existed[i] = h.contributions[key]
		if existed[i] {
			deleted++
			delete(h.contributions, key)
		}
	}
	return connect.NewResponse(&pb.DeleteEdgeContributionsResponse{Deleted: deleted, Existed: existed}), nil
}

func (h *contributionCLIHandler) DeleteEdges(_ context.Context, req *connect.Request[pb.DeleteEdgesRequest]) (*connect.Response[pb.DeleteEdgesResponse], error) {
	h.wholeCalls++
	return connect.NewResponse(&pb.DeleteEdgesResponse{Existed: make([]bool, len(req.Msg.GetEdges()))}), nil
}

func (h *contributionCLIHandler) DeleteEdge(_ context.Context, _ *connect.Request[pb.DeleteEdgeRequest]) (*connect.Response[pb.DeleteEdgeResponse], error) {
	h.wholeCalls++
	return connect.NewResponse(&pb.DeleteEdgeResponse{}), nil
}

func TestRunArgs_ContributionDeleteAndExplicitAdd(t *testing.T) {
	fake := &contributionCLIHandler{contributions: make(map[string]bool)}
	path, handler := graphv1connect.NewLanternServiceHandler(fake)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	conn, err := client.NewLantern(srv.URL, client.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var output bytes.Buffer
	svc := NewCLIService(conn, WithOutput(&output))
	id := strings.Repeat("ab", client.ContribIDSize)
	ctx := context.Background()
	if err := svc.RunArgs(ctx, []string{"add", "edge", "Tail", "Head", "2", "id=" + id}); err != nil {
		t.Fatalf("explicit Add: %v", err)
	}
	if fake.addCalls != 1 {
		t.Fatalf("Add routed to %d RPCs, want one", fake.addCalls)
	}
	if err := svc.RunArgs(ctx, []string{
		"delete", "contribution", "Tail", "Head", id,
		"Tail", "Head", id, "missing", "Head", id,
	}); err != nil {
		t.Fatalf("plural contribution Delete: %v", err)
	}
	if got := output.String(); got != "{\"deleted\":1,\"existed\":[true,false,false]}\n" {
		t.Fatalf("plural JSON output = %q", got)
	}
	output.Reset()
	if err := svc.Run(ctx, "delete contribution Tail Head "+id); err != nil {
		t.Fatalf("REPL single contribution Delete: %v", err)
	}
	if got := output.String(); got != "{\"deleted\":0,\"existed\":[false]}\n" {
		t.Fatalf("single JSON output = %q", got)
	}
	if fake.deleteCalls != 2 || fake.wholeCalls != 0 {
		t.Fatalf("selective Delete RPC calls=%d, whole-edge calls=%d", fake.deleteCalls, fake.wholeCalls)
	}
	if err := svc.RunArgs(ctx, []string{"delete", "edge", "Tail", "Head"}); err != nil {
		t.Fatalf("whole-edge Delete grammar changed: %v", err)
	}
	if fake.wholeCalls != 1 {
		t.Fatalf("whole-edge Delete not routed to its original RPC: %d", fake.wholeCalls)
	}
}

func TestRunArgs_ContributionInvalidBeforeTransport(t *testing.T) {
	svc := NewCLIService(nil)
	id := strings.Repeat("ab", client.ContribIDSize)
	for _, args := range [][]string{
		{"delete", "contribution"},
		{"delete", "contribution", "t", "h", strings.Repeat("00", client.ContribIDSize)},
		{"delete", "contribution", "t", "h", strings.Repeat("ab", 49)},
		{"delete", "contribution", "t", "h", id, "other"},
	} {
		if err := svc.RunArgs(context.Background(), args); !errors.Is(err, ErrDeleteContribution) {
			t.Errorf("RunArgs(%v) = %v, want ErrDeleteContribution", args, err)
		}
	}
	if err := svc.RunArgs(context.Background(), []string{"add", "edge", "t", "h", "2", "id=" + strings.Repeat("00", 24)}); !errors.Is(err, ErrAddEdge) {
		t.Errorf("all-zero Add ID returned %v, want ErrAddEdge", err)
	}
}

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

func TestRequireAppliedPut(t *testing.T) {
	if err := requireAppliedPut("vertex", client.PutOutcomeAppliedAndLive); err != nil {
		t.Fatalf("applied Put: %v", err)
	}
	if err := requireAppliedPut("vertex", client.PutOutcomeExpired); err == nil {
		t.Fatal("expired Put was reported as successful")
	}
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
	t.Run("ScopedHelpReturnsNil", func(t *testing.T) {
		if err := svc.RunArgs(ctx, []string{"help", "bfs"}); err != nil {
			t.Errorf("RunArgs([help bfs]) = %v, want nil", err)
		}
	})
	t.Run("UnknownHelpTopicReturnsErrHelp", func(t *testing.T) {
		if err := svc.RunArgs(ctx, []string{"help", "unknown"}); err != ErrHelp {
			t.Errorf("RunArgs([help unknown]) = %v, want ErrHelp", err)
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
			if err := svc.RunArgs(ctx, tc.args); !errors.Is(err, tc.want) {
				t.Errorf("RunArgs(%v) = %v, want an error matching %v", tc.args, err, tc.want)
			}
		})
	}
}
