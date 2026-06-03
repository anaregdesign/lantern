package service

import (
	"errors"
	"log/slog"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	metrics SubscribeMetrics
	logger  *slog.Logger
}

// NewLanternReplicationService constructs the service. log MUST be the same
// instance handed to LanternService.WithReplication; otherwise subscribers
// will not see locally-originated mutations.
func NewLanternReplicationService(log *mutationlog.Log) *LanternReplicationService {
	return &LanternReplicationService{log: log, metrics: nopSubscribeMetrics{}}
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
