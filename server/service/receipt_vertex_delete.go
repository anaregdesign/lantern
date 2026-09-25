package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

type vertexDeleteReceiptCoordinator struct {
	service *LanternService
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	store   *mutationreceipt.Store
}

type receiptVertexDeleteItem struct {
	ID  mutationreceipt.ID
	Key string
}

type receiptVertexDeleteCall struct {
	Group mutationreceipt.GroupID
	Items []receiptVertexDeleteItem
}

type vertexDeleteReceiptEnvelope struct {
	Mutation            *pb.Mutation
	Origin              hlc.NodeID
	OriginSeq           uint64
	HLC                 hlc.Timestamp
	Epoch               mutationreceipt.Epoch
	PolicyFingerprint   [32]byte
	TombstoneExpiration time.Time
	OriginalKeys        []string
	Accepted            []graphcache.IndexedVertexDelete[string]
	Receipts            []mutationreceipt.Receipt
}

func (e *vertexDeleteReceiptEnvelope) GraphMutation() *pb.Mutation { return e.Mutation }

func newVertexDeleteReceiptCoordinator(
	s *LanternService,
	store *mutationreceipt.Store,
) (*vertexDeleteReceiptCoordinator, error) {
	if s == nil || store == nil || s.log == nil || s.clock == nil || s.origins == nil ||
		s.tombstoneTTL <= 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Vertex Delete requires a log, clock, receipt store, and causal tombstone retention"))
	}
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Vertex Delete requires a staged GraphCache"))
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.receiptStore != nil && s.receiptStore != store {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Vertex Delete Store differs from the service-bound Store"))
	}
	if s.receiptVertexDeleteCoordinator != nil {
		return s.receiptVertexDeleteCoordinator, nil
	}
	s.receiptStore = store
	coordinator := &vertexDeleteReceiptCoordinator{service: s, cache: cache, store: store}
	s.receiptVertexDeleteCoordinator = coordinator
	return coordinator, nil
}

func vertexDeleteDigest(key string) [32]byte {
	canonical := []byte{byte(mutationreceipt.DeleteVertex)}
	canonical = appendReceiptCanonicalString(canonical, key)
	return mutationreceipt.IntentDigest(canonical)
}

func prepareVertexDeleteReceiptCall(
	call receiptVertexDeleteCall,
) ([]string, []mutationreceipt.Intent, error) {
	if len(call.Items) == 0 || len(call.Items) > receiptVertexWALMaxItems ||
		call.Group == (mutationreceipt.GroupID{}) {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, mutationreceipt.ErrInvalidBatch)
	}
	keys := make([]string, len(call.Items))
	intents := make([]mutationreceipt.Intent, len(call.Items))
	for i, item := range call.Items {
		if item.Key == "" || !utf8.ValidString(item.Key) || len(item.Key) > receiptVertexWALMaxBytes {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("Vertex Delete identity must be nonempty UTF-8"))
		}
		keys[i] = item.Key
		intents[i] = mutationreceipt.Intent{
			ID: item.ID, Group: call.Group, Index: uint32(i), Count: uint32(len(call.Items)),
			Kind: mutationreceipt.DeleteVertex, Digest: vertexDeleteDigest(item.Key),
		}
	}
	return keys, intents, nil
}

func receiptVertexDeleteResponse(
	receipts []mutationreceipt.Receipt,
) (*pb.DeleteVerticesResponse, error) {
	outcomes := make([]bool, len(receipts))
	for i, receipt := range receipts {
		if len(receipt.Result) != 1 || receipt.Result[0] > 1 {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt Vertex Delete item %d has invalid original result", i))
		}
		outcomes[i] = receipt.Result[0] == 1
	}
	deleted, err := checkedDeleteOutcomes(outcomes, len(receipts))
	if err != nil {
		return nil, err
	}
	return &pb.DeleteVerticesResponse{Deleted: deleted, Existed: outcomes}, nil
}

func (c *vertexDeleteReceiptCoordinator) Commit(
	ctx context.Context,
	call receiptVertexDeleteCall,
) (*pb.DeleteVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	keys, intents, err := prepareVertexDeleteReceiptCall(call)
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
		return receiptVertexDeleteResponse(prior)
	}
	placeholders := make([][]byte, len(keys))
	for i := range placeholders {
		placeholders[i] = []byte{0}
	}
	if err := storeTx.Reserve(placeholders); err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.prepareLocalMutationLocked(); err != nil {
		return nil, err
	}
	clockFloor, err := receiptClockFloor(storeTx)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("receipt Vertex Delete clock floor: %w", err))
	}
	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1
	ts, expiration, err := s.sampleDeleteStamp()
	if err != nil {
		return nil, err
	}
	graphTx, err := c.cache.BeginVertexDelete(keys, ts, expiration)
	if err != nil {
		return nil, searchIndexWriteError(err)
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
	envelope := &vertexDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts,
		Epoch: c.store.Epoch(), PolicyFingerprint: c.store.PolicyFingerprint(),
		TombstoneExpiration: expiration,
		OriginalKeys:        append([]string(nil), keys...),
		Accepted:            append([]graphcache.IndexedVertexDelete[string](nil), result.Accepted...),
		Receipts:            receipts,
	}
	envelope.Mutation = receiptVertexDeleteGraphMutation(envelope)
	if _, err := validateReceiptVertexDeleteWALEnvelope(envelope); err != nil {
		if errors.Is(err, errReceiptVertexDeleteWireCapacity) {
			return nil, s.replicationFrameCapacityError(err)
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
			errors.New("receipt Vertex Delete could not stage contiguous origin seq"))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if err := storeTx.Stage(); err != nil {
		return nil, receiptStoreError(err)
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
	return &pb.DeleteVerticesResponse{Deleted: deleted, Existed: result.Existed}, nil
}

func (c *vertexDeleteReceiptCoordinator) validateReplicatedEnvelope(
	e *vertexDeleteReceiptEnvelope,
) error {
	if e == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("replication receipt envelope is nil"))
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
	if _, err := c.service.validateIncomingTombstoneExpirationAt(e.Mutation, localWall); err != nil {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("replication receipt tombstone: %w", err))
	}
	return nil
}

