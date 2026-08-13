package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Sender is the slim, transport-agnostic surface Subscribe and
// Snapshot need from a server-streaming client connection: a single
// typed Send method. *connect.ServerStream[T] satisfies it directly.
// Tests that need to observe the streamed messages provide their own
// implementation (see replication_test.go).
type Sender[T any] interface {
	Send(*T) error
}

// causalBarrierSnapshotter is optional so lightweight test backends do not
// need to model replication-only retained state. The production GraphCache
// satisfies it. Barriers are streamed before live entries, allowing a receiver
// to retain the older floor and then apply a newer live value for the same
// identity without losing either fact.
type causalBarrierSnapshotter interface {
	SnapshotReplication() graphcache.ReplicationSnapshot[string, *pb.Vertex]
}

// ReplicationServiceName is the fully-qualified RPC service name for the
// replication surface. Used for per-service health reporting via
// grpc.health.v1.
const ReplicationServiceName = "graph.v1.LanternReplicationService"

// SubscribeMetrics is the narrow surface the replication service uses to
// publish per-stream metrics. *server/metrics.DomainMetrics satisfies it.
// Defined here so the service stays independent of provider/metrics.
type SubscribeMetrics interface {
	OnSubscribeStarted()
	OnSubscribeEnded()
	OnSubscribeDropped(reason string)
}

// nopSubscribeMetrics is the default when no metrics handle is wired (test
// path). All methods are no-ops.
type nopSubscribeMetrics struct{}

func (nopSubscribeMetrics) OnSubscribeStarted()       {}
func (nopSubscribeMetrics) OnSubscribeEnded()         {}
func (nopSubscribeMetrics) OnSubscribeDropped(string) {}

// OriginStatesProvider is the narrow read surface the PeerStatus RPC
// uses to enumerate per-origin watermarks. *LanternService satisfies
// it via OriginStates(). Defined here so the replication service can
// be constructed without a hard dependency on LanternService.
type OriginStatesProvider interface {
	OriginStates() []OriginState
}

// SearchConfigFingerprintProvider supplies the search contract carried by
// PeerStatus. *LanternService satisfies it with the same fingerprint exposed
// through GetServerStatus.
type SearchConfigFingerprintProvider interface {
	SearchConfigFingerprint() string
}

// LanternReplicationService is the in-process implementation of the
// graph.v1.LanternReplicationService surface. It exposes the
// mutation log as a resumable, back-pressured server-streaming RPC
// for peer replication and CDC consumers.
//
// The service holds a reference to the same *mutationlog.Log that
// LanternService.WithReplication wired into the write path, so
// subscribers see every successfully appended mutation in seq order.
type LanternReplicationService struct {
	log     *mutationlog.Log
	backend Backend
	clock   *hlc.Clock
	metrics SubscribeMetrics
	logger  *slog.Logger
	origins OriginStatesProvider
	search  SearchConfigFingerprintProvider
}

// NewLanternReplicationService constructs the service. log MUST be the same
// instance handed to LanternService.WithReplication; otherwise subscribers
// will not see locally-originated mutations. backend is the graph cache
// the Snapshot RPC walks; clock supplies the cutoff HLC stamped into the
// snapshot header. Both must be non-nil — Snapshot returns Unavailable
// when backend is unset, mirroring how Subscribe handles a nil log.
func NewLanternReplicationService(log *mutationlog.Log, backend Backend, clock *hlc.Clock) *LanternReplicationService {
	return &LanternReplicationService{
		log:     log,
		backend: backend,
		clock:   clock,
		metrics: nopSubscribeMetrics{},
	}
}

// WithMetrics attaches a SubscribeMetrics handle. Nil is treated as the
// no-op implementation so test wiring stays simple.
func (s *LanternReplicationService) WithMetrics(m SubscribeMetrics) *LanternReplicationService {
	if m == nil {
		s.metrics = nopSubscribeMetrics{}
	} else {
		s.metrics = m
	}
	return s
}

