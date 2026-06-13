package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// TestNewLantern_BaseURLValidation covers the constructor's argument-
// shape guards: empty baseURL, missing scheme, bare host:port. The SDK
// must fail loudly rather than producing a *Lantern that 404s on every
// call.
//
// Happy-path round-trip coverage lives in tests/integration (against
// the real server/service handler) — keeping a fake-handler smoke
// suite here would just duplicate that without exercising any code
// path tests/integration can't reach.
func TestNewLantern_BaseURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{name: "empty", baseURL: ""},
		{name: "missing scheme", baseURL: "lantern:6381"},
		{name: "bare host:port", baseURL: "localhost:6380"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLantern(tc.baseURL); err == nil {
				t.Fatalf("NewLantern(%q): want error, got nil", tc.baseURL)
			}
		})
	}
}

// TestExpirationFromTTL pins the opt-in decay contract (#523): a
// non-positive ttl yields the zero time.Time (the wire's permanent
// sentinel), while a positive ttl materialises an absolute expiration
// at now+ttl. The relative-TTL convenience methods (PutVertex, AddEdge,
// PutEdge) route through this helper, so an omitted/zero TTL stores a
// vertex/edge permanently rather than injecting a hidden default.
func TestExpirationFromTTL(t *testing.T) {
	t.Run("zero ttl is permanent", func(t *testing.T) {
		if got := expirationFromTTL(0); !got.IsZero() {
			t.Fatalf("expirationFromTTL(0) = %v, want zero time", got)
		}
	})
	t.Run("negative ttl is permanent", func(t *testing.T) {
		if got := expirationFromTTL(-time.Hour); !got.IsZero() {
			t.Fatalf("expirationFromTTL(-1h) = %v, want zero time", got)
		}
	})
	t.Run("positive ttl materialises now+ttl", func(t *testing.T) {
		const ttl = time.Minute
		before := time.Now().Add(ttl)
		got := expirationFromTTL(ttl)
		after := time.Now().Add(ttl)
		if got.IsZero() {
			t.Fatalf("expirationFromTTL(%v) = zero time, want absolute expiration", ttl)
		}
		if got.Before(before) || got.After(after) {
			t.Fatalf("expirationFromTTL(%v) = %v, want within [%v, %v]", ttl, got, before, after)
		}
	})
}

// mustLantern builds a *Lantern against a never-dialed local URL. The
// constructor performs no I/O, so this is safe for white-box tests that only
// exercise request-construction logic.
func mustLantern(t *testing.T, opts ...Option) *Lantern {
	t.Helper()
	l, err := NewLantern("http://localhost:6380", opts...)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	return l
}

// decodeContribID splits a 24-byte ContribID into its packed (seq, idx)
// suffix, mirroring nextContribIDs' big-endian (seq<<16)|idx layout.
func decodeContribID(t *testing.T, id []byte) (seq uint64, idx uint16) {
	t.Helper()
	if len(id) != 24 {
		t.Fatalf("contrib_id must be 24 bytes, got %d", len(id))
	}
	packed := binary.BigEndian.Uint64(id[16:])
	return packed >> 16, uint16(packed & 0xffff)
}

// TestNextContribIDs pins the per-call idempotency-key generator (#588): it
// is nil unless WithIdempotentAdds opted in, packs the client nonce in the
// high 16 bytes and an index-aligned (seq<<16)|idx in the low 8, shares one
// seq across all keys of a single call, and advances the seq on the next
// call while keeping the nonce stable.
func TestNextContribIDs(t *testing.T) {
	t.Run("nil when idempotency disabled", func(t *testing.T) {
		l := mustLantern(t)
		if ids := l.nextContribIDs(3); ids != nil {
			t.Fatalf("disabled client must return nil, got %d ids", len(ids))
		}
	})
	t.Run("nil for non-positive n even when enabled", func(t *testing.T) {
		l := mustLantern(t, WithIdempotentAdds())
		if ids := l.nextContribIDs(0); ids != nil {
			t.Fatalf("n=0 must return nil, got %d", len(ids))
		}
		if ids := l.nextContribIDs(-1); ids != nil {
			t.Fatalf("n<0 must return nil, got %d", len(ids))
		}
	})
	t.Run("layout: nonce prefix + index-aligned shared seq", func(t *testing.T) {
		l := mustLantern(t, WithIdempotentAdds())
		ids := l.nextContribIDs(3)
		if len(ids) != 3 {
			t.Fatalf("want 3 ids, got %d", len(ids))
		}
		var seq0 uint64
		for i, id := range ids {
			if !bytes.Equal(id[:16], l.nonce[:]) {
				t.Fatalf("id %d prefix must equal the client nonce", i)
			}
			seq, idx := decodeContribID(t, id)
			if int(idx) != i {
				t.Fatalf("id %d index want %d got %d", i, i, idx)
			}
			if i == 0 {
				seq0 = seq
			} else if seq != seq0 {
				t.Fatalf("all ids in one call share a seq; id %d seq %d != %d", i, seq, seq0)
			}
		}
	})
	t.Run("callSeq advances per call; nonce stable", func(t *testing.T) {
		l := mustLantern(t, WithIdempotentAdds())
		first := l.nextContribIDs(1)[0]
		second := l.nextContribIDs(1)[0]
		seqA, _ := decodeContribID(t, first)
		seqB, _ := decodeContribID(t, second)
		if seqB <= seqA {
			t.Fatalf("callSeq must increase per call: %d then %d", seqA, seqB)
		}
		if !bytes.Equal(first[:16], second[:16]) {
			t.Fatalf("nonce prefix must be stable across calls")
		}
	})
}

