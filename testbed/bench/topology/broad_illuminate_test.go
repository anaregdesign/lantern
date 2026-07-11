package topology

import (
	"testing"
	"time"
)

// TestNewBroadIlluminateFixture_SemanticTopologyGuard is the static companion
// to preflight's live RPC assertions. It prevents an apparently harmless
// fixture edit from collapsing the branching/depth/community work that the
// broad_illuminate release producer claims to measure.
func TestNewBroadIlluminateFixture_SemanticTopologyGuard(t *testing.T) {
	fixture := NewBroadIlluminateFixture(time.Now().Add(time.Hour))
	adj := make(map[string]map[string]float32)
	for _, edge := range fixture.Edges {
		if adj[edge.Tail] == nil {
			adj[edge.Tail] = make(map[string]float32)
		}
		adj[edge.Tail][edge.Head] = edge.Weight
	}
	if got := len(adj[WalkSeed]); got != BroadIlluminateFanOut {
		t.Fatalf("walk root degree = %d, want %d", got, BroadIlluminateFanOut)
	}
	for layer := 1; layer < BroadIlluminateDepth; layer++ {
		for i := 0; i < BroadIlluminateFanOut; i++ {
			if got := len(adj[walkKey(layer, i)]); got != BroadIlluminateFanOut {
				t.Fatalf("%s degree = %d, want %d", walkKey(layer, i), got, BroadIlluminateFanOut)
			}
		}
	}
	for i := 0; i < BroadIlluminateFanOut; i++ {
		if _, ok := adj[walkKey(BroadIlluminateDepth-1, i)][walkKey(BroadIlluminateDepth, i)]; !ok {
			t.Fatalf("missing a third-hop path through %s", walkKey(BroadIlluminateDepth-1, i))
		}
	}
	for _, prefix := range []string{communityAlphaPrefix, communityBetaPrefix} {
		for i := 0; i < BroadIlluminateCommunitySize; i++ {
			if got := len(adj[communityKey(prefix, i)]); got < BroadIlluminateCommunitySize-1 {
				t.Fatalf("%s internal degree = %d, want >= %d", communityKey(prefix, i), got, BroadIlluminateCommunitySize-1)
			}
		}
	}
	if got := adj[communityKey(communityAlphaPrefix, 0)][communityKey(communityBetaPrefix, 0)]; got != 0.01 {
		t.Fatalf("alpha→beta bridge = %v, want 0.01", got)
	}
	if got := adj[communityKey(communityBetaPrefix, 0)][communityKey(communityAlphaPrefix, 0)]; got != 0.01 {
		t.Fatalf("beta→alpha bridge = %v, want 0.01", got)
	}
}
