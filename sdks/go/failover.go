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

	// retry, when non-nil (WithRetry passed to NewLanternFailover), drives
	// the ring walk through a bounded full-jitter backoff loop: each retry
	// attempt re-runs try, resuming from the sticky cursor the previous
	// attempt advanced on ErrUnavailable, so MaxAttempts is the cross-replica
	// budget (#849). The endpoints' own per-node unary retry is neutralised
	// (clearNodeRetry) so the loop runs exactly once, here.
	retry *RetryPolicy
	// idempotentAdds mirrors WithIdempotentAdds so call can gate the additive
	// write surface (AddEdge/AddEdgeAt/AddEdges) — retried only when the nodes
	// stamp per-edge ContribIDs.
	idempotentAdds bool
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
	PutVertexIfAbsent(ctx context.Context, key string, value any, ttl time.Duration) (bool, error)
	PutVertexIfAbsentAt(ctx context.Context, key string, value any, expiration time.Time) (bool, error)
	PutVerticesIfAbsent(ctx context.Context, inputs []VertexInput) (int, []string, error)
	GetVertex(ctx context.Context, key string) (*Vertex, error)
	GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error)
	DeleteVertex(ctx context.Context, key string) (bool, error)
	DeleteVertices(ctx context.Context, keys []string) (int, error)
	ScanVertices(ctx context.Context, prefix string, opts ...ScanOption) (vertices []*Vertex, nextCursor []byte, err error)
	ScanVertexKeys(ctx context.Context, prefix string, opts ...ScanOption) (keys []string, nextCursor []byte, err error)
	SearchVertices(ctx context.Context, query string, opts ...SearchOption) (hits []SearchHit, err error)
	CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error)
	DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error)
	AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (float32, error)
	AddEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (float32, error)
	AddEdges(ctx context.Context, inputs []EdgeInput) ([]float32, error)
	PutEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error
	PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) error
	PutEdges(ctx context.Context, inputs []EdgeInput) error
	GetEdge(ctx context.Context, tail, head string) (*Edge, error)
	GetEdges(ctx context.Context, refs []EdgeRef) (found []*Edge, missing []EdgeRef, err error)
	ScanEdges(ctx context.Context, opts ...EdgeScanOption) (edges []*Edge, nextCursor []byte, err error)
	DeleteEdgesByPrefix(ctx context.Context, opts ...DeleteEdgesByPrefixOption) (uint64, error)
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
	// Resolve the retry policy and idempotent-adds setting once, at the
	// failover level: the ring walk is the sole retry driver, so every
	// per-endpoint *Lantern client is built with its own unary retry
	// neutralised (clearNodeRetry appended last) to avoid nested MaxAttempts²
	// backoff. Reading them off a throwaway options mirrors how NewLantern
	// applies opts.
	var probe options
	for _, apply := range opts {
		apply(&probe)
	}
	nodeOpts := make([]Option, 0, len(opts)+1)
	nodeOpts = append(nodeOpts, opts...)
	nodeOpts = append(nodeOpts, clearNodeRetry)

	nodes := make([]failoverNode, 0, len(addrs))
	for _, addr := range addrs {
		l, err := NewLantern(addr, nodeOpts...)
		if err != nil {
			for _, n := range nodes {
				_ = n.Close()
			}
			return nil, err
		}
		nodes = append(nodes, l)
	}
	return &Failover{nodes: nodes, retry: probe.retry, idempotentAdds: probe.idempotentAdds}, nil
}

// clearNodeRetry is an internal Option that NewLanternFailover appends to
// every per-endpoint client's option list so the constructed *Lantern nodes
// never run their own unary retry loop. The failover-level RetryPolicy held
// on *Failover is the single retry driver — without this reset a WithRetry
// passed to NewLanternFailover would arm BOTH levels, multiplying the attempt
// budget (MaxAttempts per node × the ring walk).
func clearNodeRetry(o *options) { o.retry = nil }

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

// call wraps try in the retry policy when one is armed (WithRetry) and the
// method is retry-eligible — retryableMethod consults the code-enforced
// methodRetryClasses matrix and folds in idempotentAdds for the additive
// write surface. Each retry attempt re-runs the whole ring walk, resuming
// from the cursor the previous attempt advanced on ErrUnavailable, so
// retries spread across replicas with no second rotation mechanism (#849);
// backoff sleeps honour ctx. When no policy is armed or the method is not
// eligible, call is a straight pass-through to try, so zero-config failover
// clients behave exactly as before.
func (f *Failover) call(ctx context.Context, method string, fn func(failoverNode) error) error {
	if f.retry == nil || !retryableMethod(method, f.idempotentAdds) {
		return f.try(fn)
	}
	return f.retry.run(ctx, func() error { return f.try(fn) })
}

