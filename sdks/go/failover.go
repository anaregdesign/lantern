// Package client: failover.go owns *Failover, an opt-in wrapper that
// fans a fixed, caller-supplied set of Lantern endpoints into a single
// client which transparently rotates to a healthy replica when the
// current one is unreachable.
//
// This is the SDK-native generalisation of the failover helper the MCP
// server used to carry privately (#592): it is a STATIC-endpoint, no-
// discovery failover. You hand it the replica addresses up front; it
// never learns new ones at runtime. For the leaderless full-replica
// topology Lantern targets (see docs/replication.md) every replica holds
// the same keyspace, so any reachable replica can serve any request.
//
// Failover policy (sticky-current ring walk):
//
//   - The endpoints form an ordered ring with a sticky cursor (cur).
//   - Each call tries the current endpoint first.
//   - It advances to the next endpoint ONLY when the current one reports
//     ErrUnavailable (connect.CodeUnavailable: dial failure or a server
//     UNAVAILABLE reply). It walks the ring at most once.
//   - The FIRST endpoint that returns success — or any non-Unavailable
//     application error (NotFound, InvalidArgument, …) — wins, and the
//     cursor sticks to it so subsequent calls start there.
//   - If every endpoint is Unavailable the last error is returned.
//
// Keying failover on ErrUnavailable (not a blanket retry) is the
// correctness boundary: for the additive write surface (AddEdge/AddEdges)
// an Unavailable result means the dead node committed NOTHING, so retrying
// the same contribution on a sibling replica cannot double-count. A non-
// Unavailable error means the request was actually processed (and rejected
// or not-found) somewhere — failing over would be wrong, so we surface it
// verbatim. Combined with WithIdempotentAdds (#588), even the residual
// at-least-once window of a mid-flight Unavailable retry is dedup-safe.
package client

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Failover is a fixed-membership, sticky-current failover client over a
// set of Lantern replicas. Construct one via NewLanternFailover and share
// it across goroutines (the cursor is updated atomically). Every method
// mirrors the matching *Lantern method; the only added behaviour is the
// transparent rotation described in the package-level failover.go doc.
//
// Failover wraps already-constructed *Lantern endpoints, so all per-client
// options (WithIdempotentAdds, WithDefaultTimeout, …) apply uniformly to
// every replica.
type Failover struct {
	nodes []failoverNode
	cur   atomic.Uint64
}

// failoverNode is the unexported endpoint contract the ring walk delegates
// to. *Lantern satisfies it (compile assertion below); the white-box tests
// substitute a fake to exercise the rotation logic without dialing a
// server. It mirrors the full unary surface of *Lantern that Failover
// re-exports.
type failoverNode interface {
	PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error
	PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error
	PutVertices(ctx context.Context, inputs []VertexInput) error
	GetVertex(ctx context.Context, key string) (*Vertex, error)
	GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error)
	DeleteVertex(ctx context.Context, key string) (bool, error)
	DeleteVertices(ctx context.Context, keys []string) (int, error)
	ScanVertices(ctx context.Context, prefix string, opts ...ScanOption) (vertices []*Vertex, nextCursor []byte, err error)
	CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error)
	DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error)
	AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error
	AddEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) error
	AddEdges(ctx context.Context, inputs []EdgeInput) error
	PutEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error
	PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) error
	PutEdges(ctx context.Context, inputs []EdgeInput) error
	GetEdge(ctx context.Context, tail, head string) (*Edge, error)
	GetEdges(ctx context.Context, refs []EdgeRef) (found []*Edge, missing []EdgeRef, err error)
	ScanEdges(ctx context.Context, opts ...EdgeScanOption) (edges []*Edge, nextCursor []byte, err error)
	DeleteEdge(ctx context.Context, tail, head string) (bool, error)
	DeleteEdges(ctx context.Context, refs []EdgeRef) (int, error)
	Illuminate(ctx context.Context, seed string, opts ...IlluminateOption) (*Graph, error)
	Ping(ctx context.Context) error
	Close() error
}

// Compile-time assertion: *Lantern satisfies failoverNode.
var _ failoverNode = (*Lantern)(nil)

// NewLanternFailover dials every address in addrs and returns a single
// client that transparently fails over between them. addrs must be non-
// empty; each address follows the same scheme contract as NewLantern
// ("http://host:port" for h2c, "https://host[:port]" for TLS). The opts
// apply identically to every endpoint.
//
// A single-element addrs is valid and behaves as a plain single-endpoint
// client (the ring has one node, so no rotation is possible) — callers
// that conditionally pass one or many addresses need no special-casing.
//
// If any endpoint fails to dial, every successfully-dialed endpoint is
// closed before returning, so a partial failure leaks no connections.
func NewLanternFailover(addrs []string, opts ...Option) (*Failover, error) {
	if len(addrs) == 0 {
		return nil, errors.New("client: NewLanternFailover requires at least one address")
	}
	nodes := make([]failoverNode, 0, len(addrs))
	for _, addr := range addrs {
		l, err := NewLantern(addr, opts...)
		if err != nil {
			for _, n := range nodes {
				_ = n.Close()
			}
			return nil, err
		}
		nodes = append(nodes, l)
	}
	return &Failover{nodes: nodes}, nil
}

