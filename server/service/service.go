package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ServiceName is the fully-qualified gRPC service name used for per-service
// health reporting (grpc.health.v1) and as a logical metric label.
const ServiceName = "graph.v1.LanternService"

// LanternService implements pb.LanternServiceServer.
//
// Per-call logging, metrics, recovery, tracing and validation are wired up
// via interceptors in server/provider so handlers stay focused on business
// logic. Handlers honor the incoming context — short paths rely on gRPC to
// propagate cancellation; the early ctx.Err() checks let us return a clean
// Canceled / DeadlineExceeded status when a client already gave up.
//
// The service depends on the narrow Backend interface (see backend.go); a
// wire binding maps it to *graph.GraphCache in production. Tests can supply
// a fake without standing up the real cache.
type LanternService struct {
	pb.UnimplementedLanternServiceServer
	cache                  Backend
	scan                   ScanLimits
	log                    *mutationlog.Log
	clock                  *hlc.Clock
	origin                 []byte
	onAppend               func()
	logger                 *slog.Logger
	tombstoneTTL           time.Duration
	origins                *originStateTracker
	onApplied              func(origin string)
	onReplicationApply     func(op string)
	onValidationReject     func(reason string)
	onTombstoneClampReject func()
	metrics                HotPathMetrics

	// statusInfo + startedAt + startedAtOnce back GetServerStatus
	// (#314). Populated by WithStatusInfo / MarkStarted from the
	// composition root; default zero values keep tests that construct
	// LanternService directly compatible (the RPC still returns a
	// well-formed response, just with empty fields).
	statusInfo    StatusInfo
	startedAt     time.Time
	startedAtOnce sync.Once

	// replicationSnapshotter + replicationStatusInfo back
	// GetReplicationStatus (#315). Both nil/zero by default so tests
	// constructing LanternService directly get a well-formed
	// "single-instance, no peers" response.
	replicationSnapshotter ReplicationSnapshotter
	replicationStatusInfo  ReplicationStatusInfo
}

// HotPathMetrics is the narrow observability surface consumed by the
// hot-path handlers (#220). Implemented by *server/metrics.DomainMetrics in
// production; tests can install a fake to assert per-RPC instrumentation
// fires.
//
// Implementations must be safe for concurrent use.
type HotPathMetrics interface {
	OnIlluminate(optimization string, visitedVertices, visitedEdges int, traversal, optimize time.Duration)
	OnScan(op string, results int, duration time.Duration)
	OnBatch(op string, size int)
}

type noopHotPathMetrics struct{}

func (noopHotPathMetrics) OnIlluminate(string, int, int, time.Duration, time.Duration) {}
func (noopHotPathMetrics) OnScan(string, int, time.Duration)                           {}
func (noopHotPathMetrics) OnBatch(string, int)                                         {}

// ScanLimits caps the per-call pagination knobs for the prefix RPCs. It is
// a value struct rather than a pointer-to-provider type so the service
// stays test-instantiable without the wire graph; production callers get
// it from provider.ScanConfig via the adapter constructor below.
type ScanLimits struct {
	ScanDefaultLimit           uint32
	ScanMaxLimit               uint32
	DeleteByPrefixDefaultLimit uint32
	DeleteByPrefixMaxLimit     uint32
}

func defaultScanLimits() ScanLimits {
	return ScanLimits{
		ScanDefaultLimit:           1000,
		ScanMaxLimit:               10000,
		DeleteByPrefixDefaultLimit: 10000,
		DeleteByPrefixMaxLimit:     100000,
	}
}

func NewLanternService(cache Backend) *LanternService {
	return &LanternService{cache: cache, scan: defaultScanLimits(), origins: newOriginStateTracker(), metrics: noopHotPathMetrics{}}
}

// WithScanLimits returns s with its prefix-RPC limits replaced. Intended
// for the wire provider in package main to thread provider.ScanConfig into
// the service without forcing every test caller to construct the struct.
func (s *LanternService) WithScanLimits(l ScanLimits) *LanternService {
	s.scan = l
	return s
}

