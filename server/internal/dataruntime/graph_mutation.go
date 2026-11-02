package dataruntime

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/decisiontrace"
	"google.golang.org/protobuf/proto"
)

// MutateGraph publishes one receipt group with one origin sequence and WAL
// decision. It never falls back to independently publishing either family.
func (s *Executor) MutateGraph(ctx context.Context, request *pb.MutateGraphRequest) (*pb.MutateGraphResponse, error) {
	observation := newGraphMutationObservation()
	observation.mark(ctx, "admission")
	if !s.graphMutationAdmission.TryLock() {
		return nil, connect.NewError(connect.CodeResourceExhausted, graphcache.ErrGraphMutationBudget)
	}
	defer s.graphMutationAdmission.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	observation.mark(ctx, "operation_budget")
	if request == nil || request.GetReceiptContext() == nil || proto.Size(request) > graphMutationMaxBytes || rejectProtoUnknownFields(request.ProtoReflect()) != nil {
		return nil, invalidReceiptRequest(errGraphMutation)
	}
	vertexCount, err := validateGraphMutationItems(request.GetItems(), s.namespaceFormat)
	if err != nil {
		return nil, invalidReceiptRequest(err)
	}
	// The largest original/accepted wire form includes the private receipt and
	// origin metadata. Reserve it before owning payload bytes or graph state.
	worst := 256
	for _, item := range request.GetItems() {
		worst += 2*proto.Size(item) + 1024
	}
	if worst > graphMutationMaxBytes {
		return nil, connect.NewError(connect.CodeResourceExhausted, errGraphMutation)
	}
	observation.mark(ctx, "receipt_runtime")
	runtime, release, err := s.acquirePublicReceiptRuntime()
	if err != nil {
		return nil, err
	}
	defer release()
	observation.mark(ctx, "receipt_context")
	group, ids, err := s.validatePublicReceiptContext(runtime, request.GetReceiptContext(), len(request.GetItems()), "items")
	if err != nil {
		return nil, err
	}
	vc := receiptVertexPutCall{Group: group, Items: make([]receiptVertexPutItem, vertexCount)}
	ec := receiptEdgeAddCall{Group: group, NoEndpointCreation: true, Items: make([]receiptEdgeAddItem, len(ids)-vertexCount)}
	for i, item := range request.GetItems() {
		if i < vertexCount {
			vc.Items[i] = receiptVertexPutItem{ID: ids[i], Vertex: item.GetPutVertex()}
		} else {
			add := item.GetAddEdge()
			var id graphcache.ContribID
			copy(id[:], add.GetContribId())
			ec.Items[i-vertexCount] = receiptEdgeAddItem{ID: ids[i], Edge: add.GetEdge(), ContribID: id}
		}
	}
	observation.mark(ctx, "prepare_items")
	ov, vertices, iv, err := prepareVertexPutReceiptCall(s, vc)
	if err != nil {
		return nil, err
	}
	oe, edges, ie, err := prepareEdgeAddReceiptCall(s, ec)
	if err != nil {
		return nil, err
	}
	original := make([]*pb.GraphMutationItem, len(ids))
	intents := append(iv, ie...)
	for i := range original {
		if i < vertexCount {
			original[i] = &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: ov[i]}}
			vertices[i].DenyLifecycleReduction = s.denyVertexLifecycle(ctx, vertices[i].Key)
			vertices[i].CaptureLifecycleReduction = s.namespaceFormat != ""
		} else {
			original[i] = &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: oe[i-vertexCount], ContribId: append([]byte(nil), ec.Items[i-vertexCount].ContribID[:]...)}}}
		}
		intents[i].Index, intents[i].Count = uint32(i), uint32(len(ids))
		intents[i].Digest, _, err = graphMutationDigest(original[i], s.namespaceFormat)
		if err != nil {
			return nil, invalidReceiptRequest(err)
		}
	}
	observation.mark(ctx, "replication_lock_wait")
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	observation.mark(ctx, "origin_lock_wait")
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	observation.mark(ctx, "receipt_begin")
	publication := stagedPublication{service: s}
	defer publication.failClosedOnPanic()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return nil, publicationGapError()
	}
	storeTx, err := runtime.store.Begin(time.Now())
	if err != nil {
		return nil, receiptStoreError(err)
	}
	defer storeTx.Abort()
	classification, prior, err := storeTx.Classify(intents)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if classification == mutationreceipt.Duplicate {
		observation.mark(ctx, "duplicate_rows")
		if err := s.authorizeReceiptRows(ctx, prior); err != nil {
			return nil, err
		}
		return graphMutationResponse(prior)
	}
	results := make([][]byte, len(ids))
	for i := range results {
		if i < vertexCount {
			results[i] = make([]byte, 1)
		} else {
			results[i] = make([]byte, 4)
		}
	}
	observation.mark(ctx, "receipt_reserve")
	if err := storeTx.Reserve(results); err != nil {
		return nil, receiptStoreError(err)
	}
	observation.mark(ctx, "prepare_local_mutation")
	if err := s.prepareLocalMutationLocked(); err != nil {
		return nil, err
	}
	if err := s.checkVertexCapacity(len(vertices)); err != nil {
		return nil, err
	}
	if err := s.checkEdgeCapacity(len(edges)); err != nil {
		return nil, err
	}
	observation.mark(ctx, "clock_floor")
	floor, err := receiptClockFloor(storeTx)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(floor); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	ts := s.clock.Now()
	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1
	observation.mark(ctx, "graph_begin")
	graphTx, err := s.receiptVertexPutCoordinator.cache.BeginGraphMutation(ctx, vertices, edges, ts, uint64(4*worst))
	if err != nil {
		return nil, graphMutationError(err)
	}
	defer graphTx.Abort()
	observation.mark(ctx, "graph_staged")
	result := graphTx.Result()
	outcomes, err := putOutcomes(result.Vertices.Outcomes, len(vertices))
	if err != nil {
		return nil, err
	}
	for i, outcome := range outcomes {
		results[i] = []byte{byte(outcome)}
	}
	copy(results[vertexCount:], receiptEdgeAddResults(result.Edges.Effective))
	if err := storeTx.ReplaceReservedResults(results); err != nil {
		return nil, receiptStoreError(err)
	}
	reductions := make([]bool, len(ids))
	copy(reductions, result.Vertices.LifecycleReductions)
	if s.namespaceFormat != "" {
		if err := storeTx.SetReservedLifecycleReductions(reductions); err != nil {
			return nil, receiptStoreError(err)
		}
	}
	receipts, err := storeTx.ReservedReceipts()
	if err != nil {
		return nil, receiptStoreError(err)
	}
	epoch, policy := runtime.store.Epoch(), runtime.store.PolicyFingerprint()
	call := &pb.ReplicatedGraphMutation{Version: 1, DeploymentEpoch: append([]byte(nil), epoch[:]...), PolicyFingerprint: append([]byte(nil), policy[:]...), Items: make([]*pb.ReplicatedGraphMutationItem, len(ids))}
	for i, receipt := range receipts {
		status, err := receiptStatusProto(receipt.ID, mutationreceipt.Observation{Status: mutationreceipt.Confirmed, Receipt: receipt})
		if err != nil {
			return nil, err
		}
		call.Items[i] = &pb.ReplicatedGraphMutationItem{Original: original[i], Receipt: status.GetReceipt(), LifecycleReduced: receipt.LifecycleReduction}
	}
	for _, accepted := range result.Edges.Accepted {
		call.Items[vertexCount+accepted.Index].EdgeAccepted = true
	}
	ei := identityIndexes(len(edges))
	for i := range ei {
		ei[i] += vertexCount
	}
	call.Projection, err = graphMutationProjection(result, identityIndexes(len(vertices)), ei)
	if err != nil {
		return nil, err
	}
	mutation := &pb.Mutation{NamespaceFormat: s.namespaceFormat, Origin: append([]byte(nil), origin[:]...), Seq: seq, Hlc: hlcToProto(ts), Op: &pb.MutationOp{Op: &pb.MutationOp_GraphMutation{GraphMutation: call}}}
	envelope, err := decodeGraphMutation(mutation)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	observation.mark(ctx, "validate_envelope")
	if err := s.validateReplicationFrame(envelope); err != nil {
		return nil, err
	}
	if err := s.validateReplicationRelayFrame(envelope); err != nil {
		return nil, err
	}
	observation.mark(ctx, "stage_origin")
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errGraphMutation)
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	observation.mark(ctx, "stage_receipt")
	if err := storeTx.Stage(); err != nil {
		return nil, receiptStoreError(err)
	}
	observation.mark(ctx, "check_before_wal")
	if err := graphTx.CheckBeforeWAL(); err != nil {
		return nil, graphMutationError(err)
	}
	// Entering this boundary is not proof of either commit or nonexecution.
	observation.mark(ctx, "publication_enter")
	if err := publication.commit(envelope, ts, storeTx, graphTx.Commit, originTx); err != nil {
		return nil, graphMutationPublicationError(err)
	}
	observation.mark(ctx, "publication_complete")
	return graphMutationResponse(receipts)
}

