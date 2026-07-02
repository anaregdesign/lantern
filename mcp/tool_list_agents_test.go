package mcp

import (
	"context"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestListAgents(t *testing.T) {
	t.Run("lists and decodes live presences sorted by id", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.scanVerticesFn = func(_ context.Context, prefix string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
			if prefix != agentKeyPrefix {
				t.Fatalf("scan prefix = %q, want %q", prefix, agentKeyPrefix)
			}
			return []*client.Vertex{
				presenceVertex(t, "agents.zeta", presenceRecord{Task: "docs"}),
				presenceVertex(t, "agents.alpha", presenceRecord{Task: "build", Detail: "branch main"}),
			}, nil, nil
		}
		res := h.call(t, "list_agents", map[string]any{})
		var out listAgentsOutput
		structuredAs(t, res, &out)
		if out.Count != 2 || out.Agents[0].AgentID != "alpha" || out.Agents[1].AgentID != "zeta" {
			t.Fatalf("agents: %+v", out)
		}
		if out.Agents[0].Task != "build" || out.Agents[0].Detail != "branch main" {
			t.Fatalf("decode: %+v", out.Agents[0])
		}
		if out.Agents[0].ExpiresAt == "" {
			t.Fatal("expiry missing from listing")
		}
	})

	t.Run("empty fleet is a friendly empty result", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
			return nil, nil, nil
		}
		res := h.call(t, "list_agents", map[string]any{})
		var out listAgentsOutput
		structuredAs(t, res, &out)
		if out.Count != 0 || out.Agents == nil {
			t.Fatalf("empty fleet: %+v", out)
		}
	})
}
