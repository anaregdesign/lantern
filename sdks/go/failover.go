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
// at-least-once window of a mid-flight Unavailable retry is dedup-safe:
// Failover mints the per-edge contrib ids ONCE per logical call (from its
// own failover-level generator) and reuses those exact bytes across every
// retry attempt AND every node it rotates through. Minting per attempt —
// or per node — would re-count, because the replicas converge via
// replication, so a re-mint through node B lands as a distinct contribution
// just like a re-mint through node A (#916).
package client

import (
	"context"
	"errors"
	"iter"
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
	clock func() time.Time

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
	// contribIDs is the single failover-level ContribID generator. Non-nil
	// only when idempotentAdds is set. Minting ids here — once per logical
	// AddEdge/AddEdges call, before the retry loop — and reusing them across
	// every attempt and every node is what makes a mid-flight Unavailable
	// retry dedup-safe; a fresh id per attempt would double-count (#916).
	contribIDs *contribIDGen
}

// failoverNode is the unexported endpoint contract the ring walk delegates
// to. *Lantern satisfies it (compile assertion below); the white-box tests
// substitute a fake to exercise the rotation logic without dialing a
// server. It mirrors the full unary surface of *Lantern that Failover
// re-exports.
type failoverNode interface {
	PutVertex(ctx context.Context, key string, value any, ttl time.Duration) (PutOutcome, error)
	PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error)
	PutVertices(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error)
	PutVertexIfAbsent(ctx context.Context, key string, value any, ttl time.Duration) (PutOutcome, error)
	PutVertexIfAbsentAt(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error)
	PutVerticesIfAbsent(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error)
	GetVertex(ctx context.Context, key string) (*Vertex, error)
	GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error)
	DeleteVertex(ctx context.Context, key string) (bool, error)
	DeleteVertices(ctx context.Context, keys []string) (int, error)
	ScanVertices(ctx context.Context, prefix string, opts ...ScanOption) (vertices []*Vertex, nextCursor []byte, err error)
	ScanVertexKeys(ctx context.Context, prefix string, opts ...ScanOption) (keys []string, nextCursor []byte, err error)
	SearchVertices(ctx context.Context, query string, opts ...SearchOption) (hits []SearchHit, err error)
	SearchVerticesPage(ctx context.Context, query string, opts ...SearchOption) (SearchPage, error)
	CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error)
	DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error)
	AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (float32, error)
	AddEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (float32, error)
	AddEdges(ctx context.Context, inputs []EdgeInput) ([]float32, error)
	// addEdgeAtWithIDs / addEdgesWithIDs are the id-accepting seams the
	// failover ring drives so it can mint contrib ids once per logical call
	// and reuse them across retry attempts (#916). Unexported: they are an
	// internal contract between *Failover and *Lantern, not client API.
	addEdgeAtWithIDs(ctx context.Context, tail, head string, weight float32, expiration time.Time, ids [][]byte) (float32, error)
	addEdgesWithIDs(ctx context.Context, inputs []EdgeInput, ids [][]byte) ([]float32, error)
	PutEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (PutOutcome, error)
	PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (PutOutcome, error)
	PutEdges(ctx context.Context, inputs []EdgeInput) ([]EdgePutResult, error)
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
	// Seed a single failover-level ContribID generator when idempotent adds
	// are enabled, so every AddEdge/AddEdges call mints its ids once and
	// reuses them across retries and node switches (#916). Done before
	// dialing so a seed failure leaks no connections.
	var contribIDs *contribIDGen
	if probe.idempotentAdds {
		g, err := newContribIDGen()
		if err != nil {
			return nil, err
		}
		contribIDs = g
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
	return &Failover{nodes: nodes, retry: probe.retry, idempotentAdds: probe.idempotentAdds, contribIDs: contribIDs, clock: time.Now}, nil
}

