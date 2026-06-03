package service

import (
	"errors"
	"log/slog"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReplicationServiceName is the fully-qualified gRPC service name for the
// replication surface. Used for per-service health reporting.
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

// LanternReplicationService implements pb.LanternReplicationServiceServer.
// It exposes the mutation log as a resumable, back-pressured server-streaming
// RPC for peer replication and CDC consumers.
//
// The service holds a reference to the same *mutationlog.Log that
// LanternService.WithReplication wired into the write path, so subscribers
// see every successfully appended mutation in seq order.
type LanternReplicationService struct {
	pb.UnimplementedLanternReplicationServiceServer
	log     *mutationlog.Log
	backend Backend
	clock   *hlc.Clock
	metrics SubscribeMetrics
	logger  *slog.Logger
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

// Subscribe implements pb.LanternReplicationServiceServer.
//
// Flow:
//  1. Open a subscription on the mutation log starting at req.FromSeq.
//     If FromSeq is below the ring's firstSeq the log returns ErrGapped
//     and we surface FailedPrecondition + reason "gapped" so the client
//     knows to snapshot and resubscribe (see RFC §8.2).
//  2. Drain the subscription channel and forward each entry as a
//     SubscribeResponse. The Mutation stored on the entry already carries
//     hlc/origin/op; we shallow-copy it to overwrite Seq with the
//     authoritative value from the log entry (the stored Mutation has
//     Seq=0 because the log assigns seq at Append time).
//  3. If the channel is closed mid-stream the subscriber fell behind the
//     per-subscriber buffer (Options.SubscriberBuffer). Surface this as
//     FailedPrecondition + "gapped" — symmetric with case 1.
//  4. Honor stream.Context() cancellation throughout.
//
// Send errors terminate the stream and increment dropped{reason="send_failed"}.
func (s *LanternReplicationService) Subscribe(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.SubscribeResponse]) error {
	if s.log == nil {
		return status.Error(codes.Unavailable, "replication is not enabled on this server")
	}
	ch, cancel, err := s.log.Subscribe(req.GetFromSeq())
	if err != nil {
		if errors.Is(err, mutationlog.ErrGapped) {
			s.metrics.OnSubscribeDropped("gapped")
			return status.Errorf(codes.FailedPrecondition,
				"gapped: from_seq=%d is older than the first available seq; snapshot and resubscribe",
				req.GetFromSeq())
		}
		return status.Errorf(codes.Unavailable, "subscribe failed: %v", err)
	}
	defer func() { _ = cancel() }()

	s.metrics.OnSubscribeStarted()
	defer s.metrics.OnSubscribeEnded()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case entry, ok := <-ch:
			if !ok {
				// Slow subscriber: log closed our channel mid-stream.
				s.metrics.OnSubscribeDropped("gapped")
				return status.Error(codes.FailedPrecondition,
					"gapped: subscriber fell behind; snapshot and resubscribe")
			}
			mu, ok := entry.Op.(*pb.Mutation)
			if !ok {
				l := s.loggerOrDefault()
				l.Warn("replication: unexpected mutation log entry type",
					slog.String("type", "non-Mutation payload"),
					slog.Uint64("seq", entry.Seq))
				return status.Errorf(codes.Internal,
					"replication: malformed mutation log entry at seq=%d", entry.Seq)
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
func (s *LanternReplicationService) Snapshot(_ *pb.SnapshotRequest, stream grpc.ServerStreamingServer[pb.SnapshotResponse]) error {
	if s.backend == nil {
		return status.Error(codes.Unavailable, "snapshot is not enabled on this server")
	}
	ctx := stream.Context()

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
			return status.FromContextError(err).Err()
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
			return status.FromContextError(err).Err()
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
