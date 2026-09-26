package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
)

type edgeAddReceiptCoordinator struct {
	service *LanternService
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	store   *mutationreceipt.Store
}

type receiptEdgeAddItem struct {
	ID        mutationreceipt.ID
	Edge      *pb.Edge
	ContribID graphcache.ContribID
}

type receiptEdgeAddCall struct {
	Group mutationreceipt.GroupID
	Items []receiptEdgeAddItem
}

func (s *LanternService) commitPublicReceiptEdgeAdd(
	ctx context.Context,
	request *pb.AddEdgesRequest,
) (*pb.AddEdgesResponse, error) {
	runtime, release, err := s.acquirePublicReceiptRuntime()
	if err != nil {
		return nil, err
	}
	defer release()

	edges := request.GetEdges()
	contribIDs := request.GetContribIds()
	if len(edges) > receiptVertexWALMaxItems {
		return nil, invalidReceiptRequest(fmt.Errorf(
			"receipt Edge Add batch exceeds %d items",
			receiptVertexWALMaxItems,
		))
	}
	if len(contribIDs) != len(edges) {
		return nil, invalidReceiptRequest(errors.New(
			"receipt ContribIDs must be explicit and index-aligned with every edge",
		))
	}
	group, ids, err := s.validatePublicReceiptContext(
		runtime,
		request.GetReceiptContext(),
		len(edges),
		"edges",
	)
	if err != nil {
		return nil, err
	}

	items := make([]receiptEdgeAddItem, len(edges))
	seenContribs := make(map[graphcache.ContribID]struct{}, len(edges))
	for i, id := range ids {
		if len(contribIDs[i]) != len(graphcache.ContribID{}) {
			return nil, invalidReceiptRequest(fmt.Errorf(
				"contrib_ids[%d] must be exactly %d bytes",
				i,
				len(graphcache.ContribID{}),
			))
		}
		var contribID graphcache.ContribID
		copy(contribID[:], contribIDs[i])
		if contribID.IsZero() {
			return nil, invalidReceiptRequest(fmt.Errorf("contrib_ids[%d] must be nonzero", i))
		}
		if _, duplicate := seenContribs[contribID]; duplicate {
			return nil, invalidReceiptRequest(fmt.Errorf("contrib_ids[%d] duplicates an earlier item", i))
		}
		seenContribs[contribID] = struct{}{}
		edge := edges[i]
		if edge == nil {
			return nil, invalidReceiptRequest(fmt.Errorf("edges[%d] is nil", i))
		}
		items[i] = receiptEdgeAddItem{ID: id, Edge: edge, ContribID: contribID}
	}
	return s.receiptEdgeAddCoordinator.Commit(ctx, receiptEdgeAddCall{
		Group: group,
		Items: items,
	})
}

func newEdgeAddReceiptCoordinator(
	s *LanternService,
	store *mutationreceipt.Store,
) (*edgeAddReceiptCoordinator, error) {
	if s == nil || store == nil || s.log == nil || s.clock == nil || s.origins == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Edge Add requires a log, clock, receipt store, and origin tracker"))
	}
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Edge Add requires a staged GraphCache"))
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.receiptStore != nil && s.receiptStore != store {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Edge Add Store differs from the service-bound Store"))
	}
	if s.receiptEdgeAddCoordinator != nil {
		return s.receiptEdgeAddCoordinator, nil
	}
	s.receiptStore = store
	coordinator := &edgeAddReceiptCoordinator{service: s, cache: cache, store: store}
	s.receiptEdgeAddCoordinator = coordinator
	return coordinator, nil
}