// try runs fn against the sticky-current endpoint, rotating to the next
// endpoint only on ErrUnavailable and walking the ring at most once. The
// first success or non-Unavailable application error wins and becomes the
// new sticky current. If every endpoint is Unavailable the last error is
// returned.
func (f *Failover) try(fn func(failoverNode) error) error {
	n := len(f.nodes)
	start := int(f.cur.Load() % uint64(n))
	var lastErr error
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		err := fn(f.nodes[idx])
		if errors.Is(err, ErrUnavailable) {
			lastErr = err
			continue
		}
		// Success or a non-Unavailable application error: this endpoint
		// answered. Stick to it and return the result verbatim.
		f.cur.Store(uint64(idx))
		return err
	}
	return lastErr
}

// PutVertex forwards to the current endpoint's PutVertex, failing over on
// ErrUnavailable.
func (f *Failover) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return f.try(func(l failoverNode) error { return l.PutVertex(ctx, key, value, ttl) })
}

// PutVertexAt forwards to the current endpoint's PutVertexAt, failing over
// on ErrUnavailable.
func (f *Failover) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error {
	return f.try(func(l failoverNode) error { return l.PutVertexAt(ctx, key, value, expiration) })
}

// PutVertices forwards to the current endpoint's PutVertices, failing over
// on ErrUnavailable.
func (f *Failover) PutVertices(ctx context.Context, inputs []VertexInput) error {
	return f.try(func(l failoverNode) error { return l.PutVertices(ctx, inputs) })
}

// GetVertex forwards to the current endpoint's GetVertex, failing over on
// ErrUnavailable.
func (f *Failover) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	var out *Vertex
	err := f.try(func(l failoverNode) error {
		v, e := l.GetVertex(ctx, key)
		out = v
		return e
	})
	return out, err
}

// GetVertices forwards to the current endpoint's GetVertices, failing over
// on ErrUnavailable.
func (f *Failover) GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error) {
	e := f.try(func(l failoverNode) error {
		var ie error
		found, missing, ie = l.GetVertices(ctx, keys)
		return ie
	})
	return found, missing, e
}

// DeleteVertex forwards to the current endpoint's DeleteVertex, failing
// over on ErrUnavailable.
func (f *Failover) DeleteVertex(ctx context.Context, key string) (bool, error) {
	var existed bool
	err := f.try(func(l failoverNode) error {
		b, e := l.DeleteVertex(ctx, key)
		existed = b
		return e
	})
	return existed, err
}

// DeleteVertices forwards to the current endpoint's DeleteVertices, failing
// over on ErrUnavailable.
func (f *Failover) DeleteVertices(ctx context.Context, keys []string) (int, error) {
	var deleted int
	err := f.try(func(l failoverNode) error {
		d, e := l.DeleteVertices(ctx, keys)
		deleted = d
		return e
	})
	return deleted, err
}

// ScanVertices forwards to the current endpoint's ScanVertices, failing
// over on ErrUnavailable. Scan cursors are radix positions over the shared
// replicated keyspace, so a cursor minted by one replica is valid against
// any other — a mid-scan rotation resumes correctly.
func (f *Failover) ScanVertices(ctx context.Context, prefix string, opts ...ScanOption) (vertices []*Vertex, nextCursor []byte, err error) {
	e := f.try(func(l failoverNode) error {
		var ie error
		vertices, nextCursor, ie = l.ScanVertices(ctx, prefix, opts...)
		return ie
	})
	return vertices, nextCursor, e
}

// CountVerticesByPrefix forwards to the current endpoint's
// CountVerticesByPrefix, failing over on ErrUnavailable.
func (f *Failover) CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error) {
	var count uint64
	err := f.try(func(l failoverNode) error {
		c, e := l.CountVerticesByPrefix(ctx, prefix)
		count = c
		return e
	})
	return count, err
}

// DeleteVerticesByPrefix forwards to the current endpoint's
// DeleteVerticesByPrefix, failing over on ErrUnavailable.
func (f *Failover) DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error) {
	var deleted uint64
	err := f.try(func(l failoverNode) error {
		d, e := l.DeleteVerticesByPrefix(ctx, prefix, opts...)
		deleted = d
		return e
	})
	return deleted, err
}

// AddEdge forwards to the current endpoint's AddEdge, failing over on
// ErrUnavailable. Because an Unavailable result means the dead node
// committed nothing, the additive contribution is retried on a sibling
// replica without risk of double-counting.
func (f *Failover) AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error {
	return f.try(func(l failoverNode) error { return l.AddEdge(ctx, tail, head, weight, ttl) })
}

