package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

// searchVertexDocument mirrors the production provider projection
// (server/provider/search.go) closely enough for the wire test: it folds
// the key together with the string value so a query matches either. The
// integration suite cannot import the unexported provider helper, and the
// exact value-type rendering is covered by the provider unit test — here we
// only need a live index behind the RPC.
func searchVertexDocument(v *pb.Vertex) search.Document {
	var b strings.Builder
	b.WriteString(v.GetKey())
	if s := v.GetString_(); s != "" {
		b.WriteByte(' ')
		b.WriteString(s)
	}
	return search.Text(b.String())
}

// newSearchRawClient stands up a Connect server whose GraphCache has the
// search index enabled (or not) and whose service gates SearchVertices on
// the matching SearchLimits.Enabled flag — the same two-sided agreement the
// production composition root makes.
func newSearchRawClient(t *testing.T, enabled bool) graphv1connect.LanternServiceClient {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	if enabled {
		cache.EnableSearchIndex(searchVertexDocument)
	}
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:      enabled,
		DefaultLimit: 100,
		MaxLimit:     1000,
	})
	srv := newConnectTestServer(t, svc, nil)
	return graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
}

func TestSearchVertices_EndToEnd(t *testing.T) {
	c := newSearchRawClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "user.preferences.tone", Value: &pb.Vertex_String_{String_: "calm and concise"}, Expiration: exp},
		{Key: "user.preferences.format", Value: &pb.Vertex_String_{String_: "calm bullet points"}, Expiration: exp},
		{Key: "project.lantern.stack", Value: &pb.Vertex_String_{String_: "go and react"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Content query matches the two "calm" preference vertices, ranked.
	resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "calm"}))
	if err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	hits := resp.Msg.GetHits()
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.GetKey(), "user.preferences.") {
			t.Errorf("unexpected hit key %q", h.GetKey())
		}
		if h.GetScore() <= 0 {
			t.Errorf("hit %q score = %v, want > 0", h.GetKey(), h.GetScore())
		}
	}

	// Prefix scopes the same query to a single namespace branch.
	scoped, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
		Query:  "calm",
		Prefix: "user.preferences.tone",
	}))
	if err != nil {
		t.Fatalf("SearchVertices scoped: %v", err)
	}
	if got := scoped.Msg.GetHits(); len(got) != 1 || got[0].GetKey() != "user.preferences.tone" {
		t.Fatalf("scoped hits = %+v, want only user.preferences.tone", got)
	}

	// Empty query is a zero-hit success, not an error.
	empty, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: ""}))
	if err != nil {
		t.Fatalf("SearchVertices empty query: %v", err)
	}
	if got := empty.Msg.GetHits(); len(got) != 0 {
		t.Fatalf("empty-query hits = %d, want 0", len(got))
	}
}

func TestSearchVertices_DisabledReturnsFailedPrecondition(t *testing.T) {
	c := newSearchRawClient(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "calm"}))
	if err == nil {
		t.Fatalf("expected FAILED_PRECONDITION when search disabled")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
}
