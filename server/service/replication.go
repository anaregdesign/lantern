package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

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
//  1. Compute the minimum seq across all requested origins (cold-start
//     callers pass an empty map, which yields min=1 = oldest retained
//     entry). Open a log subscription at that minimum so the dispatcher
//     hands us every entry the cursor could possibly want.
//  2. For each entry, filter by the per-origin cursor: deliver only
//     when entry.Seq >= cursor[origin]. Origins absent from the cursor
//     are delivered from the oldest retained entry — this lets a
//     consumer that has never seen origin X (e.g. X joined the cluster
//     while the consumer was offline) catch up naturally.
//  3. ErrGapped from the log layer (the computed minimum is below the
//     ring's firstSeq) surfaces as FailedPrecondition + reason
//     "gapped" so the client knows to snapshot and resubscribe (RFC
//     §8.2).
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
	minSeq := minSeqFromCursor(cursor)
	ch, cancel, err := s.log.Subscribe(minSeq)
	if err != nil {
		if errors.Is(err, mutationlog.ErrGapped) {
			s.metrics.OnSubscribeDropped("gapped")
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"gapped: from_seq=%d is older than the first available seq; snapshot and resubscribe",
				minSeq))
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
			if want, present := cursor[hex.EncodeToString(mu.GetOrigin())]; present && entry.Seq < want {
				continue
			}
			// Shallow copy so we can stamp Seq without mutating the
			// shared Mutation pointer other subscribers (or the WAL) may
			// be observing.
			out := &pb.Mutation{
				Seq:    entry.Seq,
				Hlc:    mu.GetHlc(),
				Origin: mu.GetOrigin(),
				Op:     mu.GetOp(),
			}
			if err := stream.Send(&pb.SubscribeResponse{Mutation: out}); err != nil {
				s.metrics.OnSubscribeDropped("send_failed")
				return err
			}
		}
	}
}

// minSeqFromCursor returns the smallest seq present in the per-origin
// cursor, or 1 (the first valid log seq) when the cursor is empty so a
// cold-start subscriber receives every retained entry.
func minSeqFromCursor(cursor map[string]uint64) uint64 {
	if len(cursor) == 0 {
		return 1
	}
	var min uint64
	first := true
	for _, v := range cursor {
		if first || v < min {
			min = v
			first = false
		}
	}
	return min
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
//  1. Stamp the cutoff (cutoff_seq, cutoff_hlc) and send a SnapshotHeader
//     as the very first frame so receivers know where to resume Subscribe.
//     cutoff_seq is `log.LastSeq()` at snapshot-open time (0 when the log
//     is empty or replication is disabled — the peer is expected to start
//     Subscribe at cutoff_seq + 1 = 1, which matches the mutation log's
//     first valid seq); cutoff_hlc is `clock.Now()`.
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

	var cutoffSeq uint64
	if s.log != nil {
		if last, ok := s.log.LastSeq(); ok {
			cutoffSeq = last
		}
	}
	var cutoffHLC hlc.Timestamp
	if s.clock != nil {
		cutoffHLC = s.clock.Now()
	}
	header := &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Header{
			Header: &pb.SnapshotHeader{
				CutoffSeq: cutoffSeq,
				CutoffHlc: hlcToProto(cutoffHLC),
			},
		},
	}
	if err := stream.Send(header); err != nil {
		return err
	}

	vertices := s.backend.SnapshotVertices()
	var vertexCount uint64
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

	edges := s.backend.SnapshotEdges()
	var edgeCount uint64
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
				VertexCount: vertexCount,
				EdgeCount:   edgeCount,
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