// WithReplication attaches the mutation log, hybrid logical clock, and an
// optional append-success callback (for metric counting). When log or clock
// is nil the service silently skips logging, which keeps existing tests
// (and the singular forwarders, which delegate to plurals) working without
// modification.
//
// origin is captured from the clock's NodeID so every appended Mutation
// carries the local node's identity.
func (s *LanternService) WithReplication(log *mutationlog.Log, clock *hlc.Clock, onAppend func()) *LanternService {
	s.log = log
	s.clock = clock
	if clock != nil {
		id := clock.NodeID()
		s.origin = id[:]
	}
	s.onAppend = onAppend
	return s
}

// WithLogger replaces the slog handle used for replication-side warnings
// (e.g. WAL write failures). Defaults to slog.Default() when unset.
func (s *LanternService) WithLogger(l *slog.Logger) *LanternService {
	s.logger = l
	return s
}

// WithHotPathMetrics installs the hot-path observability sink (#220).
// Defaults to a no-op so unit tests can construct the service without
// pulling in the metrics package.
func (s *LanternService) WithHotPathMetrics(m HotPathMetrics) *LanternService {
	if m == nil {
		s.metrics = noopHotPathMetrics{}
	} else {
		s.metrics = m
	}
	return s
}

// WithTombstoneTTL configures the maximum retention window for delete
// tombstones AND the upper bound on caller-supplied Expiration on the
// Add*/Put* RPCs (#183). When d <= 0 the clamp is disabled (legacy
// behaviour) and Delete handlers fall back to the non-HLC backend path.
// Wired from LANTERN_TOMBSTONE_TTL via provider.ReplicationConfig.
func (s *LanternService) WithTombstoneTTL(d time.Duration) *LanternService {
	s.tombstoneTTL = d
	return s
}

// WithAppliedHook registers a callback invoked after every successful
// remote-mutation apply (ApplyMutation path). The hook receives the
// lowercase-hex encoding of the originating HLC NodeID and is used by
// provider/metrics to bump lantern_replication_applied_total{origin}.
// A nil hook (the default) disables the callback. The hook MUST be
// non-blocking; the apply path holds no locks but synchronously waits
// for the callback to return.
func (s *LanternService) WithAppliedHook(f func(origin string)) *LanternService {
	s.onApplied = f
	return s
}

// WithReplicationApplyHook registers a callback invoked after every
// successful ApplyMutation, naming the MutationOp oneof variant
// (e.g. "PutVertex", "AddEdges"). Used by provider/metrics to bump
// lantern_replication_apply_total{op}. A nil hook disables the callback.
// The hook MUST be non-blocking.
func (s *LanternService) WithReplicationApplyHook(f func(op string)) *LanternService {
	s.onReplicationApply = f
	return s
}

// WithValidationRejectHook registers a callback invoked once per
// rejected request, naming the canonical reason (one of
// metrics.validationRejectReasons). Used by provider/metrics to bump
// lantern_validation_rejected_total{reason}. A nil hook disables the
// callback. The hook MUST be non-blocking — it runs synchronously on
// the RPC critical path. Called from validateExpiration (#183) and the
// prefix-scan cursor decode (server/service/prefix.go).
func (s *LanternService) WithValidationRejectHook(f func(reason string)) *LanternService {
	s.onValidationReject = f
	return s
}

// WithTombstoneClampRejectHook registers a callback invoked once per
// ApplyMutation Put/Add operation that the tombstone-aware HLC path
// drops because the incoming HLC lost the LWW comparison (against a
// live tombstone OR against a strictly-newer existing entry). Used by
// provider/metrics to bump lantern_tombstone_clamp_rejected_total.
// A nil hook disables the callback. Only fires inside the s.tombstoneTTL > 0
// branch of apply.go — the non-HLC fast path never rejects.
func (s *LanternService) WithTombstoneClampRejectHook(f func()) *LanternService {
	s.onTombstoneClampReject = f
	return s
}

// validateExpiration enforces the LANTERN_TOMBSTONE_TTL clamp on
// caller-supplied per-entry expirations. A zero expiration (the proto
// default — "no expiration") is always accepted; otherwise the
// expiration must not exceed now + tombstoneTTL, which is the longest
// window any tombstone could shadow a late replay. The error message
// names the env var so operators see the knob they need to adjust.
func (s *LanternService) validateExpiration(exp time.Time) error {
	if s.tombstoneTTL <= 0 || exp.IsZero() {
		return nil
	}
	if exp.After(time.Now().Add(s.tombstoneTTL)) {
		if s.onValidationReject != nil {
			s.onValidationReject("bad_ttl")
		}
		return status.Errorf(codes.InvalidArgument,
			"expiration %s exceeds LANTERN_TOMBSTONE_TTL=%s",
			exp.UTC().Format(time.RFC3339Nano), s.tombstoneTTL)
	}
	return nil
}

