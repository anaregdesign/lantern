package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
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

// edgeDeleteReceiptCoordinator is an unwired #1115 commit prerequisite. A
// future receipt RPC must provide authenticated epoch admission, replay and
// receipt-bearing Snapshot/backup before this path can serve traffic.
type edgeDeleteReceiptCoordinator struct {
	service *LanternService
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	store   *mutationreceipt.Store
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

// edgeDeleteReceiptEnvelope is one owned WAL payload. OriginalKeys and
// Receipts retain request index and original result; Accepted records only
// causally admitted graph transitions. Mutation is the graph-only projection
// consumed internally by the coordinator. Subscribe projects the full owned
// envelope as a receipt-bearing wire arm and refuses unadvertised full-stream
// consumers. Peer apply and Snapshot/BackupSnapshot remain receipt-unaware.
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
	return &edgeDeleteReceiptCoordinator{service: s, cache: cache, store: store}, nil
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

	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1 // prepareLocalMutationLocked rejected overflow.
	ts := s.clock.Now()
	expiration := s.tombstoneExpiration()
	graphTx, err := c.cache.BeginEdgeDelete(keys, ts, expiration)
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
	if err := tx.Stage(); err != nil {
		return nil, receiptStoreError(err)
	}
	receipts, err := tx.StagedReceipts()
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
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("receipt Edge Delete could not stage contiguous origin seq"))
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
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
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return &pb.DeleteEdgesResponse{Deleted: deleted, Existed: result.Existed}, nil
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
