// Package client: client.go owns the *Lantern type, its
// Connect-Go-backed constructor, the unary forwarder helper, and the
// canonical surface of the SDK (GetVertex, PutVertices, Illuminate, …).
//
// Wire transport is Connect-Go over h2c (plaintext HTTP/2) or
// TLS-backed HTTP/2. The SDK does not speak gRPC and does not depend
// on google.golang.org/grpc.
package client

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// ErrNotFound is returned by GetVertex / GetEdge when the requested key or
// edge does not exist. Use errors.Is to detect it; the underlying
// *connect.Error (with connect.CodeNotFound) is joined for callers that
// need the full detail.
var ErrNotFound = errors.New("not found")

// ErrInvalidArgument wraps connect.CodeInvalidArgument responses from the
// server (typically raised by the ValidationInterceptor for empty/oversized
// keys or batch entries above MaxBatchSize). Use errors.Is to branch on
// caller-fixable input errors vs. transient failures.
var ErrInvalidArgument = errors.New("invalid argument")

// ErrResourceExhausted wraps connect.CodeResourceExhausted responses
// (rate limiter, server-side back-pressure). Callers that see this
// should back off before retrying.
var ErrResourceExhausted = errors.New("resource exhausted")

// ErrUnavailable wraps connect.CodeUnavailable responses — the
// "this node could not be reached / is not serving" signal. connect-go
// returns CodeUnavailable both for transport dial failures (connection
// refused) and for server-side UNAVAILABLE replies. Use errors.Is to
// detect it; it is the signal the failover wrapper keys on to rotate to a
// healthy replica (#592). Unlike the other sentinels it is transient — a
// retry against a sibling replica may succeed.
var ErrUnavailable = errors.New("unavailable")

// ErrFailedPrecondition wraps connect.CodeFailedPrecondition responses — the
// "the server is not in a state to serve this call" signal. Its canonical
// source is SearchVertices against a server started without the search index
// (LANTERN_SEARCH_ENABLED=false): the call cannot succeed until an operator
// enables the index, so it is a configuration state, not a transient failure.
// Use errors.Is to present a calm "search is turned off" state rather than
// retrying.
var ErrFailedPrecondition = errors.New("failed precondition")

// ErrUnauthenticated wraps connect.CodeUnauthenticated responses — the
// server has LANTERN_AUTH_TOKENS armed and the request carried no (or a
// stale) bearer token (#850). Configure the client with WithAuthToken.
var ErrUnauthenticated = errors.New("unauthenticated")

// Edge re-exports the generated protobuf Edge type so SDK callers do not
// need to import the pb package directly. It is a true Go type alias, not a
// parallel struct: client.Edge and pb.Edge are the same type, freely
// interchangeable without conversion.
//
// Use EdgeExpiration to read the Expiration field as a Go time.Time.
type Edge = pb.Edge

// EdgeExpiration returns the absolute expiration carried by e, or the zero
// time if no expiration was set on the server response.
func EdgeExpiration(e *Edge) time.Time {
	if e == nil || e.Expiration == nil {
		return time.Time{}
	}
	return e.Expiration.AsTime()
}

// Reduction re-exports the server-side post-traversal tree-view enum so SDK
// callers do not need to import the generated proto package directly. Per
// the #846 oneof redesign, MST/SPT are a knob of the BFS family
// (BFSOpts.Reduction) — not sibling algorithms — and Personalized PageRank
// is selected by passing WithPPR instead.
type Reduction = pb.Reduction

const (
	ReductionNone                = pb.Reduction_REDUCTION_UNSPECIFIED
	ReductionMinimumSpanningTree = pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE
	ReductionShortestPathTree    = pb.Reduction_REDUCTION_SHORTEST_PATH_TREE
)

// Objective is the direction of the weight-sensitive optimisation. It
// governs both the per-hop top-k pruning and the post-traversal reduction
// (#560): MINIMIZE treats edge weights as costs (keeps the k smallest-weight
// edges per hop, smallest tree wins), MAXIMIZE treats them as relevance
// (keeps the k largest-weight edges per hop, largest tree wins). Server
// resolves ObjectiveUnspecified to ObjectiveMaximize.
type Objective = pb.Objective

