// Package topology defines deterministic graph fixtures used by the bench
// harness. Keeping the fixture as Go data, rather than an incidental stream of
// ghz AddEdge calls, lets the harness assert the work each traversal family
// actually performs before it measures it.
package topology

import (
	"context"
	"fmt"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

const (
	// BroadIlluminateFanOut and BroadIlluminateDepth mirror the largest BFS
	// request in broad_illuminate.yaml. Each non-leaf walk vertex has exactly
	// this fan-out; the three layers are shared so the fixture is dense enough
	// to exercise branching without materialising 64^3 distinct paths.
	BroadIlluminateFanOut = 64
	BroadIlluminateDepth  = 3

	// BroadIlluminateCommunitySize is the planted clique size and the max_size
	// requested by the community producer.
	BroadIlluminateCommunitySize = 32

	WalkSeed             = "bench:walk:root"
	CommunitySeed        = "bench:community:alpha:00"
	communityAlphaPrefix = "bench:community:alpha:"
	communityBetaPrefix  = "bench:community:beta:"
)

// BroadIlluminateFixture is the self-contained topology for the
// broad_illuminate scenario. The fixture comprises:
//
//   - a 64-way, three-hop directed layered walk rooted at WalkSeed; and
//   - two dense 32-member communities joined by a weak reciprocal bridge.
//
// The shared walk layers intentionally make the vertex count O(fan_out), while
// every measured non-leaf still has fan_out outgoing edges. This makes the
// breadth/depth request real without turning the release benchmark into a
// million-edge fixture.
type BroadIlluminateFixture struct {
	Vertices []client.VertexInput
	Edges    []client.EdgeInput
}

// NewBroadIlluminateFixture constructs the deterministic #994 fixture. The
// caller supplies expiration so preflight can choose a lifetime that covers
// the whole benchmark run.
func NewBroadIlluminateFixture(expiration time.Time) BroadIlluminateFixture {
	fixture := BroadIlluminateFixture{
		Vertices: make([]client.VertexInput, 0, 1+BroadIlluminateDepth*BroadIlluminateFanOut+2*BroadIlluminateCommunitySize),
		Edges:    make([]client.EdgeInput, 0, BroadIlluminateFanOut+2*BroadIlluminateFanOut*BroadIlluminateFanOut+2*BroadIlluminateCommunitySize*(BroadIlluminateCommunitySize-1)+2),
	}
	addVertex := func(key string) {
		fixture.Vertices = append(fixture.Vertices, client.VertexInput{Key: key, Value: key, Expiration: expiration})
	}
	addEdge := func(tail, head string, weight float32) {
		fixture.Edges = append(fixture.Edges, client.EdgeInput{Tail: tail, Head: head, Weight: weight, Expiration: expiration})
	}

	addVertex(WalkSeed)
	for layer := 1; layer <= BroadIlluminateDepth; layer++ {
		for i := 0; i < BroadIlluminateFanOut; i++ {
			addVertex(walkKey(layer, i))
		}
	}
	for i := 0; i < BroadIlluminateFanOut; i++ {
		addEdge(WalkSeed, walkKey(1, i), 1)
	}
	for layer := 1; layer < BroadIlluminateDepth; layer++ {
		for tail := 0; tail < BroadIlluminateFanOut; tail++ {
			for head := 0; head < BroadIlluminateFanOut; head++ {
				addEdge(walkKey(layer, tail), walkKey(layer+1, head), 1)
			}
		}
	}

	for _, prefix := range []string{communityAlphaPrefix, communityBetaPrefix} {
		for i := 0; i < BroadIlluminateCommunitySize; i++ {
			addVertex(communityKey(prefix, i))
		}
		for tail := 0; tail < BroadIlluminateCommunitySize; tail++ {
			for head := 0; head < BroadIlluminateCommunitySize; head++ {
				if tail != head {
					addEdge(communityKey(prefix, tail), communityKey(prefix, head), 1)
				}
			}
		}
	}
	addEdge(communityKey(communityAlphaPrefix, 0), communityKey(communityBetaPrefix, 0), 0.01)
	addEdge(communityKey(communityBetaPrefix, 0), communityKey(communityAlphaPrefix, 0), 0.01)
	return fixture
}

// SeedBroadIlluminate writes the fixture idempotently through the plural SDK
// surface, then validates the live RPC result shapes. PutEdges is intentional:
// rerunning preflight replaces the known fixture weights instead of making
// additive contributions accumulate between local benchmark attempts.
func SeedBroadIlluminate(ctx context.Context, lantern *client.Lantern, expiration time.Time) (BroadIlluminateVerification, error) {
	fixture := NewBroadIlluminateFixture(expiration)
	vertexResults, err := lantern.PutVertices(ctx, fixture.Vertices)
	if err != nil {
		return BroadIlluminateVerification{}, fmt.Errorf("seed vertices: %w", err)
	}
	for i, result := range vertexResults {
		if result.Outcome != client.PutOutcomeAppliedAndLive {
			return BroadIlluminateVerification{}, fmt.Errorf("seed vertex %d (%q): %s", i, result.Key, result.Outcome)
		}
	}
	edgeResults, err := lantern.PutEdges(ctx, fixture.Edges)
	if err != nil {
		return BroadIlluminateVerification{}, fmt.Errorf("seed edges: %w", err)
	}
	for i, result := range edgeResults {
		if result.Outcome != client.PutOutcomeAppliedAndLive {
			return BroadIlluminateVerification{}, fmt.Errorf("seed edge %d (%q -> %q): %s", i, result.Tail, result.Head, result.Outcome)
		}
	}
	return VerifyBroadIlluminate(ctx, lantern)
}

// BroadIlluminateVerification is written by the preflight command so a bench
// artifact records the observed topology, not merely the YAML intent.
type BroadIlluminateVerification struct {
	WalkVertices    int `json:"walk_vertices"`
	WalkEdges       int `json:"walk_edges"`
	WalkRootDegree  int `json:"walk_root_degree"`
	WalkLayer1      int `json:"walk_layer1_vertices"`
	WalkLayer2      int `json:"walk_layer2_vertices"`
	WalkLayer3      int `json:"walk_layer3_vertices"`
	PPRVertices     int `json:"ppr_vertices"`
	CommunityMember int `json:"community_members"`
	CommunityEdges  int `json:"community_edges"`
}

// VerifyBroadIlluminate exercises each family against the live fixture and
// rejects a bench run when branching, depth, PPR touch count, or the planted
// community/reduction shape has regressed. This is deliberately stricter than
// a schema check: a wire-valid YAML request against an accidentally linear
// graph is not a meaningful traversal benchmark.
func VerifyBroadIlluminate(ctx context.Context, lantern *client.Lantern) (BroadIlluminateVerification, error) {
	var report BroadIlluminateVerification
	walk, err := lantern.Illuminate(ctx, WalkSeed, client.WithBFS(client.BFSOpts{
		Step: BroadIlluminateDepth, FanOut: BroadIlluminateFanOut,
	}))
	if err != nil {
		return report, fmt.Errorf("verify BFS: %w", err)
	}
	report.WalkVertices = len(walk.Vertices)
	report.WalkEdges = edgeCount(walk)
	report.WalkRootDegree = len(walk.Edges[WalkSeed])
	for key := range walk.Vertices {
		switch {
		case hasWalkLayer(key, 1):
			report.WalkLayer1++
		case hasWalkLayer(key, 2):
			report.WalkLayer2++
		case hasWalkLayer(key, 3):
			report.WalkLayer3++
		}
	}
	if report.WalkRootDegree != BroadIlluminateFanOut ||
		report.WalkLayer1 != BroadIlluminateFanOut ||
		report.WalkLayer2 != BroadIlluminateFanOut ||
		report.WalkLayer3 != BroadIlluminateFanOut {
		return report, fmt.Errorf("BFS topology mismatch: root degree=%d, layers=%d/%d/%d; want %d each",
			report.WalkRootDegree, report.WalkLayer1, report.WalkLayer2, report.WalkLayer3, BroadIlluminateFanOut)
	}

	ppr, err := lantern.Illuminate(ctx, WalkSeed, client.WithPPR(client.PPROpts{
		TopN: 32, RestartProb: 0.2, Epsilon: 1e-6,
	}))
	if err != nil {
		return report, fmt.Errorf("verify PPR: %w", err)
	}
	report.PPRVertices = len(ppr.Vertices)
	if report.PPRVertices < 17 { // seed plus a non-trivial portion of top-32
		return report, fmt.Errorf("PPR touched only %d vertices, want at least 17", report.PPRVertices)
	}

	community, err := lantern.Illuminate(ctx, CommunitySeed, client.WithLocalCommunity(client.LocalCommunityOpts{
		MaxSize: BroadIlluminateCommunitySize, RestartProb: 0.2, Epsilon: 1e-6,
		Reduction: client.ReductionMinimumSpanningTree, Objective: client.ObjectiveMinimize,
	}))
	if err != nil {
		return report, fmt.Errorf("verify community: %w", err)
	}
	for i := 0; i < BroadIlluminateCommunitySize; i++ {
		key := communityKey(communityAlphaPrefix, i)
		if _, ok := community.Vertices[key]; !ok {
			return report, fmt.Errorf("community missing planted member %q", key)
		}
		report.CommunityMember++
	}
	for i := 0; i < BroadIlluminateCommunitySize; i++ {
		if _, ok := community.Vertices[communityKey(communityBetaPrefix, i)]; ok {
			return report, fmt.Errorf("community leaked weak-bridge member %q", communityKey(communityBetaPrefix, i))
		}
	}
	report.CommunityEdges = edgeCount(community)
	if report.CommunityEdges != BroadIlluminateCommunitySize-1 {
		return report, fmt.Errorf("community arborescence edges=%d, want %d", report.CommunityEdges, BroadIlluminateCommunitySize-1)
	}
	return report, nil
}

func walkKey(layer, index int) string { return fmt.Sprintf("bench:walk:l%d:%02d", layer, index) }

func communityKey(prefix string, index int) string { return fmt.Sprintf("%s%02d", prefix, index) }

func hasWalkLayer(key string, layer int) bool {
	return len(key) >= len("bench:walk:l1:") && key[:len("bench:walk:l1:")] == fmt.Sprintf("bench:walk:l%d:", layer)
}

func edgeCount(graph *client.Graph) int {
	count := 0
	for _, heads := range graph.Edges {
		count += len(heads)
	}
	return count
}