// These process-local observations never supply a deadline or authority input.
// Emit is bounded and nonblocking even while a publication lock is held. Only
// fixed stage names, a local serial and timing leave this seam; request/receipt/
// identity bytes and arbitrary errors are excluded. A missing later event never
// proves nonexecution; the fixture retains the public RPC result separately.
type graphMutationObservation struct {
	id      uint64
	started time.Time
}

func newGraphMutationObservation() graphMutationObservation {
	if !decisiontrace.Enabled() {
		return graphMutationObservation{}
	}
	return graphMutationObservation{id: decisiontrace.NextID(), started: time.Now()}
}

func (o *graphMutationObservation) mark(ctx context.Context, stage string) {
	if o.id == 0 {
		return
	}
	deadline, hasDeadline := ctx.Deadline()
	var remaining int64
	if hasDeadline {
		remaining = int64(time.Until(deadline))
	}
	decisiontrace.Emit(decisiontrace.Event{Kind: "graph_mutation", Object: o.id,
		Stage: stage, Reason: "progress", Elapsed: int64(time.Since(o.started)),
		HasDeadline: hasDeadline, DeadlineLeft: remaining})
}

func graphMutationResponse(receipts []mutationreceipt.Receipt) (*pb.MutateGraphResponse, error) {
	response := &pb.MutateGraphResponse{Results: make([]*pb.GraphMutationResult, len(receipts))}
	for i, receipt := range receipts {
		result := &pb.GraphMutationResult{}
		switch receipt.Kind {
		case mutationreceipt.PutVertex:
			prior, err := receiptVertexPutResponse([]mutationreceipt.Receipt{receipt})
			if err != nil {
				return nil, err
			}
			result.Result = &pb.GraphMutationResult_PutVertex{PutVertex: prior.Outcomes[0]}
		case mutationreceipt.AddEdge:
			prior, err := receiptEdgeAddResponse([]mutationreceipt.Receipt{receipt})
			if err != nil {
				return nil, err
			}
			result.Result = &pb.GraphMutationResult_AddEdgeEffectiveWeight{AddEdgeEffectiveWeight: prior.EffectiveWeights[0]}
		default:
			return nil, connect.NewError(connect.CodeInternal, errGraphMutation)
		}
		response.Results[i] = result
	}
	return response, nil
}

func graphMutationPublicationError(err error) error {
	if errors.Is(err, mutationlog.ErrSeqExhausted) && !stagedPublicationRequiresRecovery(err) {
		return connect.NewError(connect.CodeResourceExhausted, err)
	}
	return connect.NewError(connect.CodeUnavailable, err)
}
func graphMutationError(err error) error {
	if errors.Is(err, graphcache.ErrGraphMutationBudget) || errors.Is(err, search.ErrPreparationScratch) {
		return connect.NewError(connect.CodeResourceExhausted, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ctxToConnect(err)
	}
	return searchIndexWriteError(err)
}