func prepareEdgeAddReceiptCall(
	s *LanternService,
	call receiptEdgeAddCall,
) ([]*pb.Edge, []graphcache.EdgeItem[string], []mutationreceipt.Intent, error) {
	if s == nil || len(call.Items) == 0 || len(call.Items) > receiptVertexWALMaxItems ||
		call.Group == (mutationreceipt.GroupID{}) {
		return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, mutationreceipt.ErrInvalidBatch)
	}
	original := make([]*pb.Edge, len(call.Items))
	for i, item := range call.Items {
		original[i] = item.Edge
	}
	if err := validateReceiptEdgeAddWALRequestCapacity(original); err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeResourceExhausted, err)
	}
	items := make([]graphcache.EdgeItem[string], len(call.Items))
	intents := make([]mutationreceipt.Intent, len(call.Items))
	for i, item := range call.Items {
		digest, err := receiptEdgeAddDigest(item.Edge, item.ContribID)
		if err != nil {
			return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		expiration, err := prototime.CheckedExpiration(item.Edge.GetExpiration())
		if err != nil {
			return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if err := s.validateExpiration(expiration); err != nil {
			return nil, nil, nil, err
		}
		items[i] = graphcache.EdgeItem[string]{
			Tail: item.Edge.GetTail(), Head: item.Edge.GetHead(), Weight: item.Edge.GetWeight(),
			Expiration: expiration, ContribID: item.ContribID,
		}
		var receiptContrib mutationreceipt.ContribID
		copy(receiptContrib[:], item.ContribID[:])
		intents[i] = mutationreceipt.Intent{
			ID: item.ID, Group: call.Group, Index: uint32(i), Count: uint32(len(call.Items)),
			Kind: mutationreceipt.AddEdge, Digest: digest,
			HasContrib: true, ContribID: receiptContrib,
		}
	}
	for i, edge := range original {
		cloned := proto.Clone(edge).(*pb.Edge)
		original[i] = cloned
		items[i].Tail = cloned.GetTail()
		items[i].Head = cloned.GetHead()
	}
	return original, items, intents, nil
}

func receiptEdgeAddResponse(receipts []mutationreceipt.Receipt) (*pb.AddEdgesResponse, error) {
	effective := make([]float32, len(receipts))
	for i, receipt := range receipts {
		if receipt.Kind != mutationreceipt.AddEdge || len(receipt.Result) != 4 {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt Edge Add item %d has invalid original result", i))
		}
		effective[i] = math.Float32frombits(binary.BigEndian.Uint32(receipt.Result))
	}
	return &pb.AddEdgesResponse{Written: int32(len(receipts)), EffectiveWeights: effective}, nil
}

func receiptEdgeAddResults(effective []float32) [][]byte {
	results := make([][]byte, len(effective))
	for i, weight := range effective {
		results[i] = make([]byte, 4)
		binary.BigEndian.PutUint32(results[i], math.Float32bits(weight))
	}
	return results
}

func edgeAddAcceptedIndexes(accepted []graphcache.IndexedEdgeAdd[string]) ([]uint32, error) {
	indexes := make([]uint32, len(accepted))
	previous := -1
	for i, item := range accepted {
		if item.Index <= previous {
			return nil, errors.New("staged Edge Add accepted index drift")
		}
		indexes[i] = uint32(item.Index)
		previous = item.Index
	}
	return indexes, nil
}

func (c *edgeAddReceiptCoordinator) Commit(
	ctx context.Context,
	call receiptEdgeAddCall,
) (*pb.AddEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	original, items, intents, err := prepareEdgeAddReceiptCall(c.service, call)
	if err != nil {
		return nil, err
	}
	s := c.service
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	walAttempted := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if walAttempted {
				s.markReceiptCommitFaultLocked()
			}
			panic(recovered)
		}
	}()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return nil, publicationGapError()
	}
	storeTx, err := c.store.Begin(time.Now())
	if err != nil {
		return nil, receiptStoreError(err)
	}
	defer storeTx.Abort()
	classification, prior, err := storeTx.Classify(intents)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if classification == mutationreceipt.Duplicate {
		return receiptEdgeAddResponse(prior)
	}
	placeholders := make([][]byte, len(items))
	for i := range placeholders {
		placeholders[i] = make([]byte, 4)
	}
	if err := storeTx.Reserve(placeholders); err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.prepareLocalMutationLocked(); err != nil {
		return nil, err
	}
	if err := s.checkVertexCapacity(2 * len(items)); err != nil {
		return nil, err
	}
	if err := s.checkEdgeCapacity(len(items)); err != nil {
		return nil, err
	}
	clockFloor, err := receiptClockFloor(storeTx)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("receipt Edge Add clock floor: %w", err))
	}
	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1
	ts := s.clock.Now()
	graphTx, err := c.cache.BeginEdgeAdd(items, ts)
	if err != nil {
		return nil, searchIndexWriteError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	if len(result.Effective) != len(items) {
		return nil, connect.NewError(connect.CodeInternal,
			errors.New("staged Edge Add result alignment drift"))
	}
	results := receiptEdgeAddResults(result.Effective)
	if err := storeTx.ReplaceReservedResults(results); err != nil {
		return nil, receiptStoreError(err)
	}
	receipts, err := storeTx.ReservedReceipts()
	if err != nil {
		return nil, receiptStoreError(err)
	}
	acceptedIndexes, err := edgeAddAcceptedIndexes(result.Accepted)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	contribIDs := make([]graphcache.ContribID, len(items))
	for i := range items {
		contribIDs[i] = items[i].ContribID
	}
	envelope := &graphAddEffectEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts,
		Epoch: c.store.Epoch(), PolicyFingerprint: c.store.PolicyFingerprint(),
		Original: cloneReceiptEdges(original), ContribIDs: contribIDs,
		AcceptedIndexes: acceptedIndexes, Receipts: receipts,
	}
	envelope.Mutation = receiptEdgeAddMutation(envelope)
	if err := validateGraphAddEffectEnvelope(envelope); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := s.validateReplicationFrame(envelope); err != nil {
		return nil, err
	}
	if err := s.validateReplicationRelayFrame(envelope); err != nil {
		return nil, err
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal,
			errors.New("receipt Edge Add could not stage contiguous origin seq"))
	}
	defer originTx.Abort()
	if err := storeTx.Stage(); err != nil {
		return nil, receiptStoreError(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	walAttempted = true
	_, err = s.log.CommitWithPostRingPublication(envelope, ts, func(mutationlog.Entry) {
		storeTx.Commit()
		graphTx.Commit()
		originTx.Commit()
	})
	if err != nil {
		var definite *mutationlog.DefiniteWALAbort
		if !errors.As(err, &definite) && !errors.Is(err, mutationlog.ErrClosed) &&
			!errors.Is(err, mutationlog.ErrSeqExhausted) {
			s.markReceiptCommitFaultLocked()
		}
		if errors.Is(err, mutationlog.ErrSeqExhausted) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &pb.AddEdgesResponse{
		Written: int32(len(items)), EffectiveWeights: append([]float32(nil), result.Effective...),
	}, nil
}

func (c *edgeAddReceiptCoordinator) validateReplicatedEnvelope(e *graphAddEffectEnvelope) error {
	if _, err := validateReceiptEdgeAddEnvelope(e); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if e.Epoch != c.store.Epoch() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("replication receipt epoch differs from the local Store"))
	}
	if e.PolicyFingerprint != c.store.PolicyFingerprint() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("replication receipt policy differs from the local Store"))
	}
	localWall := c.service.remoteValidationWall()
	if time.Unix(0, e.HLC.WallNs).After(localWall.Add(hlc.DefaultMaxSkew)) {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("replication receipt origin HLC exceeds maximum clock skew"))
	}
	if err := c.store.ValidateCommitted(e.Receipts, e.HLC.WallNs/int64(time.Millisecond)); err != nil {
		return receiptStoreError(err)
	}
	return nil
}