// captureAddEdges is a fake LanternServiceClient that records every
// AddEdgesRequest it receives. It embeds the interface (left nil) so it
// satisfies the full surface while only overriding AddEdges — the single
// method the Add* helpers route through.
type captureAddEdges struct {
	graphv1connect.LanternServiceClient
	reqs []*pb.AddEdgesRequest
}

func (c *captureAddEdges) AddEdges(_ context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	c.reqs = append(c.reqs, req.Msg)
	return connect.NewResponse(&pb.AddEdgesResponse{}), nil
}

// TestAddContribIDWiring verifies the SDK attaches contrib_ids only when
// WithIdempotentAdds is set, that AddEdge stamps exactly one key, and that
// AddEdges stamps index-aligned keys per chunk with a fresh seq per chunk.
func TestAddContribIDWiring(t *testing.T) {
	newClient := func(t *testing.T, opts ...Option) (*Lantern, *captureAddEdges) {
		t.Helper()
		l := mustLantern(t, opts...)
		capt := &captureAddEdges{}
		l.client = capt
		return l, capt
	}

	t.Run("default sends no contrib_ids (AddEdge)", func(t *testing.T) {
		l, capt := newClient(t)
		if err := l.AddEdge(context.Background(), "a", "b", 1, 0); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if len(capt.reqs) != 1 {
			t.Fatalf("want 1 request, got %d", len(capt.reqs))
		}
		if got := capt.reqs[0].GetContribIds(); got != nil {
			t.Fatalf("default must send no contrib_ids, got %d", len(got))
		}
	})

	t.Run("WithIdempotentAdds stamps one key (AddEdge)", func(t *testing.T) {
		l, capt := newClient(t, WithIdempotentAdds())
		if err := l.AddEdge(context.Background(), "a", "b", 1, 0); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		ids := capt.reqs[0].GetContribIds()
		if len(ids) != 1 {
			t.Fatalf("want 1 contrib_id, got %d", len(ids))
		}
		if !bytes.Equal(ids[0][:16], l.nonce[:]) {
			t.Fatalf("contrib_id prefix must be the client nonce")
		}
	})

	t.Run("WithIdempotentAdds stamps index-aligned keys per chunk (AddEdges)", func(t *testing.T) {
		l, capt := newClient(t, WithIdempotentAdds(), WithBatchChunkSize(2))
		inputs := []EdgeInput{
			{Tail: "a", Head: "b", Weight: 1},
			{Tail: "c", Head: "d", Weight: 1},
			{Tail: "e", Head: "f", Weight: 1},
		}
		if err := l.AddEdges(context.Background(), inputs); err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		if len(capt.reqs) != 2 {
			t.Fatalf("chunk size 2 over 3 inputs want 2 requests, got %d", len(capt.reqs))
		}
		c0, c1 := capt.reqs[0].GetContribIds(), capt.reqs[1].GetContribIds()
		if len(c0) != 2 || len(c1) != 1 {
			t.Fatalf("chunk key counts want 2,1 got %d,%d", len(c0), len(c1))
		}
		seq0, idx00 := decodeContribID(t, c0[0])
		_, idx01 := decodeContribID(t, c0[1])
		if idx00 != 0 || idx01 != 1 {
			t.Fatalf("chunk 0 indices want 0,1 got %d,%d", idx00, idx01)
		}
		seq1, idx10 := decodeContribID(t, c1[0])
		if idx10 != 0 {
			t.Fatalf("chunk 1 index want 0 got %d", idx10)
		}
		if seq1 == seq0 {
			t.Fatalf("each chunk must use a distinct seq; got %d twice", seq0)
		}
	})

	t.Run("default sends no contrib_ids (AddEdges)", func(t *testing.T) {
		l, capt := newClient(t)
		if err := l.AddEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}}); err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		if got := capt.reqs[0].GetContribIds(); got != nil {
			t.Fatalf("default AddEdges must send no contrib_ids, got %d", len(got))
		}
	})
}

// captureIlluminate is a fake LanternServiceClient that records every
// IlluminateRequest it receives. It embeds the interface (left nil) so it
// satisfies the full surface while only overriding Illuminate.
type captureIlluminate struct {
	graphv1connect.LanternServiceClient
	reqs []*pb.IlluminateRequest
}

func (c *captureIlluminate) Illuminate(_ context.Context, req *connect.Request[pb.IlluminateRequest]) (*connect.Response[pb.IlluminateResponse], error) {
	c.reqs = append(c.reqs, req.Msg)
	return connect.NewResponse(&pb.IlluminateResponse{Graph: &pb.Graph{}}), nil
}

// TestWithVertexPrefixWiring verifies WithVertexPrefix round-trips onto the
// emitted IlluminateRequest and that omitting it leaves the field empty (the
// server's no-filter default).
func TestWithVertexPrefixWiring(t *testing.T) {
	newClient := func(t *testing.T) (*Lantern, *captureIlluminate) {
		t.Helper()
		l := mustLantern(t)
		capt := &captureIlluminate{}
		l.client = capt
		return l, capt
	}

	t.Run("default leaves vertex_prefix empty", func(t *testing.T) {
		l, capt := newClient(t)
		if _, err := l.Illuminate(context.Background(), "seed"); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if len(capt.reqs) != 1 {
			t.Fatalf("want 1 request, got %d", len(capt.reqs))
		}
		if got := capt.reqs[0].GetVertexPrefix(); got != "" {
			t.Fatalf("default must send empty vertex_prefix, got %q", got)
		}
	})

	t.Run("WithVertexPrefix sets the field", func(t *testing.T) {
		l, capt := newClient(t)
		if _, err := l.Illuminate(context.Background(), "users/1", WithVertexPrefix("users/")); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if got := capt.reqs[0].GetVertexPrefix(); got != "users/" {
			t.Fatalf("vertex_prefix want %q got %q", "users/", got)
		}
	})
}