// tombstoneExpiration returns the wall-clock instant a tombstone stamped
// now would expire, or the zero time when the clamp is disabled.
func (s *LanternService) tombstoneExpiration() time.Time {
	if s.tombstoneTTL <= 0 {
		return time.Time{}
	}
	return time.Now().Add(s.tombstoneTTL)
}

// OriginStates returns a snapshot of the per-origin (last_seq, hlc)
// watermark map maintained by logMutation and ApplyMutation. Used by
// the PeerStatus RPC (#186). Returns nil when replication is unwired
// (the tracker is nil in test paths that construct LanternService
// directly without going through NewLanternService).
func (s *LanternService) OriginStates() []OriginState {
	if s.origins == nil {
		return nil
	}
	return s.origins.States()
}

// LocalSeq returns the highest per-origin seq the local node has
// recorded for the given origin (0 when the origin has never been
// seen, or when the tracker is unwired). Used by the anti-entropy
// driver (#186) to compute its catch-up start seq.
func (s *LanternService) LocalSeq(origin hlc.NodeID) uint64 {
	if s.origins == nil {
		return 0
	}
	return s.origins.LocalSeq(origin)
}

// OriginStatesCount returns the number of distinct origins currently
// recorded in the per-origin watermark table, or 0 when the tracker is
// unwired. Used by provider/metrics to sample lantern_origin_states_count.
func (s *LanternService) OriginStatesCount() int {
	if s.origins == nil {
		return 0
	}
	return s.origins.OriginCount()
}

// logMutation appends op to the mutation log after a local commit. The HLC
// is stamped here so the seq->hlc ordering on the log is monotone with
// commit order. Failures are logged but not surfaced to clients — the
// local write has already succeeded and replication is best-effort within
// the bounded ring buffer.
//
// Callers MUST construct op as a fully populated MutationOp (one oneof
// case set). Returns immediately when the log is not wired (test path).
func (s *LanternService) logMutation(op *pb.MutationOp) {
	if s.log == nil || s.clock == nil {
		return
	}
	ts := s.clock.Now()
	mu := &pb.Mutation{
		Hlc: &pb.HLCTimestamp{
			WallNs:  ts.WallNs,
			Logical: ts.Logical,
			NodeId:  append([]byte(nil), ts.NodeID[:]...),
		},
		Origin: append([]byte(nil), s.origin...),
		Op:     op,
	}
	entry, err := s.log.Append(mu, ts)
	if err != nil {
		l := s.logger
		if l == nil {
			l = slog.Default()
		}
		l.Warn("mutation log append failed", slog.Any("err", err))
		return
	}
	if s.origins != nil {
		s.origins.Record(ts.NodeID, entry.Seq, ts)
	}
	if s.onAppend != nil {
		s.onAppend()
	}
}