// AddEdgeAt forwards to the current endpoint's AddEdgeAt, failing over on
// ErrUnavailable.
func (f *Failover) AddEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) error {
	return f.try(func(l failoverNode) error { return l.AddEdgeAt(ctx, tail, head, weight, expiration) })
}

// AddEdges forwards to the current endpoint's AddEdges, failing over on
// ErrUnavailable.
func (f *Failover) AddEdges(ctx context.Context, inputs []EdgeInput) error {
	return f.try(func(l failoverNode) error { return l.AddEdges(ctx, inputs) })
}

// PutEdge forwards to the current endpoint's PutEdge, failing over on
// ErrUnavailable.
func (f *Failover) PutEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error {
	return f.try(func(l failoverNode) error { return l.PutEdge(ctx, tail, head, weight, ttl) })
}

// PutEdgeAt forwards to the current endpoint's PutEdgeAt, failing over on
// ErrUnavailable.
func (f *Failover) PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) error {
	return f.try(func(l failoverNode) error { return l.PutEdgeAt(ctx, tail, head, weight, expiration) })
}

// PutEdges forwards to the current endpoint's PutEdges, failing over on
// ErrUnavailable.
func (f *Failover) PutEdges(ctx context.Context, inputs []EdgeInput) error {
	return f.try(func(l failoverNode) error { return l.PutEdges(ctx, inputs) })
}

// GetEdge forwards to the current endpoint's GetEdge, failing over on
// ErrUnavailable.
func (f *Failover) GetEdge(ctx context.Context, tail, head string) (*Edge, error) {
	var out *Edge
	err := f.try(func(l failoverNode) error {
		e, ie := l.GetEdge(ctx, tail, head)
		out = e
		return ie
	})
	return out, err
}

// GetEdges forwards to the current endpoint's GetEdges, failing over on
// ErrUnavailable.
func (f *Failover) GetEdges(ctx context.Context, refs []EdgeRef) (found []*Edge, missing []EdgeRef, err error) {
	e := f.try(func(l failoverNode) error {
		var ie error
		found, missing, ie = l.GetEdges(ctx, refs)
		return ie
	})
	return found, missing, e
}

// ScanEdges forwards to the current endpoint's ScanEdges, failing over on
// ErrUnavailable. As with ScanVertices the cursor is a shared-keyspace
// radix position, so a mid-scan rotation resumes correctly.
func (f *Failover) ScanEdges(ctx context.Context, opts ...EdgeScanOption) (edges []*Edge, nextCursor []byte, err error) {
	e := f.try(func(l failoverNode) error {
		var ie error
		edges, nextCursor, ie = l.ScanEdges(ctx, opts...)
		return ie
	})
	return edges, nextCursor, e
}

// DeleteEdge forwards to the current endpoint's DeleteEdge, failing over on
// ErrUnavailable.
func (f *Failover) DeleteEdge(ctx context.Context, tail, head string) (bool, error) {
	var existed bool
	err := f.try(func(l failoverNode) error {
		b, e := l.DeleteEdge(ctx, tail, head)
		existed = b
		return e
	})
	return existed, err
}

// DeleteEdges forwards to the current endpoint's DeleteEdges, failing over
// on ErrUnavailable.
func (f *Failover) DeleteEdges(ctx context.Context, refs []EdgeRef) (int, error) {
	var deleted int
	err := f.try(func(l failoverNode) error {
		d, e := l.DeleteEdges(ctx, refs)
		deleted = d
		return e
	})
	return deleted, err
}

// Illuminate forwards to the current endpoint's Illuminate, failing over on
// ErrUnavailable.
func (f *Failover) Illuminate(ctx context.Context, seed string, opts ...IlluminateOption) (*Graph, error) {
	var out *Graph
	err := f.try(func(l failoverNode) error {
		g, e := l.Illuminate(ctx, seed, opts...)
		out = g
		return e
	})
	return out, err
}

// Ping reports the cluster healthy if ANY endpoint reports SERVING. Unlike
// the data methods it does not classify the error: the SDK's Ping uses a
// raw HTTP health probe whose transport failure is not a connect code, so
// Ping simply walks every endpoint in sticky order and returns nil on the
// first success, sticking to that endpoint. If every endpoint fails it
// returns the last error.
func (f *Failover) Ping(ctx context.Context) error {
	n := len(f.nodes)
	start := int(f.cur.Load() % uint64(n))
	var lastErr error
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if err := f.nodes[idx].Ping(ctx); err != nil {
			lastErr = err
			continue
		}
		f.cur.Store(uint64(idx))
		return nil
	}
	return lastErr
}

// Close releases every underlying endpoint's idle connections. It attempts
// to close all endpoints and returns the first close error, if any.
func (f *Failover) Close() error {
	var firstErr error
	for _, n := range f.nodes {
		if err := n.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
