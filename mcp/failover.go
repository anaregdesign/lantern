package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// parseLanternAddrs splits a comma-separated LANTERN_ADDR value into a
// clean list of endpoint URLs. Whitespace around each entry is trimmed and
// empty entries are dropped, so " a , ,b ," yields ["a","b"]. A single
// address (the common case) returns a one-element slice, preserving the
// original single-endpoint behaviour.
func parseLanternAddrs(raw string) []string {
	parts := strings.Split(raw, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			addrs = append(addrs, s)
		}
	}
	return addrs
}

// isUnavailable reports whether err is the "this node is unreachable / not
// serving" signal that should trigger failover to another replica.
//
// connect-go surfaces connection-refused dial failures and server-side
// CodeUnavailable responses as connect.CodeUnavailable (verified
// empirically: dialling a dead port yields "unavailable: … connection
// refused"). Application-level errors carry other codes — ErrNotFound
// (CodeNotFound), ErrInvalidArgument (CodeInvalidArgument),
// ErrResourceExhausted (CodeResourceExhausted) — and must NOT trigger
// failover, because a consistent replica would answer them identically. A
// nil error and any non-Connect error (connect.CodeOf → CodeUnknown) are
// treated as non-failover.
func isUnavailable(err error) bool {
	return err != nil && connect.CodeOf(err) == connect.CodeUnavailable
}

// failoverClient wraps an ordered set of Lantern endpoints (one per HA
// replica) and transparently fails over between them. It implements the
// lanternClient interface, so the MCP tool handlers consume it exactly
// like a single *client.Lantern.
//
// Selection policy: calls are sticky to the last endpoint that answered
// (cur). On a per-call basis, the wrapper tries the current endpoint
// first; if and only if that endpoint returns an "unavailable" error
// (isUnavailable) it advances to the next endpoint and retries, walking
// the whole ring once. The first endpoint that either succeeds or returns
// an application-level error wins, becomes the new sticky current, and its
// result is returned. If every endpoint is unavailable the last
// unavailable error is returned.
//
// Correctness notes (the Lantern cluster the MCP targets is full-mesh
// replicated and consistent — see #544 — so reads against any replica are
// equivalent):
//   - Failover for the additive writes (AddEdge, AddEdges) only ever
//     triggers on connect.CodeUnavailable, which connect-go returns when
//     the request could not be delivered (e.g. connection refused) — i.e.
//     nothing was committed on the dead node — so rotating to a healthy
//     replica and retrying does not double-apply the write in the common
//     node-down case. The residual at-least-once window (a write that
//     commits on node A but whose response is lost as Unavailable) is
//     inherent to any failover and acceptable here: edge weights are
//     additive contributions and reinforce (#549) is best-effort.
//   - Scan cursors (ScanVertices/ScanEdges) are radix positions over the
//     shared, replicated keyspace, so replaying a cursor on a sibling
//     replica is well-defined while the cluster is consistent. Callers
//     that drive bounded scan loops (memory_stats, recall_related)
//     tolerate a fresh restart if a rotation ever invalidates a cursor.
type failoverClient struct {
	nodes []lanternClient
	cur   atomic.Uint64
}

// newFailoverClient dials every address in addrs and returns a
// failoverClient over the resulting endpoints. addrs must be non-empty.
// If any dial fails, every endpoint already dialled is closed and the
// error is returned, so the caller never leaks half-open clients.
func newFailoverClient(addrs []string, opts ...client.Option) (*failoverClient, error) {
	if len(addrs) == 0 {
		return nil, errors.New("mcp: newFailoverClient requires at least one address")
	}
	nodes := make([]lanternClient, 0, len(addrs))
	for _, a := range addrs {
		l, err := client.NewLantern(a, opts...)
		if err != nil {
			for _, n := range nodes {
				if c, ok := n.(interface{ Close() error }); ok {
					_ = c.Close()
				}
			}
			return nil, fmt.Errorf("dial %s: %w", a, err)
		}
		nodes = append(nodes, l)
	}
	return &failoverClient{nodes: nodes}, nil
}