// PutVertex forwards to the current endpoint's PutVertex, failing over on
// ErrUnavailable.
func (f *Failover) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return f.call(ctx, "PutVertex", func(l failoverNode) error { return l.PutVertex(ctx, key, value, ttl) })
}

// PutVertexAt forwards to the current endpoint's PutVertexAt, failing over
// on ErrUnavailable.
func (f *Failover) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error {
	return f.call(ctx, "PutVertexAt", func(l failoverNode) error { return l.PutVertexAt(ctx, key, value, expiration) })
}

// PutVertices forwards to the current endpoint's PutVertices, failing over
// on ErrUnavailable.
func (f *Failover) PutVertices(ctx context.Context, inputs []VertexInput) error {
	return f.call(ctx, "PutVertices", func(l failoverNode) error { return l.PutVertices(ctx, inputs) })
}

// PutVertexIfAbsent forwards to the current endpoint's PutVertexIfAbsent,
// failing over on ErrUnavailable. A retry after a partial success reports
// written=false because the vertex is already present — the value is stored
// either way, matching SET NX semantics over an unreliable transport.
func (f *Failover) PutVertexIfAbsent(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	var written bool
	err := f.call(ctx, "PutVertexIfAbsent", func(l failoverNode) error {
		w, e := l.PutVertexIfAbsent(ctx, key, value, ttl)
		written = w
		return e
	})
	return written, err
}

// PutVertexIfAbsentAt forwards to the current endpoint's PutVertexIfAbsentAt,
// failing over on ErrUnavailable.
func (f *Failover) PutVertexIfAbsentAt(ctx context.Context, key string, value any, expiration time.Time) (bool, error) {
	var written bool
	err := f.call(ctx, "PutVertexIfAbsentAt", func(l failoverNode) error {
		w, e := l.PutVertexIfAbsentAt(ctx, key, value, expiration)
		written = w
		return e
	})
	return written, err
}

// PutVerticesIfAbsent forwards to the current endpoint's PutVerticesIfAbsent,
// failing over on ErrUnavailable.
func (f *Failover) PutVerticesIfAbsent(ctx context.Context, inputs []VertexInput) (int, []string, error) {
	var written int
	var skipped []string
	err := f.call(ctx, "PutVerticesIfAbsent", func(l failoverNode) error {
		w, s, e := l.PutVerticesIfAbsent(ctx, inputs)
		written, skipped = w, s
		return e
	})
	return written, skipped, err
}

// GetVertex forwards to the current endpoint's GetVertex, failing over on
// ErrUnavailable.
func (f *Failover) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	var out *Vertex
	err := f.call(ctx, "GetVertex", func(l failoverNode) error {
		v, e := l.GetVertex(ctx, key)
		out = v
		return e
	})
	return out, err
}

