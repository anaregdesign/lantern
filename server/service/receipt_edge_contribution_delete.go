package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type edgeContributionDeleteReceiptCoordinator struct {
	service *LanternService
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	store   *mutationreceipt.Store
}

type receiptEdgeContributionDeleteItem struct {
	ID        mutationreceipt.ID
	Tail      string
	Head      string
	ContribID graphcache.ContribID
}

type receiptEdgeContributionDeleteCall struct {
	Group mutationreceipt.GroupID
	Items []receiptEdgeContributionDeleteItem
}

type edgeContributionDeleteReceiptEnvelope struct {
	Mutation            *pb.Mutation
	Origin              hlc.NodeID
	OriginSeq           uint64
	HLC                 hlc.Timestamp
	Epoch               mutationreceipt.Epoch
	PolicyFingerprint   [32]byte
	TombstoneExpiration time.Time
	OriginalKeys        []graphcache.EdgeContributionKey[string]
	Accepted            []graphcache.IndexedEdgeContributionDelete[string]
	Receipts            []mutationreceipt.Receipt
}

func (e *edgeContributionDeleteReceiptEnvelope) GraphMutation() *pb.Mutation {
	return e.Mutation
}

func newEdgeContributionDeleteReceiptCoordinator(
	s *LanternService,
	store *mutationreceipt.Store,
) (*edgeContributionDeleteReceiptCoordinator, error) {
	if s == nil || store == nil || s.log == nil || s.clock == nil || s.origins == nil || s.tombstoneTTL <= 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt contribution Delete requires a log, clock, receipt Store, and causal tombstone retention"))
	}
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt contribution Delete requires a staged GraphCache"))
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.receiptStore != nil && s.receiptStore != store {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt contribution Delete Store differs from the service-bound Store"))
	}
	if s.receiptEdgeContributionDeleteCoordinator != nil {
		return s.receiptEdgeContributionDeleteCoordinator, nil
	}
	s.receiptStore = store
	coordinator := &edgeContributionDeleteReceiptCoordinator{service: s, cache: cache, store: store}
	s.receiptEdgeContributionDeleteCoordinator = coordinator
	return coordinator, nil
}

func (s *LanternService) commitPublicReceiptEdgeContributionDelete(
	ctx context.Context,
	request *pb.DeleteEdgeContributionsRequest,
) (*pb.DeleteEdgeContributionsResponse, error) {
	runtime, release, err := s.acquirePublicReceiptRuntime()
	if err != nil {
		return nil, err
	}
	defer release()

	contributions := request.GetContributions()
	group, ids, err := s.validatePublicReceiptContext(
		runtime, request.GetReceiptContext(), len(contributions), "contributions",
	)
	if err != nil {
		return nil, err
	}
	items := make([]receiptEdgeContributionDeleteItem, len(contributions))
	for i, id := range ids {
		key := contributions[i]
		if key == nil {
			return nil, invalidReceiptRequest(fmt.Errorf("contributions[%d] is nil", i))
		}
		if len(key.GetContribId()) != len(graphcache.ContribID{}) {
			return nil, invalidReceiptRequest(fmt.Errorf("contributions[%d].contrib_id must be 24 bytes", i))
		}
		var contribID graphcache.ContribID
		copy(contribID[:], key.GetContribId())
		items[i] = receiptEdgeContributionDeleteItem{
			ID: id, Tail: key.GetTail(), Head: key.GetHead(), ContribID: contribID,
		}
	}
	return s.receiptEdgeContributionDeleteCoordinator.Commit(ctx, receiptEdgeContributionDeleteCall{
		Group: group, Items: items,
	})
}

func edgeContributionDeleteDigest(key graphcache.EdgeContributionKey[string]) [32]byte {
	canonical := make([]byte, 0, 1+8+len(key.Tail)+8+len(key.Head)+8+len(key.ContribID))
	canonical = append(canonical, byte(mutationreceipt.DeleteEdgeContribution))
	canonical = appendReceiptCanonicalString(canonical, key.Tail)
	canonical = appendReceiptCanonicalString(canonical, key.Head)
	canonical = appendReceiptCanonicalBytes(canonical, key.ContribID[:])
	return mutationreceipt.IntentDigest(canonical)
}

func prepareEdgeContributionDeleteReceiptCall(
	call receiptEdgeContributionDeleteCall,
) ([]graphcache.EdgeContributionKey[string], []mutationreceipt.Intent, error) {
	if len(call.Items) == 0 || len(call.Items) > math.MaxInt32 ||
		call.Group == (mutationreceipt.GroupID{}) {
		return nil, nil, invalidReceiptRequest(mutationreceipt.ErrInvalidBatch)
	}
	keys := make([]graphcache.EdgeContributionKey[string], len(call.Items))
	intents := make([]mutationreceipt.Intent, len(call.Items))
	for i, item := range call.Items {
		key := graphcache.EdgeContributionKey[string]{
			Tail: item.Tail, Head: item.Head, ContribID: item.ContribID,
		}
		if key.Tail == "" || key.Head == "" || !utf8.ValidString(key.Tail) ||
			!utf8.ValidString(key.Head) || key.ContribID.IsZero() {
			return nil, nil, invalidReceiptRequest(fmt.Errorf(
				"contributions[%d] must have nonempty UTF-8 endpoints and a nonzero 24-byte ContribID", i,
			))
		}
		keys[i] = key
		intents[i] = mutationreceipt.Intent{
			ID: item.ID, Group: call.Group, Index: uint32(i),
			Count: uint32(len(call.Items)), Kind: mutationreceipt.DeleteEdgeContribution,
			Digest: edgeContributionDeleteDigest(key),
		}
	}
	return keys, intents, nil
}

