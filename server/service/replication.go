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

// replicationSnapshotter is optional so lightweight test backends do not
// need to model replication-only retained state. The production GraphCache
// satisfies it. Barriers and active Delete tombstones stream before live
// entries, allowing a receiver to retain the older floor and then apply a
// newer live value for the same identity without losing either fact.
type replicationSnapshotter interface {
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

// snapshotCutProvider is implemented by LanternService when origin states
// and the graph share its replication commit boundary. A narrow fake origin
// provider may omit it; production always passes LanternService here.
type snapshotCutProvider interface {
	withReplicationSnapshotCut(capture func()) error
}

// subscribeCutProvider registers a log tail and captures its publication
// fault generation under the same cut as graph writes and Snapshot.
type subscribeCutProvider interface {
	withReplicationSubscribeCut(capture func(<-chan struct{})) error
}

// publicationStatusProvider exposes a local/relay log fault generation. It
// is implemented by the production LanternService; narrow test providers
// can omit it when they do not publish mutations.
type publicationStatusProvider interface {
	publicationStatus() (<-chan struct{}, bool)
}

// graphMutationFromLog projects an owned log payload onto the Subscribe wire.
// A receipt envelope must take the ReplicationMutation path before the legacy
// graph-only fallback, so a peer cannot advance an origin without receipts.
func graphMutationFromLog(op mutationlog.MutationOp) (*pb.Mutation, bool) {
	switch value := op.(type) {
	case interface{ ReplicationMutation() (*pb.Mutation, error) }:
		mutation, err := value.ReplicationMutation()
		return mutation, err == nil && mutation != nil
	case *pb.Mutation:
		return value, value != nil
	case interface{ GraphMutation() *pb.Mutation }:
		mutation := value.GraphMutation()
		return mutation, mutation != nil
	default:
		return nil, false
	}
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
//  1. Open a log subscription under the service publication cut at
//     req.FromLocalSeq when a snapshot consumer resumes against the same
//     responder; otherwise start at local seq 1 so ring eviction remains a
//     detectable gap. Per-origin sequence is unrelated to this replica-local
//     position and filtering happens at this layer.
//  2. For each entry, filter by the per-origin cursor: deliver only
//     when mu.Seq >= cursor[origin]. Origins absent from the cursor
//     are delivered from the oldest retained entry — this lets a
//     consumer that has never seen origin X (e.g. X joined the
//     cluster while the consumer was offline) catch up naturally.
//     mu.Seq carries the originating writer's seq (stamped at local
//     publication / preserved across relay), NOT the forwarding
//     replica's local log seq.
//  3. ErrGapped from the log layer (the ring was truncated below the
//     requested local position) surfaces as
//     FailedPrecondition + reason "gapped" so the client knows to
//     snapshot and resubscribe (RFC §8.2).
//  4. If the channel is closed mid-stream the subscriber fell behind
//     the per-subscriber buffer (Options.SubscriberBuffer). Surface
//     this as FailedPrecondition + "gapped" — symmetric with case 3.
//  5. Wait for the publication cut before forwarding a dispatched entry,
//     then release it before Send so slow consumers do not stall writers.
//     Honor ctx cancellation throughout.
//
// Send errors terminate the stream and increment
// dropped{reason="send_failed"}.
func (s *LanternReplicationService) Subscribe(ctx context.Context, req *pb.SubscribeRequest, stream Sender[pb.SubscribeResponse]) error {
	if s.log == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("replication is not enabled on this server"))
	}
	switch req.GetProjection() {
	case pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY:
		return s.subscribeIdentity(ctx, req, stream)
	case pb.SubscribeProjection_SUBSCRIBE_PROJECTION_UNSPECIFIED, pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION:
		if req.GetBootstrap() {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("bootstrap requires identity-only projection"))
		}
	default:
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown Subscribe projection %d", req.GetProjection()))
	}
	cursor := req.GetFromSeqPerOrigin()
	fromLocalSeq := req.GetFromLocalSeq()
	if fromLocalSeq == 0 {
		fromLocalSeq = 1
	}
	var (
		faultCh <-chan struct{}
		ch      <-chan mutationlog.Entry
		cancel  func() error
		openErr error
	)
	register := func(generation <-chan struct{}) {
		faultCh = generation
		ch, cancel, openErr = s.log.Subscribe(fromLocalSeq)
	}
	if cut, ok := s.origins.(subscribeCutProvider); ok {
		if err := cut.withReplicationSubscribeCut(register); err != nil {
			s.metrics.OnSubscribeDropped("gapped")
			return err
		}
	} else {
		// Narrow test origin providers may lack the production publication
		// gate. Retain their historical read-only fallback.
		if status, ok := s.origins.(publicationStatusProvider); ok {
			var faulted bool
			faultCh, faulted = status.publicationStatus()
			if faulted {
				s.metrics.OnSubscribeDropped("gapped")
				return publicationGapError()
			}
		}
		register(faultCh)
	}
	if openErr != nil {
		if errors.Is(openErr, mutationlog.ErrGapped) {
			s.metrics.OnSubscribeDropped("gapped")
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("gapped: log truncated below requested local seq %d; snapshot and resubscribe", fromLocalSeq))
		}
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("subscribe failed: %w", openErr))
	}
	defer func() { _ = cancel() }()

	s.metrics.OnSubscribeStarted()
	defer s.metrics.OnSubscribeEnded()

	for {
		select {
		case <-ctx.Done():
			return ctxToConnect(ctx.Err())
		case <-faultCh:
			s.metrics.OnSubscribeDropped("gapped")
			return publicationGapError()
		case entry, ok := <-ch:
			// Append can hand an entry to the log dispatcher before its
			// enclosing graph/receipt publication gate is released. Wait for
			// that cut before exposing the frame, without holding the gate
			// across a potentially slow network Send.
			if cut, hasCut := s.origins.(snapshotCutProvider); hasCut {
				if err := cut.withReplicationSnapshotCut(func() {}); err != nil {
					s.metrics.OnSubscribeDropped("gapped")
					return err
				}
			}
			// When log and fault channels are both ready, the select may
			// choose an entry. Do not send it on a poisoned stream.
			select {
			case <-faultCh:
				s.metrics.OnSubscribeDropped("gapped")
				return publicationGapError()
			default:
			}
			if !ok {
				// Slow subscriber: log closed our channel mid-stream.
				s.metrics.OnSubscribeDropped("gapped")
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("gapped: subscriber fell behind; snapshot and resubscribe"))
			}
			mu, ok := graphMutationFromLog(entry.Op)
			if !ok {
				l := s.loggerOrDefault()
				l.Warn("replication: unexpected mutation log entry type",
					slog.String("type", "non-Mutation payload"),
					slog.Uint64("seq", entry.Seq))
				return connect.NewError(connect.CodeInternal, fmt.Errorf(
					"replication: malformed mutation log entry at seq=%d", entry.Seq))
			}
			if _, receipt := mu.GetOp().GetOp().(*pb.MutationOp_ReplicatedReceiptEdgeDelete); receipt {
				if !req.GetAcceptReceiptEnvelopes() {
					return connect.NewError(connect.CodeInvalidArgument,
						errors.New("full Subscribe consumer must accept receipt envelopes before receiving them"))
				}
				if _, err := acceptedReceiptEdgeDeleteKeys(mu); err != nil {
					return connect.NewError(connect.CodeInternal, fmt.Errorf("replication: malformed receipt envelope at seq=%d: %w", entry.Seq, err))
				}
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
			if err := stream.Send(&pb.SubscribeResponse{Event: &pb.SubscribeResponse_Mutation{Mutation: mu}}); err != nil {
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
//  1. Capture the per-origin/local-log cutoffs, cutoff_hlc, causal floors,
//     vertices, and edges in one Snapshot cut. The production
//     OriginStatesProvider holds the service commit gate across this capture,
//     and GraphCache takes its own write lock for the graph image. An empty
//     origin map denotes a cold cluster or an unwired test provider.
//  2. Send SnapshotHeader first, then each owned body frame, honouring
//     stream.Context() cancellation between sends. No commit gate is held
//     while sending to the client.
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
	var cutoffHLC hlc.Timestamp
	var cutoffLocalSeq uint64
	var barriers graphcache.CausalBarrierSnapshot[string]
	var tombstones graphcache.TombstoneSnapshot[string]
	var graph graphcache.GraphSnapshot[string, *pb.Vertex]
	capture := func() {
		if s.origins != nil {
			states := s.origins.OriginStates()
			if len(states) > 0 {
				cutoffPerOrigin = make(map[string]uint64, len(states))
				for _, st := range states {
					cutoffPerOrigin[hex.EncodeToString(st.Origin[:])] = st.LastSeq
				}
			}
		}
		if s.clock != nil {
			cutoffHLC = s.clock.Now()
		}
		if s.log != nil {
			cutoffLocalSeq, _ = s.log.LastSeq()
		}
		if snapshotter, ok := s.backend.(replicationSnapshotter); ok {
			snapshot := snapshotter.SnapshotReplication()
			barriers = snapshot.Barriers
			tombstones = snapshot.Tombstones
			graph = snapshot.Graph
		} else {
			// Compatibility seam for narrow fake backends. Production
			// GraphCache always captures causal and live state in one pass.
			graph = graphcache.GraphSnapshot[string, *pb.Vertex]{
				Vertices: s.backend.SnapshotVertices(),
				Edges:    s.backend.SnapshotEdges(),
			}
		}
	}
	if gate, ok := s.origins.(snapshotCutProvider); ok {
		if err := gate.withReplicationSnapshotCut(capture); err != nil {
			return err
		}
	} else {
		capture()
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

	var vertexTombstoneCount uint64
	for _, tombstone := range tombstones.Vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if err := stream.Send(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexTombstone{
			VertexTombstone: &pb.SnapshotVertexTombstone{
				Key: tombstone.Key, Hlc: hlcToProto(tombstone.HLC),
				Expiration: timestamppb.New(tombstone.Expiration),
			},
		}}); err != nil {
			return err
		}
		vertexTombstoneCount++
	}
	var edgeTombstoneCount uint64
	for _, tombstone := range tombstones.Edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if err := stream.Send(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeTombstone{
			EdgeTombstone: &pb.SnapshotEdgeTombstone{
				Tail: tombstone.Tail, Head: tombstone.Head,
				Hlc: hlcToProto(tombstone.HLC), Expiration: timestamppb.New(tombstone.Expiration),
			},
		}}); err != nil {
			return err
		}
		edgeTombstoneCount++
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
					Vertex: replicationSnapshotVertex(v),
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
				Hlc:        hlcToProto(c.HLC),
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
				VertexTombstoneCount:     vertexTombstoneCount,
				EdgeTombstoneCount:       edgeTombstoneCount,
			},
		},
	}
	return stream.Send(footer)
}

// replicationSnapshotVertex converts GraphCache's nil value sentinel into the
// public Vertex nil-value arm. Edge writes auto-create endpoint vertices with a
// nil *pb.Vertex payload, but a SnapshotVertex wire frame must remain
// self-describing: the receiver needs both the key and expiration and rejects a
// nil payload as a truncated/corrupt frame. Non-nil values already carry their
// exact public representation and can be streamed unchanged.
func replicationSnapshotVertex(v graphcache.SnapshotVertex[string, *pb.Vertex]) *pb.Vertex {
	if v.Value != nil {
		return v.Value
	}
	vertex := &pb.Vertex{
		Key:   v.Key,
		Value: &pb.Vertex_Nil{Nil: true},
	}
	if !v.Expiration.IsZero() {
		vertex.Expiration = timestamppb.New(v.Expiration)
	}
	return vertex
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
	var rows []OriginState
	if cut, ok := s.origins.(snapshotCutProvider); ok {
		if err := cut.withReplicationSnapshotCut(func() {
			rows = s.origins.OriginStates()
		}); err != nil {
			return nil, err
		}
	} else {
		rows = s.origins.OriginStates()
	}
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