const (
	ObjectiveUnspecified = pb.Objective_OBJECTIVE_UNSPECIFIED
	ObjectiveMinimize    = pb.Objective_OBJECTIVE_MINIMIZE
	ObjectiveMaximize    = pb.Objective_OBJECTIVE_MAXIMIZE
)

// Weighting is the edge-weight transform applied BEFORE the BFS walk.
// RAW uses the stored edge.weight verbatim; TFIDF re-scores using TF-IDF
// over the per-vertex out-edge distribution; BM25 re-scores using Okapi
// BM25 (k1=1.2, b=0.75) over the same distribution, adding IDF saturation
// and out-degree length-normalisation on top of TF-IDF. Server resolves
// WeightingUnspecified to WeightingRaw.
type Weighting = pb.Weighting

const (
	WeightingUnspecified = pb.Weighting_WEIGHTING_UNSPECIFIED
	WeightingRaw         = pb.Weighting_WEIGHTING_RAW
	WeightingTFIDF       = pb.Weighting_WEIGHTING_TFIDF
	WeightingBM25        = pb.Weighting_WEIGHTING_BM25
)

// VertexInput describes one vertex for the batch PutVertices API.
type VertexInput struct {
	Key        string
	Value      any
	Expiration time.Time
}

// EdgeInput describes one edge for the batch AddEdges / PutEdges APIs.
type EdgeInput struct {
	Tail       string
	Head       string
	Weight     float32
	Expiration time.Time
}

// Lantern is a Connect-Go-backed client for the Lantern graph service.
// Construct one via NewLantern; share it across goroutines.
type Lantern struct {
	client     graphv1connect.LanternServiceClient
	opts       options
	httpClient *http.Client
	baseURL    string

	// nonce + callSeq generate per-call ContribID idempotency keys when
	// WithIdempotentAdds is set (#588). nonce is a per-client 128-bit random
	// prefix that namespaces this client's keys; callSeq is bumped once per
	// Add* request build. See nextContribIDs.
	nonce   [16]byte
	callSeq atomic.Uint64
}

// NewLantern dials the Lantern server at baseURL and returns a
// connected client. baseURL must include the scheme: "http://host:port"
// for h2c (matches the Lantern primary listener default) or
// "https://host[:port]" for TLS. A trailing slash is stripped
// defensively; an empty string or schemeless input is rejected.
//
// Override the default h2c http.Client via WithHTTPClient — for TLS
// supply &http.Client{Transport: &http2.Transport{TLSClientConfig: cfg}}.
func NewLantern(baseURL string, opts ...Option) (*Lantern, error) {
	if baseURL == "" {
		return nil, errors.New("client: NewLantern requires a base URL with scheme")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("client: NewLantern baseURL must start with http:// or https://; got %q", baseURL)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	o := options{batchChunkSize: defaultBatchChunkSize}
	for _, apply := range opts {
		apply(&o)
	}
	if o.httpClient == nil {
		o.httpClient = defaultH2CClient()
	}

	l := &Lantern{
		client:     graphv1connect.NewLanternServiceClient(o.httpClient, baseURL, o.clientOptions...),
		opts:       o,
		httpClient: o.httpClient,
		baseURL:    baseURL,
	}
	// A per-client random nonce namespaces this client's ContribIDs so two
	// processes (or two NewLantern calls) never collide their idempotency
	// keys. Only consulted when WithIdempotentAdds is set, but seeded
	// unconditionally so the field is always valid.
	if _, err := rand.Read(l.nonce[:]); err != nil {
		return nil, fmt.Errorf("client: NewLantern could not seed idempotency nonce: %w", err)
	}
	return l, nil
}

// Close releases any idle HTTP/2 connections the SDK is holding. The
// underlying http.Client manages its own connection pool, so Close is
// best-effort and noop-safe to call multiple times.
func (l *Lantern) Close() error {
	if l.httpClient != nil {
		l.httpClient.CloseIdleConnections()
	}
	return nil
}

// applyTimeout returns ctx with the default timeout applied when the caller
// did not already set a deadline. The returned cancel is always safe to call
// (no-op when no timeout was applied).
func (l *Lantern) applyTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if l.opts.defaultTimeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, l.opts.defaultTimeout)
}

