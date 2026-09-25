package service

import (
	"context"
	"encoding/binary"
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

// edgeDeleteReceiptCoordinator is the receipt commit infrastructure. The
// certified durable runtime binds it after the service has its tombstone
// policy; the final activation barrier controls public access.
type edgeDeleteReceiptCoordinator struct {
	service *LanternService
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	store   *mutationreceipt.Store
	retired *retiredReceiptCatalogSlot
}

type receiptEdgeDeleteItem struct {
	ID   mutationreceipt.ID
	Tail string
	Head string
}

type receiptEdgeDeleteCall struct {
	Group mutationreceipt.GroupID
	Items []receiptEdgeDeleteItem
}

func (s *LanternService) commitPublicReceiptEdgeDelete(
	ctx context.Context,
	request *pb.DeleteEdgesRequest,
) (*pb.DeleteEdgesResponse, error) {
	runtime, release, err := s.acquirePublicReceiptRuntime()
	if err != nil {
		return nil, err
	}
	defer release()

	edges := request.GetEdges()
	group, ids, err := s.validatePublicReceiptContext(
		runtime,
		request.GetReceiptContext(),
		len(edges),
		"edges",
	)
	if err != nil {
		return nil, err
	}

	items := make([]receiptEdgeDeleteItem, len(edges))
	for i, id := range ids {
		edge := edges[i]
		if edge == nil {
			return nil, invalidReceiptRequest(fmt.Errorf("edges[%d] is nil", i))
		}
		items[i] = receiptEdgeDeleteItem{
			ID: id, Tail: edge.GetTail(), Head: edge.GetHead(),
		}
	}
	return s.receiptEdgeDeleteCoordinator.Commit(ctx, receiptEdgeDeleteCall{
		Group: group,
		Items: items,
	})
}

// edgeDeleteReceiptEnvelope is one owned WAL payload. OriginalKeys and
// Receipts retain request index and original result; Accepted records only
// causally admitted graph transitions. Mutation is the graph-only projection
// consumed internally by the coordinator. Subscribe projects the full owned
// envelope as a receipt-bearing wire arm and refuses unadvertised full-stream
// consumers. Peer apply consumes the envelope when this private coordinator is
// bound. Graph-only Snapshot/BackupSnapshot remain receipt-unaware; the
// certified RECEIPT source captures this state through its separate path.
type edgeDeleteReceiptEnvelope struct {
	Mutation            *pb.Mutation
	Origin              hlc.NodeID
	OriginSeq           uint64
	HLC                 hlc.Timestamp
	Epoch               mutationreceipt.Epoch
	PolicyFingerprint   [32]byte
	TombstoneExpiration time.Time
	OriginalKeys        []graphcache.EdgeKey[string]
	Accepted            []graphcache.IndexedEdgeDelete[string]
	Receipts            []mutationreceipt.Receipt
}

func (e *edgeDeleteReceiptEnvelope) GraphMutation() *pb.Mutation { return e.Mutation }

func newEdgeDeleteReceiptCoordinator(s *LanternService, store *mutationreceipt.Store) (*edgeDeleteReceiptCoordinator, error) {
	if s == nil || store == nil || s.log == nil || s.clock == nil || s.origins == nil || s.tombstoneTTL <= 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("receipt Edge Delete requires a log, clock, receipt store, and causal tombstone retention"))
	}
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("receipt Edge Delete requires a staged GraphCache"))
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.receiptStore != nil && s.receiptStore != store {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("receipt Edge Delete Store differs from the service-bound Store"))
	}
	if s.receiptEdgeDeleteCoordinator != nil {
		return s.receiptEdgeDeleteCoordinator, nil
	}
	s.receiptStore = store
	if s.receiptRetiredCatalog == nil {
		s.receiptRetiredCatalog = &retiredReceiptCatalogSlot{}
	}
	coordinator := &edgeDeleteReceiptCoordinator{
		service: s,
		cache:   cache,
		store:   store,
		retired: s.receiptRetiredCatalog,
	}
	s.receiptEdgeDeleteCoordinator = coordinator
	return coordinator, nil
}

// edgeDeleteDigest encodes the validated semantic intent, independent of
// protobuf serialization and unknown fields. The Store separately binds the
// logical-call group, index, and count to each operation ID.
func edgeDeleteDigest(tail, head string) [32]byte {
	canonical := make([]byte, 0, 1+8+len(tail)+8+len(head))
	canonical = append(canonical, byte(mutationreceipt.DeleteEdge))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tail)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, tail...)
	binary.BigEndian.PutUint64(length[:], uint64(len(head)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, head...)
	return mutationreceipt.IntentDigest(canonical)
}