func receiptEdgeContributionDeleteResponse(
	receipts []mutationreceipt.Receipt,
) (*pb.DeleteEdgeContributionsResponse, error) {
	outcomes := make([]bool, len(receipts))
	for i, receipt := range receipts {
		if receipt.Kind != mutationreceipt.DeleteEdgeContribution ||
			len(receipt.Result) != 1 || receipt.Result[0] > 1 {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt contribution Delete item %d has invalid original result", i))
		}
		outcomes[i] = receipt.Result[0] == 1
	}
	deleted, err := checkedDeleteOutcomes(outcomes, len(receipts))
	if err != nil {
		return nil, err
	}
	return &pb.DeleteEdgeContributionsResponse{Deleted: deleted, Existed: outcomes}, nil
}

func (c *edgeContributionDeleteReceiptCoordinator) Commit(
	ctx context.Context,
	call receiptEdgeContributionDeleteCall,
) (*pb.DeleteEdgeContributionsResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	keys, intents, err := prepareEdgeContributionDeleteReceiptCall(call)
	if err != nil {
		return nil, err
	}
	if err := validateReceiptEdgeContributionDeleteWALRequestCapacity(keys); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
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
	if err := s.prepareLocalMutationLocked(); err != nil {
		return nil, err
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
		return receiptEdgeContributionDeleteResponse(prior)
	}
	placeholders := make([][]byte, len(keys))
	for i := range placeholders {
		placeholders[i] = []byte{0}
	}
	if err := storeTx.Reserve(placeholders); err != nil {
		return nil, receiptStoreError(err)
	}
	clockFloor, err := receiptClockFloor(storeTx)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("receipt contribution Delete clock floor: %w", err))
	}
	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1
	ts, expiration, err := s.sampleDeleteStamp()
	if err != nil {
		return nil, err
	}
	graphTx, err := c.cache.PrepareEdgeContributionDelete(keys, ts, expiration)
	if err != nil {
		return nil, writeError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	deleted, err := checkedDeleteOutcomes(result.Existed, len(keys))
	if err != nil {
		return nil, err
	}
	results := make([][]byte, len(keys))
	for i, existed := range result.Existed {
		if existed {
			results[i] = []byte{1}
		} else {
			results[i] = []byte{0}
		}
	}
	if err := storeTx.ReplaceReservedResults(results); err != nil {
		return nil, receiptStoreError(err)
	}
	receipts, err := storeTx.ReservedReceipts()
	if err != nil {
		return nil, receiptStoreError(err)
	}
	accepted := make([]*pb.EdgeContributionKey, len(result.Accepted))
	for i, item := range result.Accepted {
		if item.Index < 0 || item.Index >= len(keys) || item.Key != keys[item.Index] {
			return nil, connect.NewError(connect.CodeInternal,
				errors.New("staged contribution Delete accepted index drift"))
		}
		accepted[i] = receiptEdgeContributionDeleteWireKey(item.Key)
	}
	mutation := &pb.Mutation{
		Origin: append([]byte(nil), origin[:]...), Seq: seq, Hlc: hlcToProto(ts),
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgeContributions{
			DeleteEdgeContributions: &pb.DeleteEdgeContributionsRequest{Contributions: accepted},
		}},
		TombstoneExpiration: timestamppb.New(expiration),
	}
	envelope := &edgeContributionDeleteReceiptEnvelope{
		Mutation: mutation, Origin: origin, OriginSeq: seq, HLC: ts,
		Epoch: c.store.Epoch(), PolicyFingerprint: c.store.PolicyFingerprint(),
		TombstoneExpiration: expiration,
		OriginalKeys:        append([]graphcache.EdgeContributionKey[string](nil), keys...),
		Accepted:            append([]graphcache.IndexedEdgeContributionDelete[string](nil), result.Accepted...),
		Receipts:            receipts,
	}
	if _, err := validateReceiptEdgeContributionDeleteWALEnvelope(envelope); err != nil {
		if errors.Is(err, errReceiptEdgeContributionDeleteWALCapacity) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
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
			errors.New("receipt contribution Delete could not stage contiguous origin seq"))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if err := storeTx.Stage(); err != nil {
		return nil, receiptStoreError(err)
	}
	graphTx.Apply()
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
		if errors.Is(err, mutationlog.ErrSeqExhausted) ||
			errors.Is(err, errReceiptEdgeContributionDeleteWALCapacity) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &pb.DeleteEdgeContributionsResponse{Deleted: deleted, Existed: result.Existed}, nil
}

