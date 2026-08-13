package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// TestCausalMetadataCapacity_WireGate covers the external failure contract at
// the real Connect/h2c boundary. The first born-expired Put is accepted and
// retains its resurrection floor; a second identity is rejected atomically,
// while an equal/newer Delete of the existing identity replaces the barrier
// without needing another budget slot.
func TestCausalMetadataCapacity_WireGate(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{
		TTL: time.Minute, MaxVertexCausalEntries: 1, MaxEdgeCausalEntries: 1,
	}, provider.SearchConfig{})
	log := mutationlog.New(mutationlog.Options{Capacity: 16, SubscriberBuffer: 16})
	t.Cleanup(func() { _ = log.Close() })
	var nodeID hlc.NodeID
	copy(nodeID[:], "causal-capacity")
	clock := hlc.New(nodeID, hlc.Options{})
	svc := service.NewLanternService(cache).
		WithReplication(log, clock, nil).
		WithTombstoneTTL(time.Hour)
	validation := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, validation.ConnectInterceptor())
	sdk := newConnectClientFor(t, srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, err := sdk.PutVertexAt(ctx, "first", "expired", time.Now().Add(-time.Second))
	if err != nil || outcome != client.PutOutcomeExpired {
		t.Fatalf("first Put = (%v, %v), want EXPIRED, nil", outcome, err)
	}
	if log.Len() != 1 {
		t.Fatalf("mutation log after accepted Put = %d, want 1", log.Len())
	}

	_, err = sdk.PutVertex(ctx, "rejected", "live", time.Minute)
	if !errors.Is(err, client.ErrResourceExhausted) {
		t.Fatalf("second Put error = %v, want ErrResourceExhausted", err)
	}
	if _, ok := cache.GetVertex("rejected"); ok {
		t.Fatal("capacity-rejected Put changed the live graph")
	}
	if log.Len() != 1 {
		t.Fatalf("capacity-rejected Put appended mutation: log len = %d", log.Len())
	}

	if _, err := sdk.DeleteVertex(ctx, "first"); err != nil {
		t.Fatalf("same-identity Delete at capacity: %v", err)
	}
	if log.Len() != 2 {
		t.Fatalf("accepted Delete log len = %d, want 2", log.Len())
	}
	stats := cache.CausalMetadataStats()
	if stats.VertexEntries != 1 || stats.VertexRejected != 1 || stats.VertexOverLimit {
		t.Fatalf("causal stats = %+v", stats)
	}
	status, err := sdk.GetServerStatus(ctx)
	if err != nil {
		t.Fatalf("GetServerStatus after vertex rejection: %v", err)
	}
	vertexStatus := status.GetCausalMetadata().GetVertices()
	if vertexStatus.GetLimit() != 1 || vertexStatus.GetEntries() != 1 || vertexStatus.GetRejectedTotal() != 1 || vertexStatus.GetOverLimit() {
		t.Fatalf("wire vertex causal status = %v, want limit=entries=rejected=1 and over_limit=false", vertexStatus)
	}

	edgeOutcome, err := sdk.PutEdgeAt(ctx, "tail", "head", 1, time.Now().Add(-time.Second))
	if err != nil || edgeOutcome != client.PutOutcomeExpired {
		t.Fatalf("first edge Put = (%v, %v), want EXPIRED, nil", edgeOutcome, err)
	}
	if log.Len() != 3 {
		t.Fatalf("mutation log after accepted edge Put = %d, want 3", log.Len())
	}
	_, err = sdk.PutEdge(ctx, "rejected-tail", "rejected-head", 1, time.Minute)
	if !errors.Is(err, client.ErrResourceExhausted) {
		t.Fatalf("second edge Put error = %v, want ErrResourceExhausted", err)
	}
	if _, _, ok := cache.GetEdgeDetail("rejected-tail", "rejected-head"); ok {
		t.Fatal("capacity-rejected edge Put changed the live graph")
	}
	if log.Len() != 3 {
		t.Fatalf("capacity-rejected edge Put appended mutation: log len = %d", log.Len())
	}
	if _, err := sdk.DeleteEdge(ctx, "tail", "head"); err != nil {
		t.Fatalf("same-identity edge Delete at capacity: %v", err)
	}
	stats = cache.CausalMetadataStats()
	if stats.EdgeEntries != 1 || stats.EdgeRejected != 1 || stats.EdgeOverLimit {
		t.Fatalf("edge causal stats = %+v", stats)
	}
	status, err = sdk.GetServerStatus(ctx)
	if err != nil {
		t.Fatalf("GetServerStatus after edge rejection: %v", err)
	}
	edgeStatus := status.GetCausalMetadata().GetEdges()
	if edgeStatus.GetLimit() != 1 || edgeStatus.GetEntries() != 1 || edgeStatus.GetRejectedTotal() != 1 || edgeStatus.GetOverLimit() {
		t.Fatalf("wire edge causal status = %v, want limit=entries=rejected=1 and over_limit=false", edgeStatus)
	}
}
