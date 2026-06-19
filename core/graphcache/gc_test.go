package graphcache

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestGraphCache_GCFlushMaintainsIndexesWatermarksAndDanglingEdges(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract)

	now := time.Now()
	liveExp := now.Add(time.Minute)
	expired := now.Add(-time.Millisecond)

	if !c.PutVertexWithExpirationHLC("expired:vertex", "expired searchable payload", expired, hlc.Timestamp{WallNs: 1}) {
		t.Fatal("PutVertexWithExpirationHLC(expired:vertex) reported false")
	}
	if got := c.VertexHLCCount(); got != 1 {
		t.Fatalf("VertexHLCCount before flush = %d, want 1", got)
	}
	if got := c.CountByPrefix("expired:"); got != 1 {
		t.Fatalf("CountByPrefix(expired:) before vertex flush = %d, want 1", got)
	}
	if got := c.SearchVertices("expired", 10, ""); got != nil {
		t.Fatalf("SearchVertices(expired) before vertex flush = %v, want nil because liveness filter hides expired entries", keys(got))
	}

	c.PutVertexWithExpiration("tail", "tail payload", liveExp)
	c.PutVertexWithExpiration("head", "head payload", liveExp)
	c.AddEdgeWithExpiration("tail", "head", 3, liveExp)
	if !c.DeleteVertex("head") {
		t.Fatal("DeleteVertex(head) reported false")
	}
	if got := collectScan(c, "tail", "head"); len(got) != 1 {
		t.Fatalf("ScanEdgesByPrefix before dangling sweep = %v, want one dangling edge still present", got)
	}

	c.DeleteVertexHLC("tombstone:old", hlc.Timestamp{WallNs: 2}, expired)
	if len(c.vertexTombstones) != 1 {
		t.Fatalf("vertex tombstones before flush = %d, want 1", len(c.vertexTombstones))
	}

	if removed := c.vertices.Flush(); removed != 1 {
		t.Fatalf("vertices.Flush removed %d, want 1 expired vertex", removed)
	}
	zero, dangling := c.flush()
	if zero != 0 || dangling != 1 {
		t.Fatalf("c.flush removed zero=%d dangling=%d, want zero=0 dangling=1", zero, dangling)
	}

	if got := c.CountByPrefix("expired:"); got != 0 {
		t.Fatalf("CountByPrefix(expired:) after flush = %d, want 0", got)
	}
	if got := c.VertexHLCCount(); got != 0 {
		t.Fatalf("VertexHLCCount after flush = %d, want 0", got)
	}
	if len(c.vertexTombstones) != 0 {
		t.Fatalf("vertex tombstones after flush = %d, want 0", len(c.vertexTombstones))
	}
	if _, _, ok := c.GetEdgeDetail("tail", "head"); ok {
		t.Fatal("GetEdgeDetail(tail, head) returned ok=true after dangling sweep")
	}
	if got := collectScan(c, "tail", "head"); len(got) != 0 {
		t.Fatalf("ScanEdgesByPrefix after dangling sweep = %v, want empty", got)
	}
	if completed := c.ScanByPrefix(context.Background(), "expired:", func(_, _ string, _ string) bool {
		t.Fatal("ScanByPrefix yielded an expired vertex after flush")
		return false
	}); !completed {
		t.Fatal("ScanByPrefix after flush returned completed=false")
	}
}