func (c *edgeContributionDeleteReceiptCoordinator) validateReplicatedEnvelope(
	e *edgeContributionDeleteReceiptEnvelope,
) error {
	if e == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("replication contribution Delete receipt envelope is nil"))
	}
	if e.Epoch != c.store.Epoch() || e.PolicyFingerprint != c.store.PolicyFingerprint() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("replication contribution Delete receipt epoch or policy differs from the local Store"))
	}
	localWall := c.service.remoteValidationWall()
	if time.Unix(0, e.HLC.WallNs).After(localWall.Add(hlc.DefaultMaxSkew)) {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("replication contribution Delete origin HLC exceeds maximum clock skew"))
	}
	if err := c.store.ValidateCommitted(e.Receipts, e.HLC.WallNs/int64(time.Millisecond)); err != nil {
		return receiptStoreError(err)
	}
	if _, err := c.service.validateIncomingTombstoneExpirationAt(e.Mutation, localWall); err != nil {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("replication contribution Delete tombstone: %w", err))
	}
	return nil
}

func (c *edgeContributionDeleteReceiptCoordinator) commitReplicated(
	ctx context.Context,
	origin hlc.NodeID,
	seq uint64,
	ts hlc.Timestamp,
	pending *pendingMutation,
) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	e, ok := pending.receipt.(*edgeContributionDeleteReceiptEnvelope)
	if !ok || e == nil || e.Origin != origin || e.OriginSeq != seq || e.HLC != ts {
		return connect.NewError(connect.CodeInternal,
			errors.New("replication contribution Delete pending identity drift"))
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
	if err := s.validateReplicationRelayFrame(e); err != nil {
		return err
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
	graphTx, err := c.cache.PrepareReplicatedEdgeContributionDelete(
		e.OriginalKeys, ts, e.TombstoneExpiration,
	)
	if err != nil {
		return writeError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	localEnvelope := &edgeContributionDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts,
		Epoch: e.Epoch, PolicyFingerprint: e.PolicyFingerprint,
		TombstoneExpiration: e.TombstoneExpiration,
		OriginalKeys:        append([]graphcache.EdgeContributionKey[string](nil), e.OriginalKeys...),
		Accepted:            append([]graphcache.IndexedEdgeContributionDelete[string](nil), result.Accepted...),
		Receipts:            cloneMutationReceipts(e.Receipts),
	}
	localEnvelope.Mutation = receiptEdgeContributionDeleteWALMutation(localEnvelope)
	if _, err := validateReceiptEdgeContributionDeleteWALEnvelope(localEnvelope); err != nil {
		if errors.Is(err, errReceiptEdgeContributionDeleteWALCapacity) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication contribution Delete receipt relay envelope: %w", err))
	}
	if err := s.validateReplicationFrame(localEnvelope); err != nil {
		return err
	}
	if prior, ok := pending.receiptWAL.(*edgeContributionDeleteReceiptEnvelope); ok &&
		sameReceiptEdgeContributionDeleteIntent(prior, localEnvelope) &&
		slices.Equal(prior.Accepted, localEnvelope.Accepted) {
		localEnvelope = prior
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication contribution Delete could not stage origin %x seq %d", origin, seq))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	if err := storeTx.Stage(); err != nil {
		return receiptStoreError(err)
	}
	graphTx.Apply()
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
		if errors.Is(err, mutationlog.ErrSeqExhausted) ||
			errors.Is(err, errReceiptEdgeContributionDeleteWALCapacity) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		s.markReceiptCommitFaultLocked()
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication contribution Delete receipt clock floor: %w", err))
	}
	if err := s.clock.RestoreFloor(ts); err != nil {
		s.markReceiptCommitFaultLocked()
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication contribution Delete origin clock floor: %w", err))
	}
	return nil
}

func maximalReceiptEdgeContributionDeleteEnvelope(
	e *edgeContributionDeleteReceiptEnvelope,
) *edgeContributionDeleteReceiptEnvelope {
	maximal := &edgeContributionDeleteReceiptEnvelope{
		Origin: e.Origin, OriginSeq: e.OriginSeq, HLC: e.HLC,
		Epoch: e.Epoch, PolicyFingerprint: e.PolicyFingerprint,
		TombstoneExpiration: e.TombstoneExpiration,
		OriginalKeys:        append([]graphcache.EdgeContributionKey[string](nil), e.OriginalKeys...),
		Accepted:            make([]graphcache.IndexedEdgeContributionDelete[string], len(e.OriginalKeys)),
		Receipts:            cloneMutationReceipts(e.Receipts),
	}
	for i, key := range maximal.OriginalKeys {
		maximal.Accepted[i] = graphcache.IndexedEdgeContributionDelete[string]{Index: i, Key: key}
	}
	maximal.Mutation = receiptEdgeContributionDeleteWALMutation(maximal)
	return maximal
}