func (c *vertexDeleteReceiptCoordinator) commitReplicated(
	ctx context.Context,
	origin hlc.NodeID,
	seq uint64,
	ts hlc.Timestamp,
	pending *pendingMutation,
) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	e, ok := pending.receipt.(*vertexDeleteReceiptEnvelope)
	if !ok || e == nil || e.Origin != origin || e.OriginSeq != seq || e.HLC != ts {
		return connect.NewError(connect.CodeInternal,
			errors.New("replication Vertex Delete receipt pending identity drift"))
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
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Vertex Delete receipt clock floor: %w", err))
	}
	graphTx, err := c.cache.BeginReplicatedVertexDelete(e.OriginalKeys, ts, e.TombstoneExpiration)
	if err != nil {
		return searchIndexWriteError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	localEnvelope := &vertexDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts, Epoch: e.Epoch,
		PolicyFingerprint: e.PolicyFingerprint, TombstoneExpiration: e.TombstoneExpiration,
		OriginalKeys: append([]string(nil), e.OriginalKeys...),
		Accepted:     append([]graphcache.IndexedVertexDelete[string](nil), result.Accepted...),
		Receipts:     cloneMutationReceipts(e.Receipts),
	}
	localEnvelope.Mutation = receiptVertexDeleteGraphMutation(localEnvelope)
	if _, err := validateReceiptVertexDeleteWALEnvelope(localEnvelope); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Vertex Delete receipt relay envelope: %w", err))
	}
	if err := s.validateReplicationFrame(localEnvelope); err != nil {
		return err
	}
	if prior, ok := pending.receiptWAL.(*vertexDeleteReceiptEnvelope); ok &&
		slices.Equal(prior.Accepted, localEnvelope.Accepted) {
		localEnvelope = prior
	}

	if err := storeTx.Stage(); err != nil {
		return receiptStoreError(err)
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Vertex Delete receipt could not stage origin %x seq %d", origin, seq))
	}
	defer originTx.Abort()
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
			fmt.Errorf("replication Vertex Delete receipt origin clock floor: %w", err))
	}
	return nil
}

func maximalReceiptVertexDeleteEnvelope(
	e *vertexDeleteReceiptEnvelope,
) *vertexDeleteReceiptEnvelope {
	maximal := &vertexDeleteReceiptEnvelope{
		Origin:              e.Origin,
		OriginSeq:           e.OriginSeq,
		HLC:                 e.HLC,
		Epoch:               e.Epoch,
		PolicyFingerprint:   e.PolicyFingerprint,
		TombstoneExpiration: e.TombstoneExpiration,
		OriginalKeys:        append([]string(nil), e.OriginalKeys...),
		Accepted:            make([]graphcache.IndexedVertexDelete[string], len(e.OriginalKeys)),
		Receipts:            cloneMutationReceipts(e.Receipts),
	}
	for i, key := range maximal.OriginalKeys {
		maximal.Accepted[i] = graphcache.IndexedVertexDelete[string]{Index: i, Key: key}
	}
	maximal.Mutation = receiptVertexDeleteGraphMutation(maximal)
	return maximal
}

func (c *vertexDeleteReceiptCoordinator) Lookup(
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

func sameReceiptVertexDeleteIntent(a, b *vertexDeleteReceiptEnvelope) bool {
	if a == nil || b == nil || a.Origin != b.Origin || a.OriginSeq != b.OriginSeq ||
		a.HLC != b.HLC || a.Epoch != b.Epoch || a.PolicyFingerprint != b.PolicyFingerprint ||
		!a.TombstoneExpiration.Equal(b.TombstoneExpiration) ||
		!slices.Equal(a.OriginalKeys, b.OriginalKeys) || len(a.Receipts) != len(b.Receipts) {
		return false
	}
	for i := range a.Receipts {
		ar, br := a.Receipts[i], b.Receipts[i]
		if ar.Intent != br.Intent || ar.DeadlineMillis != br.DeadlineMillis ||
			!bytes.Equal(ar.Result, br.Result) {
			return false
		}
	}
	return true
}

func receiptVertexDeleteGraphMutation(e *vertexDeleteReceiptEnvelope) *pb.Mutation {
	keys := make([]string, len(e.Accepted))
	for i, item := range e.Accepted {
		keys[i] = item.Key
	}
	return &pb.Mutation{
		Origin: append([]byte(nil), e.Origin[:]...), Seq: e.OriginSeq, Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
			DeleteVertices: &pb.DeleteVerticesRequest{Keys: keys},
		}},
		TombstoneExpiration: timestamppb.New(e.TombstoneExpiration),
	}
}
