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

// A graph-first mutation can still own an applied postimage missing from the
// durable cut. Preserve the applied-view path until its exact append repairs it.
func (s *Executor) storedContributionDeleteGraphLocked() *graphcache.GraphCache[string, *pb.Vertex] {
	if s.log == nil || s.runtime == nil || s.runtime.tarp == nil {
		return nil
	}
	graph := s.cleanStoredGraphLocked()
	if graph == nil || !graph.SupportsMutationStaging() {
		return nil
	}
	return graph
}

func (s *Executor) commitLocalStoredContributionDelete(
	ctx context.Context, graph *graphcache.GraphCache[string, *pb.Vertex], keys []graphcache.EdgeContributionKey[string],
	op *pb.MutationOp, ts hlc.Timestamp, expiration time.Time,
) (*pb.DeleteEdgeContributionsResponse, error) {
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	publication := stagedPublication{service: s}
	defer publication.failClosedOnPanic()
	stage, err := graph.PrepareStoredEdgeContributionDelete(ctx, keys, ts, expiration)
	if err != nil {
		return nil, writeError(err)
	}
	defer stage.Abort()
	result := stage.Result()
	deleted, err := checkedDeleteOutcomes(result.Existed, len(keys))
	if err != nil {
		return nil, err
	}
	effect, err := s.newLocalGraphDeleteEffectLocked(op, ts, expiration, contributionDeleteAcceptedIndexes(result))
	if err != nil {
		return nil, err
	}
	if err := publication.commitStoredContributionDelete(ctx, stage, effect, ts); err != nil {
		return nil, err
	}
	if s.onAppend != nil {
		s.onAppend()
	}
	return &pb.DeleteEdgeContributionsResponse{Deleted: deleted, Existed: result.Existed}, nil
}

func edgeContributionDeleteMutationKeys(op *pb.MutationOp) ([]graphcache.EdgeContributionKey[string], string, error) {
	var wireKeys []*pb.EdgeContributionKey
	var opName string
	switch entry := op.GetOp().(type) {
	case *pb.MutationOp_DeleteEdgeContribution:
		wireKeys = []*pb.EdgeContributionKey{{
			Tail: entry.DeleteEdgeContribution.GetTail(), Head: entry.DeleteEdgeContribution.GetHead(),
			ContribId: entry.DeleteEdgeContribution.GetContribId(),
		}}
		opName = "DeleteEdgeContribution"
	case *pb.MutationOp_DeleteEdgeContributions:
		wireKeys = entry.DeleteEdgeContributions.GetContributions()
		opName = "DeleteEdgeContributions"
	default:
		return nil, "", nil
	}
	keys, err := edgeContributionKeysFromWire(wireKeys)
	return keys, opName, err
}

// An empty name leaves an unsupported mutation or dirty cut on the existing
// path. An admitted durable stage only advances the origin after WAL success.
func (s *Executor) commitReplicatedStoredContributionDelete(ctx context.Context, m *pb.Mutation) (string, error) {
	keys, name, err := edgeContributionDeleteMutationKeys(m.GetOp())
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: %w", err))
	}
	if name == "" {
		return "", nil
	}
	graph := s.storedContributionDeleteGraphLocked()
	if graph == nil {
		return "", nil
	}
	expiration, err := s.validateIncomingTombstoneExpiration(m)
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: %w", err))
	}
	ts := hlcFromProto(m.GetHlc())
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	publication := stagedPublication{service: s}
	defer publication.failClosedOnPanic()
	stage, err := graph.PrepareStoredReplicatedEdgeContributionDelete(ctx, keys, ts, expiration)
	if err != nil {
		return "", writeError(err)
	}
	defer stage.Abort()
	effect, err := newGraphDeleteEffectEnvelope(m, contributionDeleteAcceptedIndexes(stage.Result()))
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
	}
	if err := publication.commitStoredContributionDelete(ctx, stage, effect, ts); err != nil {
		return "", err
	}
	return name, nil
}

func contributionDeleteAcceptedIndexes(result graphcache.EdgeContributionDeleteStageResult[string]) []int {
	indexes := make([]int, len(result.Accepted))
	for i, item := range result.Accepted {
		indexes[i] = item.Index
	}
	return indexes
}

func (p *stagedPublication) commitStoredContributionDelete(
	ctx context.Context, stage *graphcache.EdgeContributionDeleteTransaction[string, *pb.Vertex],
	effect *graphDeleteEffectEnvelope, ts hlc.Timestamp,
) error {
	m := effect.GraphMutation()
	originTx, ok := p.service.origins.stageNext(hlc.NodeID(m.GetOrigin()), m.GetSeq(), ts)
	if !ok {
		return connect.NewError(connect.CodeInternal, errors.New("contribution Delete could not stage contiguous origin sequence"))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	stage.Apply()
	if err := p.commit(effect, ts, nil, stage.Commit, originTx, stage); err != nil {
		if errors.Is(err, mutationlog.ErrSeqExhausted) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}
	return nil
}
