package mcp

import (
	"context"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestWhatsHappening(t *testing.T) {
	t.Run("classifies the neighborhood into agents/resources/notes/claims", func(t *testing.T) {
		h := newContextHarness(t)
		// Forward walk from the resource finds nothing (resources have no
		// out-edges); the reverse scan finds an agent and a note pointing
		// at it; the claim lookup finds a live lease on the seed.
		h.fake.illuminateFn = func(_ context.Context, seed string, _ ...client.IlluminateOption) (*client.Graph, error) {
			return &client.Graph{
				Vertices: map[string]*client.Vertex{seed: {Key: seed}},
				Edges:    map[string]map[string]float32{},
			}, nil
		}
		h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
			return []*client.Edge{
				{Tail: "agents.builder-1", Head: "repo.core", Weight: 3},
				{Tail: "notes.n1", Head: "repo.core", Weight: 1},
				{Tail: "agents.other", Head: "repo.core.sibling", Weight: 9}, // prefix sibling — must be filtered
			}, nil, nil
		}
		h.fake.getVerticesFn = func(_ context.Context, keys []string) ([]*client.Vertex, []string, error) {
			var found []*client.Vertex
			for _, k := range keys {
				switch k {
				case "agents.builder-1":
					found = append(found, presenceVertex(t, k, presenceRecord{Task: "building"}))
				case "notes.n1":
					payload, _ := encodeRecord(noteRecord{Text: "ci flaky", Author: "builder-1", Severity: "warn"})
					found = append(found, &pb.Vertex{Key: k, Value: &pb.Vertex_String_{String_: payload}})
				case "claims.repo.core":
					found = append(found, claimVertex(t, k, claimRecord{Holder: "builder-1"}))
				}
			}
			return found, nil, nil
		}

		res := h.call(t, "whats_happening", map[string]any{"key": "repo.core"})
		var out whatsHappeningOutput
		structuredAs(t, res, &out)
		if out.Empty {
			t.Fatalf("context not empty: %+v", out)
		}
		if len(out.Agents) != 1 || out.Agents[0].AgentID != "builder-1" || out.Agents[0].Task != "building" {
			t.Fatalf("agents: %+v", out.Agents)
		}
		if len(out.Notes) != 1 || out.Notes[0].Text != "ci flaky" || out.Notes[0].Severity != "warn" {
			t.Fatalf("notes: %+v", out.Notes)
		}
		if len(out.Claims) != 1 || out.Claims[0].Resource != "repo.core" || out.Claims[0].Holder != "builder-1" {
			t.Fatalf("claims: %+v", out.Claims)
		}
		for _, a := range out.Agents {
			if a.AgentID == "other" {
				t.Fatal("prefix-sibling head leaked through the exact-match filter")
			}
		}
	})

	t.Run("agent seed walks forward to its working set", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.illuminateFn = func(_ context.Context, seed string, _ ...client.IlluminateOption) (*client.Graph, error) {
			return &client.Graph{
				Vertices: map[string]*client.Vertex{
					seed:            {Key: seed},
					"repo.core":     {Key: "repo.core"},
					"ticket.LANT-1": {Key: "ticket.LANT-1"},
				},
				Edges: map[string]map[string]float32{
					seed: {"repo.core": 5, "ticket.LANT-1": 2},
				},
			}, nil
		}
		res := h.call(t, "whats_happening", map[string]any{"key": "agents.builder-1"})
		var out whatsHappeningOutput
		structuredAs(t, res, &out)
		if len(out.Resources) != 2 {
			t.Fatalf("resources: %+v", out.Resources)
		}
		// Ordered by activity weight descending.
		if out.Resources[0].Key != "repo.core" || out.Resources[0].Weight != 5 {
			t.Fatalf("resource ordering by weight broken: %+v", out.Resources)
		}
	})

	t.Run("never-touched key returns well-formed empty context", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.illuminateFn = func(_ context.Context, seed string, _ ...client.IlluminateOption) (*client.Graph, error) {
			return &client.Graph{Vertices: map[string]*client.Vertex{}, Edges: map[string]map[string]float32{}}, nil
		}
		res := h.call(t, "whats_happening", map[string]any{"key": "repo.ghost"})
		var out whatsHappeningOutput
		structuredAs(t, res, &out)
		if !out.Empty {
			t.Fatalf("expected empty context: %+v", out)
		}
		if out.Agents == nil || out.Resources == nil || out.Notes == nil || out.Claims == nil {
			t.Fatal("empty context must still carry well-formed (non-null) arrays")
		}
	})

	t.Run("empty key rejected", func(t *testing.T) {
		h := newContextHarness(t)
		h.callExpectError(t, "whats_happening", map[string]any{"key": ""})
	})
}
