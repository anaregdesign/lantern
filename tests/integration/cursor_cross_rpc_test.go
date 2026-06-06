package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// TestSDK_Cursor_CrossRPC_Rejected is the end-to-end pin for #168: a
// next_cursor minted by ScanVertices, fed back into ScanEdges (and vice
// versa), must surface as INVALID_ARGUMENT rather than silently restart
// the scan. Exercises the full wire path so the proto comments and
// server-side discriminator decode stay honest.
func TestSDK_Cursor_CrossRPC_Rejected(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Seed enough vertices that limit=1 forces a non-empty NextCursor.
	seedPrefixVertices(t, ctx, l, []string{"a/1", "a/2", "a/3"})
	// And enough edges that an edge scan can also paginate.
	seedPrefixEdges(t, ctx, l, [][2]string{
		{"a/1", "b/1"}, {"a/2", "b/2"},
	})

	_, vertexCursor, err := l.ScanVertices(ctx, "a/", client.WithScanLimit(1))
	if err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if len(vertexCursor) == 0 {
		t.Fatalf("ScanVertices returned empty cursor; cannot exercise cross-feed")
	}

	_, edgeCursor, err := l.ScanEdges(ctx,
		client.WithEdgeScanTailPrefix("a/"),
		client.WithEdgeScanLimit(1),
	)
	if err != nil {
		t.Fatalf("ScanEdges: %v", err)
	}
	if len(edgeCursor) == 0 {
		t.Fatalf("ScanEdges returned empty cursor; cannot exercise cross-feed")
	}

	// Vertex cursor -> ScanEdges must reject.
	if _, _, err := l.ScanEdges(ctx,
		client.WithEdgeScanTailPrefix("a/"),
		client.WithEdgeScanLimit(1),
		client.WithEdgeScanCursor(vertexCursor),
	); err == nil {
		t.Fatalf("ScanEdges(vertex cursor) = nil error, want InvalidArgument")
	} else if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("ScanEdges(vertex cursor) err = %v, want ErrInvalidArgument", err)
	}

	// Edge cursor -> ScanVertices must reject.
	if _, _, err := l.ScanVertices(ctx, "a/",
		client.WithScanLimit(1),
		client.WithScanCursor(edgeCursor),
	); err == nil {
		t.Fatalf("ScanVertices(edge cursor) = nil error, want InvalidArgument")
	} else if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("ScanVertices(edge cursor) err = %v, want ErrInvalidArgument", err)
	}
}