// try runs fn against the endpoints in sticky order, failing over to the
// next endpoint only when the current one reports isUnavailable. It
// returns the first non-unavailable outcome (success or application error)
// and records the winning endpoint as the new sticky current; if all
// endpoints are unavailable it returns the last error.
func (f *failoverClient) try(fn func(lanternClient) error) error {
	n := len(f.nodes)
	if n == 0 {
		return errors.New("mcp: failover client has no nodes")
	}
	start := int(f.cur.Load() % uint64(n))
	var lastErr error
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		err := fn(f.nodes[idx])
		if !isUnavailable(err) {
			f.cur.Store(uint64(idx))
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (f *failoverClient) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return f.try(func(lc lanternClient) error {
		return lc.PutVertex(ctx, key, value, ttl)
	})
}

func (f *failoverClient) GetVertex(ctx context.Context, key string) (*client.Vertex, error) {
	var out *client.Vertex
	err := f.try(func(lc lanternClient) error {
		v, e := lc.GetVertex(ctx, key)
		out = v
		return e
	})
	return out, err
}

func (f *failoverClient) DeleteVertex(ctx context.Context, key string) (bool, error) {
	var out bool
	err := f.try(func(lc lanternClient) error {
		v, e := lc.DeleteVertex(ctx, key)
		out = v
		return e
	})
	return out, err
}

func (f *failoverClient) ScanVertices(ctx context.Context, prefix string, opts ...client.ScanOption) ([]*client.Vertex, []byte, error) {
	var (
		verts  []*client.Vertex
		cursor []byte
	)
	err := f.try(func(lc lanternClient) error {
		v, c, e := lc.ScanVertices(ctx, prefix, opts...)
		verts, cursor = v, c
		return e
	})
	return verts, cursor, err
}

func (f *failoverClient) CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error) {
	var out uint64
	err := f.try(func(lc lanternClient) error {
		v, e := lc.CountVerticesByPrefix(ctx, prefix)
		out = v
		return e
	})
	return out, err
}

func (f *failoverClient) DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...client.DeleteByPrefixOption) (uint64, error) {
	var out uint64
	err := f.try(func(lc lanternClient) error {
		v, e := lc.DeleteVerticesByPrefix(ctx, prefix, opts...)
		out = v
		return e
	})
	return out, err
}

func (f *failoverClient) PutVertices(ctx context.Context, inputs []client.VertexInput) error {
	return f.try(func(lc lanternClient) error {
		return lc.PutVertices(ctx, inputs)
	})
}

func (f *failoverClient) AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error {
	return f.try(func(lc lanternClient) error {
		return lc.AddEdge(ctx, tail, head, weight, ttl)
	})
}

func (f *failoverClient) AddEdges(ctx context.Context, inputs []client.EdgeInput) error {
	return f.try(func(lc lanternClient) error {
		return lc.AddEdges(ctx, inputs)
	})
}

func (f *failoverClient) GetEdge(ctx context.Context, tail, head string) (*client.Edge, error) {
	var out *client.Edge
	err := f.try(func(lc lanternClient) error {
		v, e := lc.GetEdge(ctx, tail, head)
		out = v
		return e
	})
	return out, err
}

func (f *failoverClient) ScanEdges(ctx context.Context, opts ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
	var (
		edges  []*client.Edge
		cursor []byte
	)
	err := f.try(func(lc lanternClient) error {
		es, c, e := lc.ScanEdges(ctx, opts...)
		edges, cursor = es, c
		return e
	})
	return edges, cursor, err
}

func (f *failoverClient) Illuminate(ctx context.Context, seed string, opts ...client.IlluminateOption) (*client.Graph, error) {
	var out *client.Graph
	err := f.try(func(lc lanternClient) error {
		g, e := lc.Illuminate(ctx, seed, opts...)
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
// returns the last error. This makes the MCP startup health gate pass as
// long as one replica is reachable.
func (f *failoverClient) Ping(ctx context.Context) error {
	n := len(f.nodes)
	if n == 0 {
		return errors.New("mcp: failover client has no nodes")
	}
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
// Endpoints that do not expose Close (test fakes) are skipped.
func (f *failoverClient) Close() error {
	var firstErr error
	for _, n := range f.nodes {
		if c, ok := n.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Compile-time assertion: *failoverClient satisfies lanternClient.
var _ lanternClient = (*failoverClient)(nil)
