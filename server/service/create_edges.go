package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// WithEdgeCreateHA is trusted startup composition. It is independent of the
// local mutation log: standalone nodes also need that log for CDC and receipts.
// An active private peer plane disables local-only conditional creation.
func (s *LanternService) WithEdgeCreateHA() *LanternService { s.edgeCreateHA = true; return s }

// CreateEdges is plural-canonical. The complete batch is validated before
// storage lookup. A successful item changes only its Edge bucket and indexes.
func (s *LanternService) CreateEdges(ctx context.Context, req *pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if s.edgeCreateHA {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Edge Create requires standalone serving; cluster-wide absence arbitration is unavailable"))
	}
	if req == nil {
		req = &pb.CreateEdgesRequest{}
	}
	if err := rejectProtoUnknownFields(req.ProtoReflect()); err != nil {
		return nil, invalidReceiptRequest(err)
	}
	if len(req.GetEdges()) > receiptVertexWALMaxItems {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("Edge Create batch capacity exceeded"))
	}
	if len(req.GetEdges()) == 0 {
		if req.GetReceiptContext() != nil {
			return nil, invalidReceiptRequest(mutationreceipt.ErrInvalidBatch)
		}
		return &pb.CreateEdgesResponse{}, nil
	}
	if proto.Size(req) > receiptVertexWALMaxBytes/2 {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("Edge Create request exceeds durable envelope capacity"))
	}
	original := make([]*pb.Edge, len(req.GetEdges()))
	items := make([]graphcache.EdgeItem[string], len(original))
	digests := make([][32]byte, len(original))
	resources := make([]mutationreceipt.ResourceIdentity, len(original))
	for i, edge := range req.GetEdges() {
		digest, err := edgeCreateDigest(edge, s.namespaceFormat)
		if err != nil {
			return nil, invalidReceiptRequest(fmt.Errorf("edges[%d]: %w", i, err))
		}
		exp, err := prototime.CheckedExpiration(edge.GetExpiration())
		if err != nil {
			return nil, invalidReceiptRequest(err)
		}
		if err := s.validateExpiration(exp); err != nil {
			return nil, err
		}
		resources[i], err = receiptResourceIdentity(s.namespaceFormat, edge.GetTail(), edge.GetHead())
		if err != nil {
			return nil, invalidReceiptRequest(err)
		}
		original[i] = proto.Clone(edge).(*pb.Edge)
		items[i] = graphcache.EdgeItem[string]{Tail: edge.GetTail(), Head: edge.GetHead(), Weight: edge.GetWeight(), Expiration: exp}
		digests[i] = digest
	}
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Edge Create requires an owned staged GraphCache"))
	}
	var runtime *receiptServingRuntime
	var intents []mutationreceipt.Intent
	if req.GetReceiptContext() != nil {
		var release func()
		var err error
		runtime, release, err = s.acquirePublicReceiptRuntime()
		if err != nil {
			return nil, err
		}
		defer release()
		group, ids, err := s.validatePublicReceiptContext(runtime, req.GetReceiptContext(), len(items), "edges")
		if err != nil {
			return nil, err
		}
		intents = make([]mutationreceipt.Intent, len(items))
		for i, id := range ids {
			intents[i] = mutationreceipt.Intent{ID: id, Group: group, Index: uint32(i), Count: uint32(len(items)), Kind: mutationreceipt.CreateEdge, Digest: digests[i], Resource: resources[i]}
		}
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	walAttempted := false
	defer func() {
		if value := recover(); value != nil {
			if walAttempted {
				s.markReceiptCommitFaultLocked()
			}
			panic(value)
		}
	}()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted || s.snapshotInstallFaulted {
		return nil, publicationGapError()
	}
	var storeTx *mutationreceipt.Tx
	if runtime != nil {
		var err error
		storeTx, err = runtime.store.Begin(time.Now())
		if err != nil {
			return nil, receiptStoreError(err)
		}
		defer storeTx.Abort()
		classification, prior, err := storeTx.Classify(intents)
		if err != nil {
			return nil, receiptStoreError(err)
		}
		if classification == mutationreceipt.Duplicate {
			if err := s.authorizeReceiptRows(ctx, prior); err != nil {
				return nil, err
			}
			return edgeCreateReceiptResponse(prior)
		}
		placeholders := make([][]byte, len(items))
		for i := range placeholders {
			placeholders[i] = []byte{byte(pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_ENDPOINT_NOT_LIVE)}
		}
		if err := storeTx.Reserve(placeholders); err != nil {
			return nil, receiptStoreError(err)
		}
		floor, err := receiptClockFloor(storeTx)
		if err != nil {
			return nil, receiptStoreError(err)
		}
		if err := s.clock.RestoreFloor(floor); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	}
	if err := s.prepareLocalMutationLocked(); err != nil {
		return nil, err
	}
	// Conservative preflight reserves batch Edge capacity, without reserving or
	// creating endpoint Vertices. Final outcomes still come from the graph cut.
	if err := s.checkEdgeCapacity(len(items)); err != nil {
		return nil, err
	}
	var ts hlc.Timestamp
	var origin hlc.NodeID
	var seq uint64
	if s.clock != nil && s.log != nil {
		origin = s.clock.NodeID()
		seq = s.origins.LocalSeq(origin) + 1
		ts = s.clock.Now()
	}
	graphTx, err := cache.BeginEdgeCreate(items, ts)
	if err != nil {
		return nil, searchIndexWriteError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	outcomes := make([]pb.CreateEdgeOutcome, len(result.Outcomes))
	for i, outcome := range result.Outcomes {
		outcomes[i] = pb.CreateEdgeOutcome(outcome)
	}
	var receipts []mutationreceipt.Receipt
	if storeTx != nil {
		encoded := make([][]byte, len(outcomes))
		for i, outcome := range outcomes {
			encoded[i] = []byte{byte(outcome)}
		}
		if err := storeTx.ReplaceReservedResults(encoded); err != nil {
			return nil, receiptStoreError(err)
		}
		receipts, err = storeTx.ReservedReceipts()
		if err != nil {
			return nil, receiptStoreError(err)
		}
	}
	if s.log == nil || s.clock == nil {
		if err := ctx.Err(); err != nil {
			return nil, ctxToConnect(err)
		}
		graphTx.Commit()
		return &pb.CreateEdgesResponse{Outcomes: outcomes}, nil
	}
	envelope, err := newEdgeCreateEnvelope(s.namespaceFormat, origin, seq, ts, original, outcomes, receipts, runtime)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := s.validateReplicationFrame(envelope); err != nil {
		return nil, err
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("Edge Create origin sequence drift"))
	}
	defer originTx.Abort()
	if storeTx != nil {
		if err := storeTx.Stage(); err != nil {
			return nil, receiptStoreError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	walAttempted = true
	_, err = s.log.CommitWithPostRingPublication(envelope, ts, func(mutationlog.Entry) {
		if storeTx != nil {
			storeTx.Commit()
		}
		graphTx.Commit()
		originTx.Commit()
	})
	if err != nil {
		var definite *mutationlog.DefiniteWALAbort
		if !errors.As(err, &definite) && !errors.Is(err, mutationlog.ErrClosed) && !errors.Is(err, mutationlog.ErrSeqExhausted) {
			s.markReceiptCommitFaultLocked()
		}
		if errors.Is(err, mutationlog.ErrSeqExhausted) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &pb.CreateEdgesResponse{Outcomes: outcomes}, nil
}

func edgeCreateReceiptResponse(receipts []mutationreceipt.Receipt) (*pb.CreateEdgesResponse, error) {
	outcomes := make([]pb.CreateEdgeOutcome, len(receipts))
	for i, receipt := range receipts {
		if receipt.Kind != mutationreceipt.CreateEdge || len(receipt.Result) != 1 || !validCreateEdgeOutcome(pb.CreateEdgeOutcome(receipt.Result[0])) {
			return nil, connect.NewError(connect.CodeInternal, errors.New("invalid original Edge Create receipt result"))
		}
		outcomes[i] = pb.CreateEdgeOutcome(receipt.Result[0])
	}
	return &pb.CreateEdgesResponse{Outcomes: outcomes}, nil
}

func (s *LanternService) CreateEdge(ctx context.Context, req *pb.CreateEdgeRequest) (*pb.CreateEdgeResponse, error) {
	if req == nil {
		req = &pb.CreateEdgeRequest{}
	}
	if err := rejectProtoUnknownFields(req.ProtoReflect()); err != nil {
		return nil, invalidReceiptRequest(err)
	}
	resp, err := s.CreateEdges(ctx, &pb.CreateEdgesRequest{Edges: []*pb.Edge{req.GetEdge()}, ReceiptContext: req.GetReceiptContext()})
	if err != nil {
		return nil, err
	}
	if len(resp.GetOutcomes()) != 1 || !validCreateEdgeOutcome(resp.GetOutcomes()[0]) {
		return nil, connect.NewError(connect.CodeInternal, errors.New("Edge Create outcome alignment drift"))
	}
	return &pb.CreateEdgeResponse{Outcome: resp.GetOutcomes()[0]}, nil
}

// Create receipt capability is scoped to current Head-derived authority. It is not
// retry authorization; actual calls still check their exact original resources.
func (s *LanternService) advertiseEdgeCreateReceipt(ctx context.Context) bool {
	if s.edgeCreateHA {
		return false
	}
	if !s.dataAuthorization {
		return true
	}
	admission, known := security.AdmissionFromContext(ctx)
	return known && !admission.Access().EdgeCandidateScope(security.EdgeCreate, security.VertexRead).Empty()
}
