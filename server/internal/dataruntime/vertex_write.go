package dataruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// The caller owns the serving publication gate. A failed append can leave
// a graph-first effect applied in RAM while its Tarp postimage is absent.
// Keep those pending effects authoritative until the exact relay repair.
func (s *Executor) cleanStoredVertexGraphLocked() *graphcache.GraphCache[string, *pb.Vertex] {
	if s.publicationFaultCount != 0 || s.runtime == nil {
		return nil
	}
	graph, _ := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if graph == nil || graph != s.runtime.graph || !graph.StorageBoundTo(s.runtime.GraphStore()) {
		return nil
	}
	return graph
}

func (s *Executor) putVertexBatchAtCut(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp, ifAbsent bool) ([]graphcache.PutOutcome, error) {
	if graph := s.cleanStoredVertexGraphLocked(); graph != nil {
		// Graph-first application and exact publication already own this
		// admitted request. Caller cancellation cannot abandon their work.
		return graph.PutVerticesFromCommittedCut(context.Background(), items, ts, ifAbsent)
	}
	if ifAbsent {
		_, outcomes, err := s.cache.PutVerticesWithExpirationIfAbsentHLCOutcomesChecked(items, ts)
		return outcomes, err
	}
	return s.cache.PutVerticesWithExpirationHLCOutcomesChecked(items, ts)
}

func (s *Executor) applyVertexPutsAtCut(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp) ([]graphcache.PutOutcome, error) {
	if graph := s.cleanStoredVertexGraphLocked(); graph != nil {
		return graph.ApplyVertexPutsFromCommittedCut(context.Background(), items, ts)
	}
	return s.cache.PutVerticesWithExpirationHLCOutcomes(items, ts), nil
}

func (s *Executor) deleteVertexBatchAtCut(keys []string, ts hlc.Timestamp, expiration time.Time, local bool) ([]bool, []int, error) {
	if graph := s.cleanStoredVertexGraphLocked(); graph != nil {
		return graph.DeleteVerticesFromCommittedCut(context.Background(), keys, ts, expiration, local)
	}
	if local {
		return s.cache.DeleteVerticesHLCDecisionsChecked(keys, ts, expiration)
	}
	existed, accepted := s.cache.DeleteVerticesHLCDecisions(keys, ts, expiration)
	return existed, accepted, nil
}

// localVertexWALApproval binds Search admission to the exact owned local
// envelope. It lives only as long as a staged attempt or a pending repair.
// It does not authorize admitting the current postimage during repair.
type localVertexWALApproval struct {
	effect       *graphPutEffectEnvelope
	diskRequired bool
	diskApproved bool
}

// localVertexWALWriter carries the fresh stage or the current applied graph
// into the same publication. A pending repair retains only the approval and
// envelope, never the stage or its undo journal.
type localVertexWALWriter struct {
	graphcache.StorageRowWriter[string, *pb.Vertex]
	approval  *localVertexWALApproval
	repairing bool
	dirtyCut  bool
}

func (w *localVertexWALWriter) matches(op mutationlog.MutationOp) bool {
	effect, ok := op.(*graphPutEffectEnvelope)
	return ok && effect != nil && w != nil && w.approval != nil && w.approval.effect == effect
}

func (w *localVertexWALWriter) StorageApplicationTime() time.Time {
	if timed, ok := w.StorageRowWriter.(interface{ StorageApplicationTime() time.Time }); ok {
		return timed.StorageApplicationTime()
	}
	return time.Time{}
}

func (s *Executor) commitLocalVertexWALLocked(
	effect mutationlog.MutationOp, ts hlc.Timestamp, writer *localVertexWALWriter, release func(mutationlog.Entry),
) error {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.markReceiptCommitFaultLocked()
			panic(recovered)
		}
	}()
	relay := s.runtime.tarp
	_, err := s.log.CommitPrepared(effect, ts, func(wal mutationlog.WAL, entry mutationlog.Entry) error {
		if wal != relay || !writer.matches(entry.Op) {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("Tarp Vertex publication requires the original owner and operation")}
		}
		return relay.write(entry, writer)
	}, release)
	if stagedPublicationRequiresRecovery(err) {
		s.markReceiptCommitFaultLocked()
	}
	return err
}