func (f *Failover) now() time.Time {
	if f != nil && f.clock != nil {
		return f.clock()
	}
	return time.Now()
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

// callCurrent retries only the currently selected endpoint. Search-session
// cursors are endpoint-sticky; rotating them would turn a transport outage
// into a misleading cursor-invalid response from another process.
func (f *Failover) callCurrent(ctx context.Context, method string, fn func(failoverNode) error) error {
	idx := int(f.cur.Load() % uint64(len(f.nodes)))
	run := func() error { return fn(f.nodes[idx]) }
	if f.retry == nil || !retryableMethod(method, f.idempotentAdds) {
		return run()
	}
	return f.retry.run(ctx, run)
}

// PutVertex resolves ttl once, then forwards the same absolute expiration to
// each endpoint's PutVertexAt while failing over on ErrUnavailable.
func (f *Failover) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) (PutOutcome, error) {
	observedAt := f.now()
	expiration := expirationFromTTLAt(ttl, observedAt)
	return f.putVertexAt(ctx, "PutVertex", key, value, expiration, initiallyLiveAt(expiration, observedAt))
}

func (f *Failover) putVertexAt(ctx context.Context, method, key string, value any, expiration time.Time, initiallyLive bool) (PutOutcome, error) {
	var outcome PutOutcome
	err := f.call(ctx, method, func(l failoverNode) error {
		var err error
		outcome, err = l.PutVertexAt(ctx, key, value, expiration)
		return err
	})
	return clientBoundedPutOutcome(outcome, initiallyLive, expiration, f.now()), err
}

// PutVertexAt forwards to the current endpoint's PutVertexAt, failing over
// on ErrUnavailable.
func (f *Failover) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error) {
	return f.putVertexAt(ctx, "PutVertexAt", key, value, expiration, initiallyLiveAt(expiration, f.now()))
}

// PutVertices forwards to the current endpoint's PutVertices, failing over
// on ErrUnavailable.
func (f *Failover) PutVertices(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error) {
	initiallyLive := vertexInitialLiveness(inputs, f.now())
	var results []VertexPutResult
	err := f.call(ctx, "PutVertices", func(l failoverNode) error {
		var err error
		results, err = l.PutVertices(ctx, inputs)
		return err
	})
	return clientBoundedVertexPutResults(results, inputs[:len(results)], initiallyLive[:len(results)], f.now()), err
}

// PutVertexIfAbsent resolves ttl once, then forwards the absolute expiration to
// the current endpoint's PutVertexIfAbsentAt without automatic failover. A
// response can be lost after the condition was evaluated and the write
// committed; replaying on another replica could return a different
// authoritative outcome, so ambiguous transport failure is exposed to the
// caller instead.
func (f *Failover) PutVertexIfAbsent(ctx context.Context, key string, value any, ttl time.Duration) (PutOutcome, error) {
	observedAt := f.now()
	expiration := expirationFromTTLAt(ttl, observedAt)
	return f.putVertexIfAbsentAt(ctx, "PutVertexIfAbsent", key, value, expiration, initiallyLiveAt(expiration, observedAt))
}

func (f *Failover) putVertexIfAbsentAt(ctx context.Context, method, key string, value any, expiration time.Time, initiallyLive bool) (PutOutcome, error) {
	var outcome PutOutcome
	err := f.callCurrent(ctx, method, func(l failoverNode) error {
		w, e := l.PutVertexIfAbsentAt(ctx, key, value, expiration)
		outcome = w
		return e
	})
	return clientBoundedPutOutcome(outcome, initiallyLive, expiration, f.now()), err
}

// PutVertexIfAbsentAt forwards to the current endpoint only; see
// PutVertexIfAbsent for the response-loss rationale.
func (f *Failover) PutVertexIfAbsentAt(ctx context.Context, key string, value any, expiration time.Time) (PutOutcome, error) {
	return f.putVertexIfAbsentAt(ctx, "PutVertexIfAbsentAt", key, value, expiration, initiallyLiveAt(expiration, f.now()))
}