func prepareEdgeDeleteReceiptCall(call receiptEdgeDeleteCall) ([]graphcache.EdgeKey[string], []mutationreceipt.Intent, error) {
	if len(call.Items) == 0 || len(call.Items) > math.MaxInt32 || call.Group == (mutationreceipt.GroupID{}) {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, mutationreceipt.ErrInvalidBatch)
	}
	keys := make([]graphcache.EdgeKey[string], len(call.Items))
	intents := make([]mutationreceipt.Intent, len(call.Items))
	for i, item := range call.Items {
		if item.Tail == "" || item.Head == "" || !utf8.ValidString(item.Tail) || !utf8.ValidString(item.Head) {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("Edge Delete identity must be nonempty UTF-8"))
		}
		keys[i] = graphcache.EdgeKey[string]{Tail: item.Tail, Head: item.Head}
		intents[i] = mutationreceipt.Intent{
			ID: item.ID, Group: call.Group, Index: uint32(i), Count: uint32(len(call.Items)),
			Kind: mutationreceipt.DeleteEdge, Digest: edgeDeleteDigest(item.Tail, item.Head),
		}
	}
	return keys, intents, nil
}

func receiptDeleteResponse(receipts []mutationreceipt.Receipt) (*pb.DeleteEdgesResponse, error) {
	outcomes := make([]bool, len(receipts))
	for i, receipt := range receipts {
		if len(receipt.Result) != 1 || receipt.Result[0] > 1 {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("receipt Edge Delete item %d has invalid original result", i))
		}
		outcomes[i] = receipt.Result[0] == 1
	}
	deleted, err := checkedDeleteOutcomes(outcomes, len(receipts))
	if err != nil {
		return nil, err
	}
	return &pb.DeleteEdgesResponse{Deleted: deleted, Existed: outcomes}, nil
}