// Illuminate returns a subgraph rooted at the seed, optionally optimized into
// a spanning or shortest-path tree.
func (s *LanternService) Illuminate(ctx context.Context, request *pb.IlluminateRequest) (*pb.IlluminateResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	traversalStart := time.Now()
	g, expirations, err := s.cache.NeighborWithExpirationsContext(ctx, request.GetSeed(), int(request.GetStep()), int(request.GetK()), request.GetTfidf())
	traversalDur := time.Since(traversalStart)
	if err != nil {
		return nil, status.FromContextError(err).Err()
	}

	var optimizeDur time.Duration
	if opt := optimizers[request.GetOptimization()]; opt != nil {
		optStart := time.Now()
		g, err = opt(ctx, g, request.GetSeed())
		optimizeDur = time.Since(optStart)
		if err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	vertices := make([]*pb.Vertex, 0, len(g.Vertices))
	for k, v := range g.Vertices {
		if v == nil {
			vertices = append(vertices, &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			vertices = append(vertices, v)
		}
	}

	var edges []*pb.Edge
	edgeCount := 0
	for tail, heads := range g.Edges {
		expRow := expirations[tail]
		for head, weight := range heads {
			edge := &pb.Edge{Tail: tail, Head: head, Weight: weight}
			if exp, ok := expRow[head]; ok && !exp.IsZero() {
				edge.Expiration = timestamppb.New(exp)
			}
			edges = append(edges, edge)
			edgeCount++
		}
	}

	s.metrics.OnIlluminate(optimizationLabel(request.GetOptimization()), len(vertices), edgeCount, traversalDur, optimizeDur)

	return &pb.IlluminateResponse{
		Graph: &pb.Graph{Vertices: vertices, Edges: edges},
	}, nil
}

func (s *LanternService) GetVertex(ctx context.Context, request *pb.GetVertexRequest) (*pb.GetVertexResponse, error) {
	resp, err := s.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{request.GetKey()}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetMissing()) == 1 {
		return nil, status.Errorf(codes.NotFound, "vertex %q not found", request.GetKey())
	}
	return &pb.GetVertexResponse{Vertex: resp.GetVertices()[0]}, nil
}

// GetVertices reads several vertices in one round trip. Keys present at call
// time are returned in Vertices; missing (or expired) keys are reported in
// Missing. Order within either slice is not guaranteed to follow request
// order — clients should match by Vertex.key.
func (s *LanternService) GetVertices(ctx context.Context, request *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	keys := request.GetKeys()
	s.metrics.OnBatch("GetVertices", len(keys))
	resp := &pb.GetVerticesResponse{
		Vertices: make([]*pb.Vertex, 0, len(keys)),
	}
	for _, k := range keys {
		v, ok := s.cache.GetVertex(k)
		if !ok {
			resp.Missing = append(resp.Missing, k)
			continue
		}
		if v == nil {
			resp.Vertices = append(resp.Vertices, &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			resp.Vertices = append(resp.Vertices, v)
		}
	}
	return resp, nil
}

func (s *LanternService) PutVertex(ctx context.Context, request *pb.PutVertexRequest) (*pb.PutVertexResponse, error) {
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{request.GetVertex()}}); err != nil {
		return nil, err
	}
	return &pb.PutVertexResponse{}, nil
}

func (s *LanternService) PutVertices(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetVertices()
	s.metrics.OnBatch("PutVertices", len(in))
	items := make([]graph.VertexItem[string, *pb.Vertex], 0, len(in))
	for _, v := range in {
		if err := s.validateExpiration(v.GetExpiration().AsTime()); err != nil {
			return nil, err
		}
		items = append(items, graph.VertexItem[string, *pb.Vertex]{
			Key:        v.GetKey(),
			Value:      v,
			Expiration: v.GetExpiration().AsTime(),
		})
	}
	s.cache.PutVerticesWithExpiration(items)
	s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: request}})
	return &pb.PutVerticesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) DeleteVertex(ctx context.Context, in *pb.DeleteVertexRequest) (*pb.DeleteVertexResponse, error) {
	// Per the proto contract, deleting a vertex leaves its edges orphaned;
	// the periodic GC loop reaps any tf/df rows whose endpoints disappear.
	resp, err := s.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{in.GetKey()}})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteVertexResponse{Existed: resp.GetDeleted() == 1}, nil
}

func (s *LanternService) DeleteVertices(ctx context.Context, in *pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	s.metrics.OnBatch("DeleteVertices", len(in.GetKeys()))
	var n int
	if s.clock != nil && s.tombstoneTTL > 0 {
		// Replicated path: stamp tombstones at clock.Now() so peers
		// resolve LWW deterministically; local expiration is best-effort.
		n = s.cache.DeleteVerticesHLC(in.GetKeys(), s.clock.Now(), s.tombstoneExpiration())
	} else {
		n = s.cache.DeleteVertices(in.GetKeys())
	}
	s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: in}})
	return &pb.DeleteVerticesResponse{Deleted: int32(n)}, nil
}

func (s *LanternService) GetEdge(ctx context.Context, request *pb.GetEdgeRequest) (*pb.GetEdgeResponse, error) {
	resp, err := s.GetEdges(ctx, &pb.GetEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: request.GetTail(), Head: request.GetHead()}},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.GetMissing()) == 1 {
		return nil, status.Errorf(codes.NotFound, "edge %q -> %q not found", request.GetTail(), request.GetHead())
	}
	return &pb.GetEdgeResponse{Edge: resp.GetEdges()[0]}, nil
}