// unary is the forwarding helper every *Lantern RPC method uses. It boxes
// req via connect.NewRequest, runs the typed Connect-Go method, lifts
// errors through wrapConnectErr so the SDK sentinels (ErrNotFound, etc.)
// match, and returns the unwrapped response.
//
// When WithRetry is armed (#849) and the request is retry-eligible
// (requestRetryable — the code-enforced idempotency matrix), the call is
// driven through the policy's bounded backoff loop. The request message is
// built once and re-sent verbatim, so a retried AddEdges carries the SAME
// ContribIDs on every attempt — the exactly-once property
// WithIdempotentAdds provides.
//
// Callers are responsible for applyTimeout — unary leaves ctx alone so
// batch helpers can drive a single outer deadline across multiple
// chunks.
func unary[Req, Resp any](
	ctx context.Context,
	l *Lantern,
	req *Req,
	fn func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error),
) (*Resp, error) {
	do := func() (*Resp, error) {
		resp, err := fn(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, wrapConnectErr(err)
		}
		return resp.Msg, nil
	}
	if l == nil || l.opts.retry == nil || !requestRetryable(req) {
		return do()
	}
	var out *Resp
	err := l.opts.retry.run(ctx, func() error {
		r, e := do()
		if e == nil {
			out = r
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// wrapConnectErr lifts a *connect.Error into a joined error that
// satisfies errors.Is against the matching SDK sentinel (ErrNotFound /
// ErrInvalidArgument / ErrResourceExhausted / ErrUnavailable /
// ErrFailedPrecondition) while preserving the original *connect.Error for
// callers that need connect.CodeOf or the per-error metadata. Non-Connect
// errors pass through unchanged.
func wrapConnectErr(err error) error {
	if err == nil {
		return nil
	}
	switch connect.CodeOf(err) {
	case connect.CodeNotFound:
		return errors.Join(ErrNotFound, err)
	case connect.CodeInvalidArgument:
		return errors.Join(ErrInvalidArgument, err)
	case connect.CodeResourceExhausted:
		return errors.Join(ErrResourceExhausted, err)
	case connect.CodeUnavailable:
		return errors.Join(ErrUnavailable, err)
	case connect.CodeFailedPrecondition:
		return errors.Join(ErrFailedPrecondition, err)
	case connect.CodeUnauthenticated:
		return errors.Join(ErrUnauthenticated, err)
	}
	return err
}

// GetVertex fetches the vertex at key.
//
// Presence vs. nil-value semantics:
//   - Absent key  → returns (nil, err) where errors.Is(err, ErrNotFound) is true.
//   - Present key holding an explicit nil value (the proto Vertex_Nil tombstone,
//     written by passing a Go nil to PutVertex/PutVertexAt) → returns a
//     non-nil *Vertex whose Kind() reports VertexKindNil and a nil error.
//
// Callers that need to distinguish "missing" from "present-but-nil" must
// check errors.Is(err, ErrNotFound) rather than only the returned pointer.
func (l *Lantern) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.GetVertexRequest{Key: key}, l.client.GetVertex)
	if err != nil {
		return nil, err
	}
	return resp.Vertex, nil
}

// expirationFromTTL converts a relative TTL into the absolute expiration
// the wire carries. A non-positive ttl means "no expiration" (permanent):
// it yields the zero time.Time, which serialises to the wire's permanent
// sentinel and is stored by the server as never-expiring (see #523 and
// core/cache.IsLiveAt). Decay is therefore strictly opt-in via a positive
// ttl. This keeps the relative-TTL convenience methods consistent with the
// absolute *At variants, which already treat a zero expiration as permanent.
func expirationFromTTL(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

// PutVertex upserts a single vertex with a relative TTL. A non-positive ttl
// stores the vertex permanently (no decay); see expirationFromTTL.
func (l *Lantern) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return l.PutVertexAt(ctx, key, value, expirationFromTTL(ttl))
}

// PutVertexAt upserts a single vertex with an absolute expiration time.
func (l *Lantern) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error {
	v, err := nativeVertex{key: key, value: value, expiration: expiration}.asVertex()
	if err != nil {
		return err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err = unary(ctx, l, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}, l.client.PutVertices)
	return err
}

// PutVertices upserts a batch of vertices. Large batches are automatically
// chunked according to WithBatchChunkSize (default 1000).
//
// Partial-write semantics: chunks are sent sequentially. If chunk N fails,
// the entries from chunks 0..N-1 are already committed server-side. The
// returned error is a *BatchError whose Written field records the number of
// successfully committed inputs, so callers can resume with
// inputs[err.Written:]. errors.Is / errors.As continue to work against the
// wrapped SDK sentinel.
func (l *Lantern) PutVertices(ctx context.Context, inputs []VertexInput) error {
	if len(inputs) == 0 {
		return nil
	}
	vs := make([]*pb.Vertex, 0, len(inputs))
	for _, in := range inputs {
		v, err := nativeVertex{key: in.Key, value: in.Value, expiration: in.Expiration}.asVertex()
		if err != nil {
			return err
		}
		vs = append(vs, v)
	}
	_, err := runBatchWrite(ctx, l, vs, func(ctx context.Context, chunk []*pb.Vertex) (int32, error) {
		_, err := unary(ctx, l, &pb.PutVerticesRequest{Vertices: chunk}, l.client.PutVertices)
		return 0, err
	})
	return err
}

// DeleteVertex removes the vertex identified by key. The returned
// bool reports whether the vertex existed (and was therefore removed
// by this call).
func (l *Lantern) DeleteVertex(ctx context.Context, key string) (bool, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.DeleteVertexRequest{Key: key}, l.client.DeleteVertex)
	if err != nil {
		return false, err
	}
	return resp.GetExisted(), nil
}

// DeleteVertices removes a batch of vertices. Automatically chunked to
// respect the server's MaxBatchSize cap. Edges incident to removed vertices
// are reaped lazily by the server's GC loop.
//
// Returns the number of keys that actually existed and were therefore
// removed (summed across chunks). Keys absent at call time are silently
// skipped — they do not appear in the count and do not produce errors.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// keys already deleted, so callers can resume with keys[err.Written:].
func (l *Lantern) DeleteVertices(ctx context.Context, keys []string) (int, error) {
	return runBatchWrite(ctx, l, keys, func(ctx context.Context, chunk []string) (int32, error) {
		resp, err := unary(ctx, l, &pb.DeleteVerticesRequest{Keys: chunk}, l.client.DeleteVertices)
		if err != nil {
			return 0, err
		}
		return resp.GetDeleted(), nil
	})
}

// GetVertices reads a batch of vertices in one (or a few chunked) round
// trips. Vertices present at call time are returned in found; keys that did
// not exist (or had expired) are returned in missing. Order is unspecified;
// callers should match by Vertex.Key. Automatically chunked to respect the
// server's MaxBatchSize cap.
func (l *Lantern) GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error) {
	err = runBatchRead(ctx, l, keys, func(ctx context.Context, chunk []string) error {
		resp, rerr := unary(ctx, l, &pb.GetVerticesRequest{Keys: chunk}, l.client.GetVertices)
		if rerr != nil {
			return rerr
		}
		found = append(found, resp.GetVertices()...)
		missing = append(missing, resp.GetMissing()...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return found, missing, nil
}

// GetEdge fetches the edge weight (and any expiration) between tail and head.
func (l *Lantern) GetEdge(ctx context.Context, tail string, head string) (*Edge, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.GetEdgeRequest{Tail: tail, Head: head}, l.client.GetEdge)
	if err != nil {
		return nil, err
	}
	return resp.Edge, nil
}

// AddEdge accumulates weight onto the (tail, head) pair: repeated calls with
// the same endpoints sum their weights. A non-positive ttl stores the edge
// permanently (no decay); see expirationFromTTL.
//
// It returns the edge's post-accumulation effective weight (#897): the live
// weight sum after this contribution was folded in, so an additive write can
// read back its own running total without a follow-up GetEdge. This is a
// serving-node local view — under active replication a peer may hold
// contributions not yet streamed in — so treat it as a fast local estimate,
// not a cluster-wide total.
//
// When the client was built WithIdempotentAdds, a transport-level retry of a
// single AddEdge/AddEdgeAt call records the weight once (the SDK attaches a
// stable per-call idempotency key); calling AddEdge twice yourself still
// sums, as documented above. On a dedup no-op the returned weight is the
// current live sum.
func (l *Lantern) AddEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) (float32, error) {
	return l.AddEdgeAt(ctx, tail, head, weight, expirationFromTTL(ttl))
}

// AddEdgeAt is AddEdge with an absolute expiration.
func (l *Lantern) AddEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) (float32, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.AddEdgesRequest{
		Edges:      []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
		ContribIds: l.nextContribIDs(1),
	}, l.client.AddEdges)
	if err != nil {
		return 0, err
	}
	if ew := resp.GetEffectiveWeights(); len(ew) > 0 {
		return ew[0], nil
	}
	return 0, nil
}