// GetVertices forwards to the current endpoint's GetVertices, failing over
// on ErrUnavailable.
func (f *Failover) GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error) {
	e := f.call(ctx, "GetVertices", func(l failoverNode) error {
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
	err := f.call(ctx, "DeleteVertex", func(l failoverNode) error {
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
	err := f.call(ctx, "DeleteVertices", func(l failoverNode) error {
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
	e := f.call(ctx, "ScanVertices", func(l failoverNode) error {
		var ie error
		vertices, nextCursor, ie = l.ScanVertices(ctx, prefix, opts...)
		return ie
	})
	return vertices, nextCursor, e
}

// ScanVertexKeys forwards to the current endpoint's ScanVertexKeys, failing
// over on ErrUnavailable. Like ScanVertices, the keys-only cursor is a radix
// position over the shared replicated keyspace, so a mid-scan rotation
// resumes correctly against any replica.
func (f *Failover) ScanVertexKeys(ctx context.Context, prefix string, opts ...ScanOption) (keys []string, nextCursor []byte, err error) {
	e := f.call(ctx, "ScanVertexKeys", func(l failoverNode) error {
		var ie error
		keys, nextCursor, ie = l.ScanVertexKeys(ctx, prefix, opts...)
		return ie
	})
	return keys, nextCursor, e
}

// SearchVertices forwards to the current endpoint's SearchVertices, failing
// over on ErrUnavailable. Search runs over the shared replicated keyspace, so
// a mid-call rotation simply re-runs the query against the next replica.
func (f *Failover) SearchVertices(ctx context.Context, query string, opts ...SearchOption) (hits []SearchHit, err error) {
	e := f.call(ctx, "SearchVertices", func(l failoverNode) error {
		var ie error
		hits, ie = l.SearchVertices(ctx, query, opts...)
		return ie
	})
	return hits, e
}

// CountVerticesByPrefix forwards to the current endpoint's
// CountVerticesByPrefix, failing over on ErrUnavailable.
func (f *Failover) CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error) {
	var count uint64
	err := f.call(ctx, "CountVerticesByPrefix", func(l failoverNode) error {
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
	err := f.call(ctx, "DeleteVerticesByPrefix", func(l failoverNode) error {
		d, e := l.DeleteVerticesByPrefix(ctx, prefix, opts...)
		deleted = d
		return e
	})
	return deleted, err
}

// AddEdge forwards to the current endpoint's AddEdge, failing over on
// ErrUnavailable. Because an Unavailable result means the dead node
// committed nothing, the additive contribution is retried on a sibling
// replica without risk of double-counting. It returns the post-accumulation
// effective weight reported by the serving node (#897).
func (f *Failover) AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (float32, error) {
	var effective float32
	err := f.call(ctx, "AddEdge", func(l failoverNode) error {
		w, e := l.AddEdge(ctx, tail, head, weight, ttl)
		effective = w
		return e
	})
	return effective, err
}

// AddEdgeAt forwards to the current endpoint's AddEdgeAt, failing over on
// ErrUnavailable. It returns the post-accumulation effective weight (#897).
func (f *Failover) AddEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (float32, error) {
	var effective float32
	err := f.call(ctx, "AddEdgeAt", func(l failoverNode) error {
		w, e := l.AddEdgeAt(ctx, tail, head, weight, expiration)
		effective = w
		return e
	})
	return effective, err
}

// AddEdges forwards to the current endpoint's AddEdges, failing over on
// ErrUnavailable. It returns the per-edge post-accumulation effective
// weights (#897), index-aligned with inputs.
func (f *Failover) AddEdges(ctx context.Context, inputs []EdgeInput) ([]float32, error) {
	var effective []float32
	err := f.call(ctx, "AddEdges", func(l failoverNode) error {
		w, e := l.AddEdges(ctx, inputs)
		effective = w
		return e
	})
	return effective, err
}

// PutEdge forwards to the current endpoint's PutEdge, failing over on
// ErrUnavailable.
func (f *Failover) PutEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error {
	return f.call(ctx, "PutEdge", func(l failoverNode) error { return l.PutEdge(ctx, tail, head, weight, ttl) })
}

// PutEdgeAt forwards to the current endpoint's PutEdgeAt, failing over on
// ErrUnavailable.
func (f *Failover) PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) error {
	return f.call(ctx, "PutEdgeAt", func(l failoverNode) error { return l.PutEdgeAt(ctx, tail, head, weight, expiration) })
}

// PutEdges forwards to the current endpoint's PutEdges, failing over on
// ErrUnavailable.
func (f *Failover) PutEdges(ctx context.Context, inputs []EdgeInput) error {
	return f.call(ctx, "PutEdges", func(l failoverNode) error { return l.PutEdges(ctx, inputs) })
}

// GetEdge forwards to the current endpoint's GetEdge, failing over on
// ErrUnavailable.
func (f *Failover) GetEdge(ctx context.Context, tail, head string) (*Edge, error) {
	var out *Edge
	err := f.call(ctx, "GetEdge", func(l failoverNode) error {
		e, ie := l.GetEdge(ctx, tail, head)
		out = e
		return ie
	})
	return out, err
}

// GetEdges forwards to the current endpoint's GetEdges, failing over on
// ErrUnavailable.
func (f *Failover) GetEdges(ctx context.Context, refs []EdgeRef) (found []*Edge, missing []EdgeRef, err error) {
	e := f.call(ctx, "GetEdges", func(l failoverNode) error {
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
	e := f.call(ctx, "ScanEdges", func(l failoverNode) error {
		var ie error
		edges, nextCursor, ie = l.ScanEdges(ctx, opts...)
		return ie
	})
	return edges, nextCursor, e
}

// DeleteEdgesByPrefix forwards to the current endpoint's
// DeleteEdgesByPrefix, failing over on ErrUnavailable.
func (f *Failover) DeleteEdgesByPrefix(ctx context.Context, opts ...DeleteEdgesByPrefixOption) (uint64, error) {
	var deleted uint64
	err := f.call(ctx, "DeleteEdgesByPrefix", func(l failoverNode) error {
		d, e := l.DeleteEdgesByPrefix(ctx, opts...)
		deleted = d
		return e
	})
	return deleted, err
}

// DeleteEdge forwards to the current endpoint's DeleteEdge, failing over on
// ErrUnavailable.
func (f *Failover) DeleteEdge(ctx context.Context, tail, head string) (bool, error) {
	var existed bool
	err := f.call(ctx, "DeleteEdge", func(l failoverNode) error {
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
	err := f.call(ctx, "DeleteEdges", func(l failoverNode) error {
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
	err := f.call(ctx, "Illuminate", func(l failoverNode) error {
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