// GetEdges reads several edges in one round trip. Pairs present at call time
// are returned in Edges; missing (or expired) pairs are reported in Missing.
// Order within either slice is not guaranteed to follow request order.
func (s *LanternService) GetEdges(ctx context.Context, request *pb.GetEdgesRequest) (*pb.GetEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetEdges()
	s.metrics.OnBatch("GetEdges", len(in))
	resp := &pb.GetEdgesResponse{
		Edges: make([]*pb.Edge, 0, len(in)),
	}
	for _, k := range in {
		w, exp, ok := s.cache.GetEdgeDetail(k.GetTail(), k.GetHead())
		if !ok {
			resp.Missing = append(resp.Missing, &pb.EdgeKey{Tail: k.GetTail(), Head: k.GetHead()})
			continue
		}
		edge := &pb.Edge{Tail: k.GetTail(), Head: k.GetHead(), Weight: w}
		if !exp.IsZero() {
			edge.Expiration = timestamppb.New(exp)
		}
		resp.Edges = append(resp.Edges, edge)
	}
	return resp, nil
}

func (s *LanternService) AddEdge(ctx context.Context, request *pb.AddEdgeRequest) (*pb.AddEdgeResponse, error) {
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{request.GetEdge()}}); err != nil {
		return nil, err
	}
	return &pb.AddEdgeResponse{}, nil
}

func (s *LanternService) AddEdges(ctx context.Context, request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetEdges()
	s.metrics.OnBatch("AddEdges", len(in))
	items := make([]graph.EdgeItem[string], 0, len(in))
	for _, e := range in {
		if err := s.validateExpiration(e.GetExpiration().AsTime()); err != nil {
			return nil, err
		}
		items = append(items, graph.EdgeItem[string]{
			Tail:       e.GetTail(),
			Head:       e.GetHead(),
			Weight:     e.GetWeight(),
			Expiration: e.GetExpiration().AsTime(),
		})
	}
	s.cache.AddEdgesWithExpiration(items)
	s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: request}})
	return &pb.AddEdgesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) PutEdge(ctx context.Context, request *pb.PutEdgeRequest) (*pb.PutEdgeResponse, error) {
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{request.GetEdge()}}); err != nil {
		return nil, err
	}
	return &pb.PutEdgeResponse{}, nil
}

func (s *LanternService) PutEdges(ctx context.Context, request *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetEdges()
	s.metrics.OnBatch("PutEdges", len(in))
	items := make([]graph.EdgeItem[string], 0, len(in))
	for _, e := range in {
		if err := s.validateExpiration(e.GetExpiration().AsTime()); err != nil {
			return nil, err
		}
		items = append(items, graph.EdgeItem[string]{
			Tail:       e.GetTail(),
			Head:       e.GetHead(),
			Weight:     e.GetWeight(),
			Expiration: e.GetExpiration().AsTime(),
		})
	}
	// PutEdgesWithExpiration takes the cache write lock once for the whole
	// batch, so concurrent GetEdge readers never observe a transient
	// NotFound between the per-edge delete and add.
	s.cache.PutEdgesWithExpiration(items)
	s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: request}})
	return &pb.PutEdgesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) DeleteEdge(ctx context.Context, in *pb.DeleteEdgeRequest) (*pb.DeleteEdgeResponse, error) {
	resp, err := s.DeleteEdges(ctx, &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: in.GetTail(), Head: in.GetHead()}},
	})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteEdgeResponse{Existed: resp.GetDeleted() == 1}, nil
}

func (s *LanternService) DeleteEdges(ctx context.Context, in *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	inEdges := in.GetEdges()
	s.metrics.OnBatch("DeleteEdges", len(inEdges))
	keys := make([]graph.EdgeKey[string], 0, len(inEdges))
	for _, e := range inEdges {
		keys = append(keys, graph.EdgeKey[string]{Tail: e.GetTail(), Head: e.GetHead()})
	}
	var n int
	if s.clock != nil && s.tombstoneTTL > 0 {
		n = s.cache.DeleteEdgesHLC(keys, s.clock.Now(), s.tombstoneExpiration())
	} else {
		n = s.cache.DeleteEdges(keys)
	}
	s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: in}})
	return &pb.DeleteEdgesResponse{Deleted: int32(n)}, nil
}

// LanternServer ties the gRPC server, its listener, the cache GC loop, and