// AddEdges accumulates weight onto a batch of edges. Automatically chunked.
//
// It returns the per-edge post-accumulation effective weights (#897),
// index-aligned with inputs — the live weight sum of each edge after its
// contribution was applied. As with AddEdge this is a serving-node local
// view. On error the slice is nil; use the *BatchError's Written field to
// resume (see below).
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// edges already committed, so callers can resume with inputs[err.Written:].
// Note that because AddEdge is additive (not idempotent), naively retrying
// from index 0 will double-count weight for the already-committed prefix.
//
// With WithIdempotentAdds the SDK stamps each contribution with a per-call
// idempotency key, so a transport-level retry of a chunk records its weight
// exactly once. That key is regenerated on each AddEdges invocation, so it
// does not make an application-level resume/retry from index 0 idempotent —
// use err.Written to resume, as above.
func (l *Lantern) AddEdges(ctx context.Context, inputs []EdgeInput) ([]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	effective := make([]float32, 0, len(inputs))
	_, err := runBatchWrite(ctx, l, edgesFrom(inputs), func(ctx context.Context, chunk []*pb.Edge) (int32, error) {
		resp, err := unary(ctx, l, &pb.AddEdgesRequest{Edges: chunk, ContribIds: l.nextContribIDs(len(chunk))}, l.client.AddEdges)
		if err != nil {
			return 0, err
		}
		// effective_weights is index-aligned per chunk; appending in chunk
		// order keeps the aggregate aligned with inputs.
		effective = append(effective, resp.GetEffectiveWeights()...)
		return 0, nil
	})
	if err != nil {
		return nil, err
	}
	return effective, nil
}