// WithLogger replaces the slog handle used for replication-side warnings.
// Defaults to slog.Default() when unset.
func (s *LanternReplicationService) WithLogger(l *slog.Logger) *LanternReplicationService {
	s.logger = l
	return s
}

// WithOriginStates wires the per-origin watermark provider used by the
// PeerStatus RPC (#186). Nil disables the RPC (handler returns
// Unavailable). Wired in production by passing the LanternService
// instance — its OriginStates() method satisfies the interface.
func (s *LanternReplicationService) WithOriginStates(p OriginStatesProvider) *LanternReplicationService {
	s.origins = p
	return s
}

// WithSearchConfig wires the search fingerprint published by PeerStatus.
// Nil leaves the field empty, which callers must treat as unverified rather
// than compatible.
func (s *LanternReplicationService) WithSearchConfig(p SearchConfigFingerprintProvider) *LanternReplicationService {
	s.search = p
	return s
}

// Subscribe streams every mutation log entry whose (origin, seq)
// satisfies the per-origin resume cursor in req.FromSeqPerOrigin to
// the supplied Sender, honouring ctx cancellation throughout.
//
// Under the leaderless Subscribe contract (#415), every replica's log
// carries entries from every cluster origin (each stamped with its
// writer's HLC NodeID on Entry.HLC.NodeID, which round-trips to the
// wire as Mutation.Hlc.NodeId / Mutation.Origin). A consumer attaches
// to any replica and resumes per-origin so failover between replicas
// does not require deduplication or seq remapping.
//
// Flow:
//  1. Open a log subscription at req.FromLocalSeq when a snapshot consumer
//     resumes against the same responder; otherwise start at local seq 1 so
//     ring eviction remains a detectable gap. Per-origin sequence is unrelated
//     to this replica-local position and filtering happens at this layer.
//  2. For each entry, filter by the per-origin cursor: deliver only
//     when mu.Seq >= cursor[origin]. Origins absent from the cursor
//     are delivered from the oldest retained entry — this lets a
//     consumer that has never seen origin X (e.g. X joined the
//     cluster while the consumer was offline) catch up naturally.
//     mu.Seq carries the originating writer's seq (stamped at
//     logMutation / preserved across relay), NOT the forwarding
//     replica's local log seq.
//  3. ErrGapped from the log layer (the ring was truncated below the
//     requested local position) surfaces as
//     FailedPrecondition + reason "gapped" so the client knows to
//     snapshot and resubscribe (RFC §8.2).
//  4. If the channel is closed mid-stream the subscriber fell behind
//     the per-subscriber buffer (Options.SubscriberBuffer). Surface
//     this as FailedPrecondition + "gapped" — symmetric with case 3.
//  5. Honor ctx cancellation throughout.
//
// Send errors terminate the stream and increment
// dropped{reason="send_failed"}.
func (s *LanternReplicationService) Subscribe(ctx context.Context, req *pb.SubscribeRequest, stream Sender[pb.SubscribeResponse]) error {
	if s.log == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("replication is not enabled on this server"))
	}
	cursor := req.GetFromSeqPerOrigin()
	fromLocalSeq := req.GetFromLocalSeq()
	if fromLocalSeq == 0 {
		fromLocalSeq = 1
	}
	ch, cancel, err := s.log.Subscribe(fromLocalSeq)
	if err != nil {
		if errors.Is(err, mutationlog.ErrGapped) {
			s.metrics.OnSubscribeDropped("gapped")
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("gapped: log truncated below requested local seq %d; snapshot and resubscribe", fromLocalSeq))
		}
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("subscribe failed: %w", err))
	}
	defer func() { _ = cancel() }()

	s.metrics.OnSubscribeStarted()
	defer s.metrics.OnSubscribeEnded()

	for {
		select {
		case <-ctx.Done():
			return ctxToConnect(ctx.Err())
		case entry, ok := <-ch:
			if !ok {
				// Slow subscriber: log closed our channel mid-stream.
				s.metrics.OnSubscribeDropped("gapped")
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("gapped: subscriber fell behind; snapshot and resubscribe"))
			}
			mu, ok := entry.Op.(*pb.Mutation)
			if !ok {
				l := s.loggerOrDefault()
				l.Warn("replication: unexpected mutation log entry type",
					slog.String("type", "non-Mutation payload"),
					slog.Uint64("seq", entry.Seq))
				return connect.NewError(connect.CodeInternal, fmt.Errorf(
					"replication: malformed mutation log entry at seq=%d", entry.Seq))
			}
			// Per-origin filter: skip entries whose origin watermark
			// the consumer already covers. Origins absent from the
			// cursor are NOT skipped — they are delivered from the
			// oldest retained entry, matching the contract documented
			// on pb.SubscribeRequest.
			if want, present := cursor[hex.EncodeToString(mu.GetOrigin())]; present && mu.GetSeq() < want {
				continue
			}
			// Forward the mutation as stamped at the originating
			// writer (mu.Seq = origin's seq). The relay does NOT
			// overwrite Seq with entry.Seq (this replica's local
			// log seq), because downstream consumers in the
			// leaderless Subscribe contract (#415) key on
			// (origin, origin_seq), not on the forwarding replica's
			// local seq. Forwarding entry.Seq instead would make
			// the same (origin, origin_seq) tuple appear with a
			// different Seq value on every hop, breaking the
			// per-origin dedup gate in ApplyMutation.
			if err := stream.Send(&pb.SubscribeResponse{Mutation: mu}); err != nil {
				s.metrics.OnSubscribeDropped("send_failed")
				return err
			}
		}
	}
}

