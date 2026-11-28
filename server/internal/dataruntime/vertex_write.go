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
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// The caller owns the serving publication gate. A failed append can leave
// a graph-first effect applied in RAM while its Tarp postimage is absent.
// Keep those pending effects authoritative until the exact relay repair.
func (s *Executor) cleanStoredGraphLocked() *graphcache.GraphCache[string, *pb.Vertex] {
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
	if graph := s.cleanStoredGraphLocked(); graph != nil {
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
	if graph := s.cleanStoredGraphLocked(); graph != nil {
		return graph.ApplyVertexPutsFromCommittedCut(context.Background(), items, ts)
	}
	return s.cache.PutVerticesWithExpirationHLCOutcomes(items, ts), nil
}

func (s *Executor) deleteVertexBatchAtCut(keys []string, ts hlc.Timestamp, expiration time.Time, local bool) ([]bool, []int, error) {
	if graph := s.cleanStoredGraphLocked(); graph != nil {
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
	effect       mutationlog.MutationOp
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
	if w == nil || w.approval == nil {
		return false
	}
	switch effect := op.(type) {
	case *graphPutEffectEnvelope:
		return effect != nil && w.approval.effect == effect
	case *graphDeleteEffectEnvelope:
		return effect != nil && w.approval.effect == effect
	default:
		return false
	}
}

func (w *localVertexWALWriter) requiresSearchAdmission() bool {
	_, put := w.approval.effect.(*graphPutEffectEnvelope)
	return put
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
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("staged Vertex publication requires the original Tarp owner and operation")}
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
	if graph := s.stagedLocalVertexGraphLocked(); graph != nil {
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

func (s *Executor) stagedLocalVertexGraphLocked() *graphcache.GraphCache[string, *pb.Vertex] {
	if s.log == nil || s.runtime == nil || s.runtime.tarp == nil {
		return nil
	}
	graph, _ := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if graph == nil || graph != s.runtime.graph || !graph.SupportsMutationStaging() || !graph.StorageBoundTo(s.runtime.GraphStore()) {
		return nil
	}
	return graph
}

func (s *Executor) putStagedLocalVerticesLocked(
	ctx context.Context, graph *graphcache.GraphCache[string, *pb.Vertex], items []graphcache.VertexItem[string, *pb.Vertex], in []*pb.Vertex,
	ts hlc.Timestamp, ifAbsent bool,
) (*pb.PutVerticesResponse, error) {
	// Keep the application and owned WAL attempt together under the executor
	// gate, even if the original caller is gone.
	release, err := s.holdDiskSearchAdmission(ctx)
	if err != nil {
		return nil, ctxToConnect(err)
	}
	defer release()
	ctx = context.WithoutCancel(ctx)
	dirty := s.publicationFaultCount != 0
	var stage *graphcache.VertexPutTransaction[string, *pb.Vertex]
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
	if err := s.publishStagedLocalVertexWALLocked(effect, stage, stage.Commit, dirty); err != nil {
		return nil, err
	}
	return &pb.PutVerticesResponse{Outcomes: wireOutcomes}, nil
}

func (s *Executor) deleteLocalVerticesHLCLocked(
	ctx context.Context, op *pb.MutationOp, ts hlc.Timestamp, expiration time.Time,
) (*pb.DeleteVerticesResponse, error) {
	keys := op.GetDeleteVertices().GetKeys()
	if graph := s.stagedLocalVertexGraphLocked(); graph != nil {
		ctx = context.WithoutCancel(ctx)
		dirty := s.publicationFaultCount != 0
		var stage *graphcache.VertexDeleteTransaction[string, *pb.Vertex]
		var err error
		if dirty {
			stage, err = graph.BeginVertexDeleteInAppliedViewContext(ctx, keys, ts, expiration)
		} else {
			stage, err = graph.BeginVertexDeleteContext(ctx, keys, ts, expiration)
		}
		if err == nil {
			defer stage.Abort()
			return s.deleteStagedLocalVerticesLocked(stage, op, ts, expiration, dirty)
		}
		var limit *search.AnalysisLimitError
		if !errors.Is(err, search.ErrIndexIncomplete) && !errors.As(err, &limit) {
			return nil, writeError(err)
		}
		// Graph-first Delete can remove a document and recover the RAM index
		// when the old document cannot be captured for staged Search undo.
	}
	outcomes, accepted, err := s.deleteVertexBatchAtCut(keys, ts, expiration, true)
	if err != nil {
		return nil, writeError(err)
	}
	deleted, err := checkedDeleteOutcomes(outcomes, len(keys))
	if err != nil {
		return nil, err
	}
	if err := s.publishLocalGraphDeleteLocked(op, ts, expiration, accepted); err != nil {
		return nil, err
	}
	return &pb.DeleteVerticesResponse{Deleted: deleted, Existed: outcomes}, nil
}

func (s *Executor) deleteStagedLocalVerticesLocked(
	stage *graphcache.VertexDeleteTransaction[string, *pb.Vertex], op *pb.MutationOp, ts hlc.Timestamp,
	expiration time.Time, dirty bool,
) (*pb.DeleteVerticesResponse, error) {
	result := stage.Result()
	deleted, err := checkedDeleteOutcomes(result.Existed, len(op.GetDeleteVertices().GetKeys()))
	if err != nil {
		return nil, err
	}
	accepted := make([]int, len(result.Accepted))
	for i, item := range result.Accepted {
		accepted[i] = item.Index
	}
	effect, err := s.newLocalGraphDeleteEffectLocked(op, ts, expiration, accepted)
	if err != nil {
		return nil, err
	}
	if err := s.publishStagedLocalVertexWALLocked(effect, stage, stage.Commit, dirty); err != nil {
		return nil, err
	}
	return &pb.DeleteVerticesResponse{Deleted: deleted, Existed: result.Existed}, nil
}

func (s *Executor) publishStagedLocalVertexWALLocked(
	effect interface{ GraphMutation() *pb.Mutation }, stage graphcache.StorageRowWriter[string, *pb.Vertex], commit func(), dirty bool,
) error {
	approval := &localVertexWALApproval{effect: effect}
	writer := &localVertexWALWriter{StorageRowWriter: stage, approval: approval, dirtyCut: dirty}
	released := false
	err := s.appendLocalGraphWALLocked(effect, writer, func(mutationlog.Entry) {
		commit()
		released = true
	})
	if err != nil {
		if released {
			// The write is durable and must not enter append-only repair.
			s.markReceiptCommitFaultLocked()
			return publicationGapError()
		}
		var abort *mutationlog.DefiniteWALAbort
		var rejection interface{ graphWriteRejection() error }
		if !stagedPublicationRequiresRecovery(err) && errors.As(err, &abort) && errors.As(err, &rejection) {
			return rejection.graphWriteRejection()
		}
		if approval.diskRequired && !approval.diskApproved {
			return connect.NewError(connect.CodeUnavailable, fmt.Errorf("local Vertex publication before Search admission: %w", err))
		}
		// Match graph-first semantics after admission. A definite write
		// failure leaves the graph-applied candidate for append-only repair.
		// An unknown result is fenced by commitLocalVertexWALLocked.
		commit()
		return s.retainLocalGraphPublicationLocked(effect.GraphMutation(), effect, approval, err)
	}
	return nil
}