// nextContribIDs returns n per-edge idempotency keys for one Add* request,
// or nil when WithIdempotentAdds is not set (nil → the wire carries no
// contrib_ids, i.e. the legacy additive path). It bumps callSeq exactly once
// per call; key i packs the client nonce into bytes [0:16] and the
// big-endian (seq<<16)|i into bytes [16:24], matching the server's ContribID
// layout. Because the keys are baked into the request when it is built, a
// transport retry that re-sends the same request re-sends the same keys, so
// the contribution is recorded exactly once.
func (l *Lantern) nextContribIDs(n int) [][]byte {
	if !l.opts.idempotentAdds || n <= 0 {
		return nil
	}
	seq := l.callSeq.Add(1)
	ids := make([][]byte, n)
	for i := 0; i < n; i++ {
		id := make([]byte, 24)
		copy(id[:16], l.nonce[:])
		binary.BigEndian.PutUint64(id[16:], (seq<<16)|uint64(uint16(i)))
		ids[i] = id
	}
	return ids
}

// PutEdge overwrites the (tail, head) pair. A non-positive ttl stores the
// edge permanently (no decay); see expirationFromTTL.
func (l *Lantern) PutEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.PutEdgeAt(ctx, tail, head, weight, expirationFromTTL(ttl))
}

// PutEdgeAt is PutEdge with an absolute expiration.
func (l *Lantern) PutEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := unary(ctx, l, &pb.PutEdgesRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	}, l.client.PutEdges)
	return err
}