func receiptStoreError(err error) error {
	switch {
	case errors.Is(err, mutationreceipt.ErrInvalidID), errors.Is(err, mutationreceipt.ErrInvalidBatch),
		errors.Is(err, mutationreceipt.ErrIntentConflict), errors.Is(err, mutationreceipt.ErrContributionConflict):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, mutationreceipt.ErrNoLongerProvable), errors.Is(err, mutationreceipt.ErrNotFresh),
		errors.Is(err, mutationreceipt.ErrPartialEnvelope), errors.Is(err, mutationreceipt.ErrInvalidClock):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, mutationreceipt.ErrCapacity):
		return connect.NewError(connect.CodeResourceExhausted, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func receiptClockFloor(tx *mutationreceipt.Tx) (hlc.Timestamp, error) {
	millis, err := tx.ClockHighWaterMillis()
	if err != nil {
		return hlc.Timestamp{}, err
	}
	if millis < 0 || millis > math.MaxInt64/int64(time.Millisecond) {
		return hlc.Timestamp{}, mutationreceipt.ErrInvalidClock
	}
	return hlc.Timestamp{WallNs: millis * int64(time.Millisecond)}, nil
}

// Commit serializes one logical call under the service publication cut. The
// lock order is service -> receipt origin cut -> Store -> GraphCache -> origin
// tracker -> Log. Every allocating/fallible graph and receipt step completes
// before WAL.Write. The post-ring callback only releases already-staged
// locks, after the matching log entry is installed and before dispatcher
// handoff can block. The service gate releases last. LocalSeq shares the
// receipt origin cut. This is an in-process prerequisite, not a durable or
// multi-replica guarantee; raw Core APIs cannot fail closed on an
// indeterminate WAL outcome because most return no error.
func (c *edgeDeleteReceiptCoordinator) Commit(ctx context.Context, call receiptEdgeDeleteCall) (*pb.DeleteEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	keys, intents, err := prepareEdgeDeleteReceiptCall(call)
	if err != nil {
		return nil, err
	}
	if err := validateReceiptEdgeDeleteWALRequestCapacity(keys); err != nil {
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
	tx, err := c.store.Begin(time.Now())
	if err != nil {
		return nil, receiptStoreError(err)
	}
	defer tx.Abort()
	classification, prior, err := tx.Classify(intents)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if classification == mutationreceipt.Duplicate {
		return receiptDeleteResponse(prior)
	}
	clockFloor, err := receiptClockFloor(tx)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("receipt Edge Delete clock floor: %w", err))
	}

	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1 // prepareLocalMutationLocked rejected overflow.
	ts, expiration, err := s.sampleDeleteStamp()
	if err != nil {
		return nil, err
	}
	graphTx, err := c.cache.PrepareEdgeDelete(keys, ts, expiration)
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
	if err := tx.Reserve(results); err != nil {
		return nil, receiptStoreError(err)
	}
	receipts, err := tx.ReservedReceipts()
	if err != nil {
		return nil, receiptStoreError(err)
	}
	accepted := make([]*pb.EdgeKey, len(result.Accepted))
	for i, item := range result.Accepted {
		if item.Index < 0 || item.Index >= len(keys) || item.Key != keys[item.Index] {
			return nil, connect.NewError(connect.CodeInternal, errors.New("staged Edge Delete accepted index drift"))
		}
		accepted[i] = &pb.EdgeKey{Tail: item.Key.Tail, Head: item.Key.Head}
	}
	mutation := &pb.Mutation{
		Origin: append([]byte(nil), origin[:]...), Seq: seq, Hlc: hlcToProto(ts),
		Op:                  &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{Edges: accepted}}},
		TombstoneExpiration: timestamppb.New(expiration),
	}
	envelope := &edgeDeleteReceiptEnvelope{
		Mutation: mutation, Origin: origin, OriginSeq: seq, HLC: ts,
		Epoch: c.store.Epoch(), PolicyFingerprint: c.store.PolicyFingerprint(),
		TombstoneExpiration: expiration,
		OriginalKeys:        append([]graphcache.EdgeKey[string](nil), keys...),
		Accepted:            append([]graphcache.IndexedEdgeDelete[string](nil), result.Accepted...),
		Receipts:            receipts,
	}
	if _, err := validateReceiptEdgeDeleteWALEnvelope(envelope); err != nil {
		if errors.Is(err, errReceiptEdgeDeleteWALCapacity) {
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
		return nil, connect.NewError(connect.CodeInternal, errors.New("receipt Edge Delete could not stage contiguous origin seq"))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if err := tx.Stage(); err != nil {
		return nil, receiptStoreError(err)
	}
	graphTx.Apply()
	walAttempted = true
	_, err = s.log.CommitWithPostRingPublication(envelope, ts, func(mutationlog.Entry) {
		// The ring/seq now contain the matching entry, but log readers
		// still wait on Log.mu. Release Store before GraphCache, then the
		// origin frontier last. The dispatcher handoff follows this callback,
		// so no Core lock is held across its back-pressure wait. Server-owned
		// observers remain blocked by replicationCutMu throughout.
		tx.Commit()
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
		if errors.Is(err, errReceiptEdgeDeleteWALCapacity) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &pb.DeleteEdgesResponse{Deleted: deleted, Existed: result.Existed}, nil
}

func (c *edgeDeleteReceiptCoordinator) validateReplicatedEnvelope(e *edgeDeleteReceiptEnvelope) error {
	if e == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("replication receipt envelope is nil"))
	}
	if e.Epoch != c.store.Epoch() {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("replication receipt epoch differs from the local Store"))
	}
	if e.PolicyFingerprint != c.store.PolicyFingerprint() {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("replication receipt policy differs from the local Store"))
	}
	localWall := c.service.remoteValidationWall()
	if time.Unix(0, e.HLC.WallNs).After(localWall.Add(hlc.DefaultMaxSkew)) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("replication receipt origin HLC exceeds maximum clock skew"))
	}
	if err := c.store.ValidateCommitted(e.Receipts, e.HLC.WallNs/int64(time.Millisecond)); err != nil {
		return receiptStoreError(err)
	}
	if _, err := c.service.validateIncomingTombstoneExpirationAt(e.Mutation, localWall); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication receipt tombstone: %w", err))
	}
	return nil
}