// PutVerticesIfAbsent forwards to the current endpoint only; see
// PutVertexIfAbsent for the response-loss rationale.
func (f *Failover) PutVerticesIfAbsent(ctx context.Context, inputs []VertexInput) ([]VertexPutResult, error) {
	initiallyLive := vertexInitialLiveness(inputs, f.now())
	var results []VertexPutResult
	err := f.callCurrent(ctx, "PutVerticesIfAbsent", func(l failoverNode) error {
		var e error
		results, e = l.PutVerticesIfAbsent(ctx, inputs)
		return e
	})
	return clientBoundedVertexPutResults(results, inputs[:len(results)], initiallyLive[:len(results)], f.now()), err
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
// over on ErrUnavailable. Search is local/eventual: during replication lag or
// a partition, a rotation may change membership, BM25 scores, or result order.
// Exact cross-endpoint results require the same live graph and homogeneous
// search config; production traffic should target readiness-gated replicas.
func (f *Failover) SearchVertices(ctx context.Context, query string, opts ...SearchOption) (hits []SearchHit, err error) {
	e := f.call(ctx, "SearchVertices", func(l failoverNode) error {
		var ie error
		hits, ie = l.SearchVertices(ctx, query, opts...)
		return ie
	})
	return hits, e
}

// SearchVerticesPage starts on the failover ring, but a non-empty cursor is
// retried only against the sticky endpoint that minted its bounded session.
// ErrUnavailable is returned as-is so callers can restart explicitly from
// page one; the SDK never silently changes the snapshot.
func (f *Failover) SearchVerticesPage(ctx context.Context, query string, opts ...SearchOption) (page SearchPage, err error) {
	configured := searchOptions{}
	for _, apply := range opts {
		apply(&configured)
	}
	call := f.call
	if len(configured.cursor) > 0 {
		call = f.callCurrent
	}
	err = call(ctx, "SearchVerticesPage", func(l failoverNode) error {
		var pageErr error
		page, pageErr = l.SearchVerticesPage(ctx, query, opts...)
		return pageErr
	})
	return page, err
}

// SearchVerticesIter lazily follows SearchVerticesPage while preserving its
// endpoint stickiness and bounded-tail error contract.
func (f *Failover) SearchVerticesIter(ctx context.Context, query string, opts ...SearchOption) iter.Seq2[SearchHit, error] {
	return func(yield func(SearchHit, error) bool) {
		initial := searchOptions{}
		for _, apply := range opts {
			apply(&initial)
		}
		cursor := append([]byte(nil), initial.cursor...)
		for {
			pageOpts := append(append([]SearchOption(nil), opts...), WithSearchCursor(cursor))
			page, err := f.SearchVerticesPage(ctx, query, pageOpts...)
			if err != nil {
				var zero SearchHit
				yield(zero, err)
				return
			}
			for _, hit := range page.Hits {
				if !yield(hit, nil) {
					return
				}
			}
			if len(page.NextCursor) == 0 {
				if page.ContinuationLimited {
					var zero SearchHit
					yield(zero, ErrSearchContinuationLimited)
				}
				return
			}
			cursor = page.NextCursor
		}
	}
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
// replica; with WithIdempotentAdds the contrib ids are minted once (below,
// via AddEdgeAt) and reused across every attempt, so even the residual
// at-least-once window of a mid-flight Unavailable retry cannot double-count
// (#916). It returns the post-accumulation effective weight (#897).
func (f *Failover) AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (float32, error) {
	return f.AddEdgeAt(ctx, tail, head, weight, expirationFromTTL(ttl))
}

// AddEdgeAt forwards to the current endpoint's AddEdgeAt, failing over on
// ErrUnavailable. It mints the contrib id ONCE, before the retry loop, and
// reuses it across every attempt and node (#916). It returns the
// post-accumulation effective weight (#897).
func (f *Failover) AddEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (float32, error) {
	ids := f.nextContribIDs(1)
	var effective float32
	err := f.call(ctx, "AddEdgeAt", func(l failoverNode) error {
		w, e := l.addEdgeAtWithIDs(ctx, tail, head, weight, expiration, ids)
		effective = w
		return e
	})
	return effective, err
}

// AddEdges forwards to the current endpoint's AddEdges, failing over on
// ErrUnavailable. The per-edge contrib ids are minted ONCE for the whole
// batch, before the retry loop, and reused across every attempt and node so a
// retry cannot double-count (#916). It returns the per-edge post-accumulation
// effective weights (#897), index-aligned with inputs.
func (f *Failover) AddEdges(ctx context.Context, inputs []EdgeInput) ([]float32, error) {
	ids := f.nextContribIDs(len(inputs))
	var effective []float32
	err := f.call(ctx, "AddEdges", func(l failoverNode) error {
		w, e := l.addEdgesWithIDs(ctx, inputs, ids)
		effective = w
		return e
	})
	return effective, err
}

// AddDecayingEdge forwards the geometric decay staircase (see the *Lantern
// method of the same name) through the failover ring. The curve is expanded
// ONCE — a single base time and a single mint of the per-contribution ids,
// both captured before the retry loop — so every attempt and every node
// replays byte-identical contributions and ids and a mid-flight Unavailable
// retry cannot double-count or skew the schedule (#916). It returns the edge's
// post-add effective (live-sum) weight.
func (f *Failover) AddDecayingEdge(ctx context.Context, tail, head string, opts DecayOpts) (float32, error) {
	inputs, err := DecayContributions(tail, head, opts, time.Now())
	if err != nil {
		return 0, err
	}
	ids := f.nextContribIDs(len(inputs))
	var effective []float32
	err = f.call(ctx, "AddDecayingEdge", func(l failoverNode) error {
		w, e := l.addEdgesWithIDs(ctx, inputs, ids)
		effective = w
		return e
	})
	if err != nil {
		return 0, err
	}
	if len(effective) == 0 {
		return 0, nil
	}
	return effective[len(effective)-1], nil
}

// nextContribIDs mints n contrib ids from the failover-level generator, or
// nil when idempotent adds are disabled (contribIDs is nil) — the legacy
// non-idempotent additive path. Minting at the failover layer (not per node)
// is deliberate: both replicas converge via replication, so re-minting
// through a sibling on failover double-counts exactly like re-minting through
// the same node (#916).
func (f *Failover) nextContribIDs(n int) [][]byte {
	if f.contribIDs == nil {
		return nil
	}
	return f.contribIDs.next(n)
}

// PutEdge resolves ttl once, then forwards the same absolute expiration to
// each endpoint's PutEdgeAt while failing over on ErrUnavailable.
func (f *Failover) PutEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (PutOutcome, error) {
	observedAt := f.now()
	expiration := expirationFromTTLAt(ttl, observedAt)
	return f.putEdgeAt(ctx, "PutEdge", tail, head, weight, expiration, initiallyLiveAt(expiration, observedAt))
}

func (f *Failover) putEdgeAt(ctx context.Context, method, tail, head string, weight float32, expiration time.Time, initiallyLive bool) (PutOutcome, error) {
	var outcome PutOutcome
	err := f.call(ctx, method, func(l failoverNode) error {
		var err error
		outcome, err = l.PutEdgeAt(ctx, tail, head, weight, expiration)
		return err
	})
	return clientBoundedPutOutcome(outcome, initiallyLive, expiration, f.now()), err
}

// PutEdgeAt forwards to the current endpoint's PutEdgeAt, failing over on
// ErrUnavailable.
func (f *Failover) PutEdgeAt(ctx context.Context, tail, head string, weight float32, expiration time.Time) (PutOutcome, error) {
	return f.putEdgeAt(ctx, "PutEdgeAt", tail, head, weight, expiration, initiallyLiveAt(expiration, f.now()))
}

// PutEdges forwards to the current endpoint's PutEdges, failing over on
// ErrUnavailable.
func (f *Failover) PutEdges(ctx context.Context, inputs []EdgeInput) ([]EdgePutResult, error) {
	initiallyLive := edgeInitialLiveness(inputs, f.now())
	var results []EdgePutResult
	err := f.call(ctx, "PutEdges", func(l failoverNode) error {
		var err error
		results, err = l.PutEdges(ctx, inputs)
		return err
	})
	return clientBoundedEdgePutResults(results, inputs[:len(results)], initiallyLive[:len(results)], f.now()), err
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