// PutEdges overwrites a batch of edges. Automatically chunked.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// edges already overwritten, so callers can resume with inputs[err.Written:].
// Unlike AddEdges, PutEdges is idempotent, so a full retry from index 0 is
// also safe (it will overwrite the already-committed prefix).
func (l *Lantern) PutEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	_, err := runBatchWrite(ctx, l, edgesFrom(inputs), func(ctx context.Context, chunk []*pb.Edge) (int32, error) {
		_, err := unary(ctx, l, &pb.PutEdgesRequest{Edges: chunk}, l.client.PutEdges)
		return 0, err
	})
	return err
}

// DeleteEdge removes the (tail, head) edge. The returned bool reports
// whether the edge existed (and was therefore removed by this call).
func (l *Lantern) DeleteEdge(ctx context.Context, tail string, head string) (bool, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.DeleteEdgeRequest{Tail: tail, Head: head}, l.client.DeleteEdge)
	if err != nil {
		return false, err
	}
	return resp.GetExisted(), nil
}

// EdgeRef identifies an edge by its (tail, head) pair without weight.
// Used by DeleteEdges.
type EdgeRef struct {
	Tail string
	Head string
}

// DeleteEdges removes a batch of edges. Automatically chunked.
//
// Returns the number of edges that actually existed and were therefore
// removed (summed across chunks). Edges absent at call time are silently
// skipped — they do not appear in the count and do not produce errors.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// edges already deleted, so callers can resume with refs[err.Written:].
func (l *Lantern) DeleteEdges(ctx context.Context, refs []EdgeRef) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	keys := make([]*pb.EdgeKey, 0, len(refs))
	for _, r := range refs {
		keys = append(keys, &pb.EdgeKey{Tail: r.Tail, Head: r.Head})
	}
	return runBatchWrite(ctx, l, keys, func(ctx context.Context, chunk []*pb.EdgeKey) (int32, error) {
		resp, err := unary(ctx, l, &pb.DeleteEdgesRequest{Edges: chunk}, l.client.DeleteEdges)
		if err != nil {
			return 0, err
		}
		return resp.GetDeleted(), nil
	})
}