// commitReplicated runs under replicationCutMu and pendingMu for the next
// contiguous origin sequence. Store admission precedes graph staging, so a
// capacity stall leaves the exact queued envelope untouched and retryable.
// The incoming accepted bits are deliberately ignored: this receiver stages
// every original key and relays the accepted set returned by that local cut.
func (c *edgeDeleteReceiptCoordinator) commitReplicated(
	ctx context.Context,
	origin hlc.NodeID,
	seq uint64,
	ts hlc.Timestamp,
	pending *pendingMutation,
) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	e, ok := pending.receipt.(*edgeDeleteReceiptEnvelope)
	if !ok || e == nil || e.Origin != origin || e.OriginSeq != seq || e.HLC != ts {
		return connect.NewError(connect.CodeInternal, errors.New("replication receipt pending identity drift"))
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

	tx, err := c.store.Begin(time.Now())
	if err != nil {
		return receiptStoreError(err)
	}
	defer tx.Abort()
	if err := tx.PrepareCommitted(e.Receipts, ts.WallNs/int64(time.Millisecond)); err != nil {
		return receiptStoreError(err)
	}
	clockFloor, err := receiptClockFloor(tx)
	if err != nil {
		return receiptStoreError(err)
	}
	graphTx, err := c.cache.PrepareReplicatedEdgeDelete(e.OriginalKeys, ts, e.TombstoneExpiration)
	if err != nil {
		return writeError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	localEnvelope := &edgeDeleteReceiptEnvelope{
		Origin:              origin,
		OriginSeq:           seq,
		HLC:                 ts,
		Epoch:               e.Epoch,
		PolicyFingerprint:   e.PolicyFingerprint,
		TombstoneExpiration: e.TombstoneExpiration,
		OriginalKeys:        append([]graphcache.EdgeKey[string](nil), e.OriginalKeys...),
		Accepted:            append([]graphcache.IndexedEdgeDelete[string](nil), result.Accepted...),
		Receipts:            make([]mutationreceipt.Receipt, len(e.Receipts)),
	}
	for i, receipt := range e.Receipts {
		localEnvelope.Receipts[i] = receipt
		localEnvelope.Receipts[i].Result = append([]byte(nil), receipt.Result...)
	}
	localEnvelope.Mutation = receiptEdgeDeleteWALMutation(localEnvelope)
	if _, err := validateReceiptEdgeDeleteWALEnvelope(localEnvelope); err != nil {
		if errors.Is(err, errReceiptEdgeDeleteWALCapacity) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeInternal, fmt.Errorf("replication receipt relay envelope: %w", err))
	}
	if err := s.validateReplicationFrame(localEnvelope); err != nil {
		return err
	}
	if prior, ok := pending.receiptWAL.(*edgeDeleteReceiptEnvelope); ok &&
		slices.Equal(prior.Accepted, localEnvelope.Accepted) {
		localEnvelope = prior
	}

	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("replication receipt could not stage origin %x seq %d", origin, seq))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	if err := tx.Stage(); err != nil {
		return receiptStoreError(err)
	}
	graphTx.Apply()

	// Retain the exact receiver-local evidence before entering the WAL. On an
	// indeterminate return this is the recovery identity while the service
	// faults reads, writes, status, Snapshot, and Subscribe.
	pending.receiptWAL = localEnvelope
	walAttempted = true
	_, err = s.log.CommitWithPostRingPublication(localEnvelope, ts, func(mutationlog.Entry) {
		tx.Commit()
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
		if errors.Is(err, errReceiptEdgeDeleteWALCapacity) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeUnavailable, err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		s.markReceiptCommitFaultLocked()
		return connect.NewError(connect.CodeInternal, fmt.Errorf("replication receipt clock floor: %w", err))
	}
	// The remote stamp is now part of the durable, validated publication cut.
	// Restore it exactly instead of applying the live-peer skew clamp against
	// a possibly rolled-back physical clock.
	if err := s.clock.RestoreFloor(ts); err != nil {
		s.markReceiptCommitFaultLocked()
		return connect.NewError(connect.CodeInternal, fmt.Errorf("replication receipt origin clock floor: %w", err))
	}
	return nil
}

func maximalReceiptEdgeDeleteEnvelope(
	e *edgeDeleteReceiptEnvelope,
) *edgeDeleteReceiptEnvelope {
	maximal := &edgeDeleteReceiptEnvelope{
		Origin:              e.Origin,
		OriginSeq:           e.OriginSeq,
		HLC:                 e.HLC,
		Epoch:               e.Epoch,
		PolicyFingerprint:   e.PolicyFingerprint,
		TombstoneExpiration: e.TombstoneExpiration,
		OriginalKeys:        append([]graphcache.EdgeKey[string](nil), e.OriginalKeys...),
		Accepted:            make([]graphcache.IndexedEdgeDelete[string], len(e.OriginalKeys)),
		Receipts:            cloneMutationReceipts(e.Receipts),
	}
	for i, key := range maximal.OriginalKeys {
		maximal.Accepted[i] = graphcache.IndexedEdgeDelete[string]{Index: i, Key: key}
	}
	maximal.Mutation = receiptEdgeDeleteWALMutation(maximal)
	return maximal
}

// Lookup is the only receipt status view for this private coordinator. A
// poisoned WAL or Snapshot install must not turn an uncertain operation into
// a false NotYetObserved answer. Store.Lookup alone has no cross-component
// gate. The existing GetReplicationStatus dashboard reports pump health and
// remains available during a publication fault; it does not certify receipts.
func (c *edgeDeleteReceiptCoordinator) Lookup(id mutationreceipt.ID, now time.Time) (mutationreceipt.Status, mutationreceipt.Receipt, error) {
	s := c.service
	var status mutationreceipt.Status
	var receipt mutationreceipt.Receipt
	var lookupErr error
	err := s.withCommittedView(func() error {
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

// markReceiptCommitFaultLocked has no local retry path: a generic WAL error
// may already have committed the envelope. Only a future certified replay or
// receipt-bearing Snapshot may clear this fail-stop state.
func (s *LanternService) markReceiptCommitFaultLocked() {
	if s.receiptCommitFaulted {
		return
	}
	s.receiptCommitFaulted = true
	if s.publicationFaultCount == 0 {
		close(s.publicationFaultCh)
	}
	s.publicationFaultCount++
}