func (s *Executor) putLocalVerticesLocked(
	ctx context.Context, items []graphcache.VertexItem[string, *pb.Vertex], in []*pb.Vertex, ts hlc.Timestamp, ifAbsent bool,
) (*pb.PutVerticesResponse, error) {
	graph, _ := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if s.runtime != nil && s.runtime.tarp != nil && graph != nil && graph == s.runtime.graph &&
		graph.SupportsMutationStaging() && graph.StorageBoundTo(s.runtime.GraphStore()) {
		return s.putStagedLocalVerticesLocked(ctx, graph, items, in, ts, ifAbsent)
	}
	outcomes, err := s.putVertexBatchAtCut(items, ts, ifAbsent)
	if err != nil {
		return nil, searchIndexWriteError(err)
	}
	wireOutcomes, err := putOutcomes(outcomes, len(in))
	if err != nil {
		return nil, err
	}
	if op := replicatedPutVerticesMutation(in, outcomes); op != nil {
		if err := s.publishLocalGraphPutLocked(op, ts, acceptedPutOutcomes(outcomes)); err != nil {
			return nil, err
		}
	}
	return &pb.PutVerticesResponse{Outcomes: wireOutcomes}, nil
}

func (s *Executor) putStagedLocalVerticesLocked(
	ctx context.Context, graph *graphcache.GraphCache[string, *pb.Vertex], items []graphcache.VertexItem[string, *pb.Vertex], in []*pb.Vertex,
	ts hlc.Timestamp, ifAbsent bool,
) (*pb.PutVerticesResponse, error) {
	// Keep the application and owned WAL attempt together under the executor
	// gate, even if the original caller is gone.
	ctx = context.WithoutCancel(ctx)
	dirty := s.publicationFaultCount != 0
	var stage *graphcache.VertexPutTransaction[string, *pb.Vertex]
	var err error
	if dirty {
		stage, err = graph.BeginVertexPutInAppliedViewContext(ctx, items, ts, ifAbsent)
	} else {
		stage, err = graph.BeginVertexPutContext(ctx, items, ts, ifAbsent)
	}
	if err != nil {
		return nil, searchIndexWriteError(err)
	}
	defer stage.Abort()
	outcomes := stage.Result().Outcomes
	wireOutcomes, err := putOutcomes(outcomes, len(in))
	if err != nil {
		return nil, err
	}
	op := replicatedPutVerticesMutation(in, outcomes)
	if op == nil {
		return &pb.PutVerticesResponse{Outcomes: wireOutcomes}, nil
	}
	effect, err := newGraphPutEffectEnvelope(s.newLocalMutationLocked(op, ts), acceptedPutOutcomes(outcomes))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("local Put publication evidence: %w", err))
	}
	approval := &localVertexWALApproval{effect: effect}
	writer := &localVertexWALWriter{StorageRowWriter: stage, approval: approval, dirtyCut: dirty}
	released := false
	err = s.appendLocalGraphWALLocked(effect, writer, func(mutationlog.Entry) {
		stage.Commit()
		released = true
	})
	if err != nil {
		if released {
			// The write is durable and must not enter append-only repair.
			s.markReceiptCommitFaultLocked()
			return nil, publicationGapError()
		}
		var abort *mutationlog.DefiniteWALAbort
		var rejection interface{ graphWriteRejection() error }
		if !stagedPublicationRequiresRecovery(err) && errors.As(err, &abort) && errors.As(err, &rejection) {
			return nil, rejection.graphWriteRejection()
		}
		if approval.diskRequired && !approval.diskApproved {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("local Vertex Put before Search admission: %w", err))
		}
		// Match graph-first semantics after admission. A definite write
		// failure leaves the graph-applied candidate for append-only repair.
		// An unknown result is fenced by commitLocalVertexWALLocked.
		stage.Commit()
		return nil, s.retainLocalGraphPublicationLocked(effect.GraphMutation(), effect, approval, err)
	}
	return &pb.PutVerticesResponse{Outcomes: wireOutcomes}, nil
}