// GetEdges reads a batch of edges in one (or a few chunked) round trips.
// Pairs present at call time are returned in found; pairs that did not exist
// (or had expired) are returned in missing. Order is unspecified; callers
// should match by (Edge.Tail, Edge.Head). Automatically chunked to respect
// the server's MaxBatchSize cap.
func (l *Lantern) GetEdges(ctx context.Context, refs []EdgeRef) (found []*Edge, missing []EdgeRef, err error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	keys := make([]*pb.EdgeKey, 0, len(refs))
	for _, r := range refs {
		keys = append(keys, &pb.EdgeKey{Tail: r.Tail, Head: r.Head})
	}
	err = runBatchRead(ctx, l, keys, func(ctx context.Context, chunk []*pb.EdgeKey) error {
		resp, rerr := unary(ctx, l, &pb.GetEdgesRequest{Edges: chunk}, l.client.GetEdges)
		if rerr != nil {
			return rerr
		}
		found = append(found, resp.GetEdges()...)
		for _, m := range resp.GetMissing() {
			missing = append(missing, EdgeRef{Tail: m.GetTail(), Head: m.GetHead()})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return found, missing, nil
}

// Graph is the SDK-native representation of an Illuminate response. It mirrors
// the field layout (and JSON shape) of core/graph.Graph[string, *Vertex] so
// callers that need richer graph algorithms can adapt it trivially, but the
// SDK itself stays free of any non-proto dependency.
type Graph struct {
	Vertices map[string]*Vertex            `json:"vertices,omitempty"`
	Edges    map[string]map[string]float32 `json:"edges,omitempty"`
}

// NewGraph returns an empty Graph with both maps initialized.
func NewGraph() *Graph {
	return &Graph{
		Vertices: make(map[string]*Vertex),
		Edges:    make(map[string]map[string]float32),
	}
}

// IlluminateOption configures a single Illuminate call. Select the traversal
// family with at most one of WithBFS / WithPPR (the last one passed wins,
// mirroring the wire oneof; omitting both runs BFS with server defaults —
// the bare illuminate), and combine it with the shared axes WithWeighting /
// WithVertexPrefix. Per-family knobs live on BFSOpts / PPROpts, so a knob
// that another family would ignore is not expressible (#846).
type IlluminateOption func(*illuminateConfig)

type illuminateConfig struct {
	weighting    Weighting
	vertexPrefix string
	bfs          *BFSOpts
	ppr          *PPROpts
	community    *LocalCommunityOpts
}

// BFSOpts tunes the greedy per-hop top-k BFS walk and its optional
// post-traversal tree reduction. The zero value is the server-default walk.
type BFSOpts struct {
	// Step is the BFS depth. 0 = server default.
	Step uint32
	// FanOut is the per-hop top-k prune: at each hop only the FanOut
	// strongest (or cheapest, under ObjectiveMinimize) edges survive.
	// 0 = server default. (Formerly the overloaded "k".)
	FanOut uint32
	// Objective governs both the per-hop pruning and the Reduction
	// direction (#560). Unspecified = ObjectiveMaximize.
	Objective Objective
	// Reduction optionally reduces the discovered neighbourhood to a tree
	// rooted at the seed (ReductionMinimumSpanningTree /
	// ReductionShortestPathTree). ReductionNone = raw subgraph.
	Reduction Reduction
}

// PPROpts runs seed-anchored Personalized PageRank (#801) instead of the
// BFS walk, returning a relevance star (seed→v carries v's PPR mass). PPR
// is intrinsically a relevance maximiser with no per-hop step semantics,
// which is why neither knob exists here.
type PPROpts struct {
	// TopN caps the star to the top-N vertices by mass. 0 = every
	// positive-mass vertex. (Formerly the overloaded "k".)
	TopN uint32
	// RestartProb is the teleport-to-seed probability α — the locality
	// knob: higher α (≈0.5) yields a tighter, seed-proximate set, lower α
	// (≈0.15) a broader one. Honoured only in (0,1); 0/unset or
	// out-of-range falls back to the server default (0.15).
	RestartProb float32
	// Epsilon is the forward-push residual threshold ε. Smaller ε pushes
	// mass to more vertices (higher recall, more work); larger ε stops
	// sooner (sparser star, faster). Honoured only when > 0; 0/unset falls
	// back to the server default (1e-4).
	Epsilon float32
}

// LocalCommunityOpts extracts the conductance-optimal local community
// around the seed (#845): PageRank-Nibble — the PPR forward-push followed
// by a sweep cut. Unlike WithPPR's relevance star, the response preserves
// structure: it is the induced subgraph on the selected members, with
// actual stored edge weights and expirations.
type LocalCommunityOpts struct {
	// MaxSize is an UPPER BOUND on community size — not an exact count.
	// The sweep stops at the conductance minimum, which may come earlier.
	// 0 = unbounded (the sweep alone decides).
	MaxSize uint32
	// RestartProb is the locality knob α, same semantics/defaults as
	// PPROpts.RestartProb.
	RestartProb float32
	// Epsilon is the push accuracy/work budget ε, same semantics/defaults
	// as PPROpts.Epsilon.
	Epsilon float32
	// Reduction optionally renders a tree VIEW of the community rooted at
	// the seed. Members unreachable from the seed within the community are
	// returned as isolated vertices (membership preserved). ReductionNone
	// (default) returns the full induced subgraph.
	Reduction Reduction
	// Objective sets the direction/cost mapping for the Reduction only;
	// ignored when Reduction is ReductionNone.
	Objective Objective
}

// WithBFS selects the BFS traversal family with the supplied knobs.
// Mutually exclusive with the other family options (last option wins).
func WithBFS(o BFSOpts) IlluminateOption {
	return func(c *illuminateConfig) { c.bfs, c.ppr, c.community = &o, nil, nil }
}

// WithPPR selects the Personalized PageRank traversal family with the
// supplied knobs. Mutually exclusive with the other family options (last
// option wins).
func WithPPR(o PPROpts) IlluminateOption {
	return func(c *illuminateConfig) { c.ppr, c.bfs, c.community = &o, nil, nil }
}

// WithLocalCommunity selects the local community extraction family (#845)
// with the supplied knobs. Mutually exclusive with the other family options
// (last option wins).
func WithLocalCommunity(o LocalCommunityOpts) IlluminateOption {
	return func(c *illuminateConfig) { c.community, c.bfs, c.ppr = &o, nil, nil }
}

// WithWeighting toggles the edge-weight transform applied BEFORE the
// walk (any family). WeightingRaw (the default) uses edge.weight verbatim;
// WeightingTFIDF re-scores edges using TF-IDF over the per-vertex
// out-edge distribution; WeightingBM25 re-scores using Okapi BM25
// (k1=1.2, b=0.75), adding IDF saturation and out-degree length-
// normalisation on top of TF-IDF.
func WithWeighting(w Weighting) IlluminateOption {
	return func(c *illuminateConfig) { c.weighting = w }
}

// WithVertexPrefix restricts the Illuminate traversal frontier to vertices
// whose key has the given prefix (any family). The seed is always retained
// as the anchor even if it does not match. Empty (the default) = no filter.
// The filter is applied server-side BEFORE per-hop top-k and before any
// reduction: the result is the prefix-induced subgraph. Note that
// WithVertexPrefix together with an MST/SPT reduction yields a tree over
// that induced subgraph, NOT a true shortest path in the full graph — a
// matching vertex reachable only via a non-matching bridge vertex is
// excluded.
func WithVertexPrefix(prefix string) IlluminateOption {
	return func(c *illuminateConfig) { c.vertexPrefix = prefix }
}

// Illuminate cuts the neighbourhood subgraph around seed. Select the
// traversal family with WithBFS / WithPPR (omitting both runs BFS with
// server defaults — the bare illuminate) and the shared axes with
// WithWeighting / WithVertexPrefix. See #846 for the per-family design and
// #801 for the Personalized PageRank semantics.
func (l *Lantern) Illuminate(ctx context.Context, seed string, opts ...IlluminateOption) (*Graph, error) {
	var cfg illuminateConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	req := &pb.IlluminateRequest{
		Seed:         seed,
		Weighting:    cfg.weighting,
		VertexPrefix: cfg.vertexPrefix,
	}
	switch {
	case cfg.community != nil:
		req.Params = &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{
			MaxSize:     cfg.community.MaxSize,
			RestartProb: cfg.community.RestartProb,
			Epsilon:     cfg.community.Epsilon,
			Reduction:   cfg.community.Reduction,
			Objective:   cfg.community.Objective,
		}}
	case cfg.ppr != nil:
		req.Params = &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{
			TopN:        cfg.ppr.TopN,
			RestartProb: cfg.ppr.RestartProb,
			Epsilon:     cfg.ppr.Epsilon,
		}}
	case cfg.bfs != nil:
		req.Params = &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{
			Step:      cfg.bfs.Step,
			FanOut:    cfg.bfs.FanOut,
			Objective: cfg.bfs.Objective,
			Reduction: cfg.bfs.Reduction,
		}}
	}
	resp, err := unary(ctx, l, req, l.client.Illuminate)
	if err != nil {
		return nil, err
	}
	g := NewGraph()
	for _, v := range resp.Graph.Vertices {
		g.Vertices[v.Key] = v
	}
	for _, e := range resp.Graph.Edges {
		if _, ok := g.Edges[e.Tail]; !ok {
			g.Edges[e.Tail] = make(map[string]float32)
		}
		g.Edges[e.Tail][e.Head] = e.Weight
	}
	return g, nil
}

func edgesFrom(inputs []EdgeInput) []*pb.Edge {
	edges := make([]*pb.Edge, 0, len(inputs))
	for _, in := range inputs {
		edges = append(edges, &pb.Edge{
			Tail:       in.Tail,
			Head:       in.Head,
			Weight:     in.Weight,
			Expiration: timestamppb.New(in.Expiration),
		})
	}
	return edges
}

func chunkSlice[T any](s []T, size int) [][]T {
	if size <= 0 {
		size = defaultBatchChunkSize
	}
	if len(s) <= size {
		return [][]T{s}
	}
	out := make([][]T, 0, (len(s)+size-1)/size)
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}