func (s *LanternReplicationService) loggerOrDefault() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

// Snapshot implements pb.LanternReplicationServiceServer.
//
// Flow:
//  1. Stamp the per-origin cutoff and cutoff_hlc and send a
//     SnapshotHeader as the very first frame so receivers know how
//     to resume Subscribe. cutoff_seq_per_origin is the current
//     contents of the OriginStatesProvider (each origin's last applied
//     seq); cutoff_hlc is clock.Now(). An empty map is sent when the
//     local server has not yet applied any origin (cold cluster) or
//     when no origin state provider is wired (test path).
//  2. Materialise vertices and edges through the Backend snapshot API
//     (taken under the GraphCache write lock). Stream each as its own
//     SnapshotResponse frame, honouring stream.Context() cancellation
//     between sends.
//  3. Send a SnapshotFooter with the actually-streamed counts as the very
//     last frame so receivers can detect truncation.
//
// The implementation deliberately materialises the snapshot in memory.
// Replication bootstrap is bounded (one peer per call, infrequent), so
// the O(N+E) memory overhead is acceptable. True streaming is a follow-up
// once the snapshot path is wired end-to-end.
func (s *LanternReplicationService) Snapshot(ctx context.Context, _ *pb.SnapshotRequest, stream Sender[pb.SnapshotResponse]) error {
	if s.backend == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("snapshot is not enabled on this server"))
	}

	var cutoffPerOrigin map[string]uint64
	if s.origins != nil {
		states := s.origins.OriginStates()
		if len(states) > 0 {
			cutoffPerOrigin = make(map[string]uint64, len(states))
			for _, st := range states {
				cutoffPerOrigin[hex.EncodeToString(st.Origin[:])] = st.LastSeq
			}
		}
	}
	var cutoffHLC hlc.Timestamp
	if s.clock != nil {
		cutoffHLC = s.clock.Now()
	}
	var cutoffLocalSeq uint64
	if s.log != nil {
		cutoffLocalSeq, _ = s.log.LastSeq()
	}
	header := &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Header{
			Header: &pb.SnapshotHeader{
				CutoffSeqPerOrigin: cutoffPerOrigin,
				CutoffHlc:          hlcToProto(cutoffHLC),
				CutoffLocalSeq:     cutoffLocalSeq,
			},
		},
	}
	if err := stream.Send(header); err != nil {
		return err
	}

	var barriers graphcache.CausalBarrierSnapshot[string]
	var graph graphcache.GraphSnapshot[string, *pb.Vertex]
	if snapshotter, ok := s.backend.(causalBarrierSnapshotter); ok {
		snapshot := snapshotter.SnapshotReplication()
		barriers = snapshot.Barriers
		graph = snapshot.Graph
	} else {
		// Compatibility seam for narrow fake backends. Production GraphCache
		// always implements SnapshotReplication so causal + live state share one
		// lock pass.
		graph = graphcache.GraphSnapshot[string, *pb.Vertex]{
			Vertices: s.backend.SnapshotVertices(),
			Edges:    s.backend.SnapshotEdges(),
		}
	}
	var vertexBarrierCount uint64
	for _, barrier := range barriers.Vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_VertexCausalBarrier{
				VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{
					Key: barrier.Key,
					Hlc: hlcToProto(barrier.HLC),
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		vertexBarrierCount++
	}

	var edgeBarrierCount uint64
	for _, barrier := range barriers.Edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_EdgeCausalBarrier{
				EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{
					Tail: barrier.Tail,
					Head: barrier.Head,
					Hlc:  hlcToProto(barrier.HLC),
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		edgeBarrierCount++
	}

	var vertexCount uint64
	vertices := graph.Vertices
	for _, v := range vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_Vertex{
				Vertex: &pb.SnapshotVertex{
					Vertex: v.Value,
					Hlc:    hlcToProto(v.HLC),
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		vertexCount++
	}

	var edgeCount uint64
	edges := graph.Edges
	for _, e := range edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		contribs := make([]*pb.SnapshotEdgeContribution, 0, len(e.Contributions))
		for _, c := range e.Contributions {
			contribs = append(contribs, &pb.SnapshotEdgeContribution{
				Weight:     c.Weight,
				Expiration: timestamppb.New(c.Expiration),
				ContribId:  contribIDBytes(c.ContribID),
			})
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_Edge{
				Edge: &pb.SnapshotEdge{
					Tail:          e.Tail,
					Head:          e.Head,
					Hlc:           hlcToProto(e.HLC),
					Contributions: contribs,
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		edgeCount++
	}

	footer := &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Footer{
			Footer: &pb.SnapshotFooter{
				VertexCount:              vertexCount,
				EdgeCount:                edgeCount,
				VertexCausalBarrierCount: vertexBarrierCount,
				EdgeCausalBarrierCount:   edgeBarrierCount,
			},
		},
	}
	return stream.Send(footer)
}

// PeerStatus implements pb.LanternReplicationServiceServer.
//
// Returns the responder's per-origin (last_seq, last_hlc) map. The
// reply set always includes the local origin (sourced from the
// mutation log via WithOriginStates) plus every remote origin whose
// mutation has ever been applied via ApplyMutation since process
// start.
//
// Returns Unavailable when the origin-state provider is unwired —
// either because replication is disabled on this server (single-
// instance test path) or because WithOriginStates was never called.
func (s *LanternReplicationService) PeerStatus(ctx context.Context, _ *pb.PeerStatusRequest) (*pb.PeerStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if s.origins == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("replication is not enabled on this server"))
	}
	rows := s.origins.OriginStates()
	out := &pb.PeerStatusResponse{Origins: make([]*pb.OriginState, 0, len(rows))}
	if s.search != nil {
		out.SearchConfigFingerprint = s.search.SearchConfigFingerprint()
	}
	if s.clock != nil {
		nid := s.clock.NodeID()
		out.SelfOrigin = append([]byte(nil), nid[:]...)
	}
	for _, r := range rows {
		id := r.Origin
		out.Origins = append(out.Origins, &pb.OriginState{
			Origin:  append([]byte(nil), id[:]...),
			LastSeq: r.LastSeq,
			LastHlc: hlcToProto(r.LastHLC),
		})
	}
	return out, nil
}