func receiptEdgeAddGraphItems(e *graphAddEffectEnvelope) []graphcache.EdgeItem[string] {
	items := make([]graphcache.EdgeItem[string], len(e.Original))
	for i, edge := range e.Original {
		items[i] = graphcache.EdgeItem[string]{
			Tail: edge.GetTail(), Head: edge.GetHead(), Weight: edge.GetWeight(),
			Expiration: prototime.Expiration(edge.GetExpiration()),
			ContribID:  e.ContribIDs[i],
		}
	}
	return items
}

func cloneReceiptEdges(edges []*pb.Edge) []*pb.Edge {
	cloned := make([]*pb.Edge, len(edges))
	for i, edge := range edges {
		if edge != nil {
			cloned[i] = proto.Clone(edge).(*pb.Edge)
		}
	}
	return cloned
}

func (c *edgeAddReceiptCoordinator) commitReplicated(
	ctx context.Context,
	origin hlc.NodeID,
	seq uint64,
	ts hlc.Timestamp,
	pending *pendingMutation,
) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	e, ok := pending.receipt.(*graphAddEffectEnvelope)
	if !ok || e == nil || !e.receiptBearing() ||
		e.Origin != origin || e.OriginSeq != seq || e.HLC != ts {
		return connect.NewError(connect.CodeInternal,
			errors.New("replication Edge Add receipt pending identity drift"))
	}
	if err := c.service.validateReplicationRelayFrame(e); err != nil {
		return err
	}
	s := c.service
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	walAttempted := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if walAttempted {
				s.markReceiptCommitFaultLocked()
			}
			panic(recovered)
		}
	}()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return publicationGapError()
	}
	storeTx, err := c.store.Begin(time.Now())
	if err != nil {
		return receiptStoreError(err)
	}
	defer storeTx.Abort()
	if err := storeTx.PrepareCommitted(e.Receipts, ts.WallNs/int64(time.Millisecond)); err != nil {
		return receiptStoreError(err)
	}
	clockFloor, err := receiptClockFloor(storeTx)
	if err != nil {
		return receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Edge Add receipt clock floor: %w", err))
	}
	graphTx, err := c.cache.BeginReplicatedEdgeAdd(receiptEdgeAddGraphItems(e), ts)
	if err != nil {
		return searchIndexWriteError(err)
	}
	defer graphTx.Abort()
	acceptedIndexes, err := edgeAddAcceptedIndexes(graphTx.Result().Accepted)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	localEnvelope := &graphAddEffectEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts, Epoch: e.Epoch,
		PolicyFingerprint: e.PolicyFingerprint,
		Original:          cloneReceiptEdges(e.Original),
		ContribIDs:        append([]graphcache.ContribID(nil), e.ContribIDs...),
		AcceptedIndexes:   acceptedIndexes,
		Receipts:          cloneMutationReceipts(e.Receipts),
	}
	localEnvelope.Mutation = receiptEdgeAddMutation(localEnvelope)
	if err := validateGraphAddEffectEnvelope(localEnvelope); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Edge Add receipt relay envelope: %w", err))
	}
	if err := s.validateReplicationFrame(localEnvelope); err != nil {
		return err
	}
	if prior, ok := pending.receiptWAL.(*graphAddEffectEnvelope); ok &&
		prior.receiptBearing() && slices.Equal(prior.AcceptedIndexes, localEnvelope.AcceptedIndexes) {
		localEnvelope = prior
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Edge Add receipt could not stage origin %x seq %d", origin, seq))
	}
	defer originTx.Abort()
	if err := storeTx.Stage(); err != nil {
		return receiptStoreError(err)
	}
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	pending.receiptWAL = localEnvelope
	walAttempted = true
	_, err = s.log.CommitWithPostRingPublication(localEnvelope, ts, func(mutationlog.Entry) {
		storeTx.Commit()
		graphTx.Commit()
		originTx.Commit()
	})
	if err != nil {
		var definite *mutationlog.DefiniteWALAbort
		if !errors.As(err, &definite) && !errors.Is(err, mutationlog.ErrClosed) &&
			!errors.Is(err, mutationlog.ErrSeqExhausted) {
			s.markReceiptCommitFaultLocked()
		}
		if errors.Is(err, mutationlog.ErrSeqExhausted) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}
	if err := s.clock.RestoreFloor(ts); err != nil {
		s.markReceiptCommitFaultLocked()
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Edge Add receipt origin clock floor: %w", err))
	}
	return nil
}

func (c *edgeAddReceiptCoordinator) Lookup(
	id mutationreceipt.ID,
	now time.Time,
) (mutationreceipt.Status, mutationreceipt.Receipt, error) {
	var status mutationreceipt.Status
	var receipt mutationreceipt.Receipt
	var lookupErr error
	err := c.service.withCommittedView(func() error {
		status, receipt, lookupErr = c.store.Lookup(id, now)
		return nil
	})
	if err != nil {
		return 0, mutationreceipt.Receipt{}, err
	}
	if lookupErr != nil {
		return 0, mutationreceipt.Receipt{}, receiptStoreError(lookupErr)
	}
	return status, receipt, nil
}
