package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
)

type vertexPutReceiptCoordinator struct {
	service *LanternService
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	store   *mutationreceipt.Store
}

type receiptVertexPutItem struct {
	ID     mutationreceipt.ID
	Vertex *pb.Vertex
}

type receiptVertexPutCall struct {
	Group    mutationreceipt.GroupID
	Items    []receiptVertexPutItem
	IfAbsent bool
}

type vertexPutReceiptEnvelope struct {
	Mutation          *pb.Mutation
	Origin            hlc.NodeID
	OriginSeq         uint64
	HLC               hlc.Timestamp
	Epoch             mutationreceipt.Epoch
	PolicyFingerprint [32]byte
	IfAbsent          bool
	Original          []*pb.Vertex
	Accepted          []graphcache.IndexedVertexPut[string, *pb.Vertex]
	Receipts          []mutationreceipt.Receipt
}

func (e *vertexPutReceiptEnvelope) GraphMutation() *pb.Mutation { return e.Mutation }

func newVertexPutReceiptCoordinator(
	s *LanternService,
	store *mutationreceipt.Store,
) (*vertexPutReceiptCoordinator, error) {
	if s == nil || store == nil || s.log == nil || s.clock == nil || s.origins == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Vertex Put requires a log, clock, receipt store, and origin tracker"))
	}
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Vertex Put requires a staged GraphCache"))
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.receiptStore != nil && s.receiptStore != store {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt Vertex Put Store differs from the service-bound Store"))
	}
	if s.receiptVertexPutCoordinator != nil {
		return s.receiptVertexPutCoordinator, nil
	}
	s.receiptStore = store
	coordinator := &vertexPutReceiptCoordinator{service: s, cache: cache, store: store}
	s.receiptVertexPutCoordinator = coordinator
	return coordinator, nil
}

func appendReceiptCanonicalU64(dst []byte, value uint64) []byte {
	return binary.BigEndian.AppendUint64(dst, value)
}

func appendReceiptCanonicalBytes(dst, value []byte) []byte {
	dst = appendReceiptCanonicalU64(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendReceiptCanonicalString(dst []byte, value string) []byte {
	return appendReceiptCanonicalBytes(dst, []byte(value))
}

func appendReceiptCanonicalTime(dst []byte, seconds int64, nanos int32) []byte {
	dst = appendReceiptCanonicalU64(dst, uint64(seconds))
	return binary.BigEndian.AppendUint32(dst, uint32(nanos))
}

func vertexPutDigest(vertex *pb.Vertex, ifAbsent bool) ([32]byte, error) {
	if err := validateReceiptVertex(vertex); err != nil {
		return [32]byte{}, err
	}
	canonical := make([]byte, 0, 64+len(vertex.GetKey()))
	canonical = append(canonical, byte(mutationreceipt.PutVertex))
	canonical = appendReceiptCanonicalString(canonical, vertex.GetKey())
	switch value := vertex.GetValue().(type) {
	case nil:
		canonical = append(canonical, 0)
	case *pb.Vertex_Float64:
		canonical = append(canonical, 10)
		canonical = appendReceiptCanonicalU64(canonical, math.Float64bits(value.Float64))
	case *pb.Vertex_Float32:
		canonical = append(canonical, 11)
		canonical = binary.BigEndian.AppendUint32(canonical, math.Float32bits(value.Float32))
	case *pb.Vertex_Int32:
		canonical = append(canonical, 12)
		canonical = binary.BigEndian.AppendUint32(canonical, uint32(value.Int32))
	case *pb.Vertex_Int64:
		canonical = append(canonical, 13)
		canonical = appendReceiptCanonicalU64(canonical, uint64(value.Int64))
	case *pb.Vertex_Uint32:
		canonical = append(canonical, 14)
		canonical = binary.BigEndian.AppendUint32(canonical, value.Uint32)
	case *pb.Vertex_Uint64:
		canonical = append(canonical, 15)
		canonical = appendReceiptCanonicalU64(canonical, value.Uint64)
	case *pb.Vertex_Bool:
		canonical = append(canonical, 16)
		if value.Bool {
			canonical = append(canonical, 1)
		} else {
			canonical = append(canonical, 0)
		}
	case *pb.Vertex_String_:
		canonical = append(canonical, 17)
		canonical = appendReceiptCanonicalString(canonical, value.String_)
	case *pb.Vertex_Bytes:
		canonical = append(canonical, 18)
		canonical = appendReceiptCanonicalBytes(canonical, value.Bytes)
	case *pb.Vertex_Timestamp:
		canonical = append(canonical, 19)
		canonical = appendReceiptCanonicalTime(canonical, value.Timestamp.Seconds, value.Timestamp.Nanos)
	case *pb.Vertex_Duration:
		canonical = append(canonical, 20)
		canonical = appendReceiptCanonicalTime(canonical, value.Duration.Seconds, value.Duration.Nanos)
	case *pb.Vertex_Nil:
		canonical = append(canonical, 30)
	default:
		return [32]byte{}, errors.New("Vertex Put has an unknown value kind")
	}
	if vertex.GetExpiration() == nil {
		canonical = append(canonical, 0)
	} else {
		canonical = append(canonical, 1)
		canonical = appendReceiptCanonicalTime(
			canonical,
			vertex.GetExpiration().GetSeconds(),
			vertex.GetExpiration().GetNanos(),
		)
	}
	if ifAbsent {
		canonical = append(canonical, 1)
	} else {
		canonical = append(canonical, 0)
	}
	return mutationreceipt.IntentDigest(canonical), nil
}

func validateReceiptVertex(vertex *pb.Vertex) error {
	if vertex == nil || vertex.GetKey() == "" || !utf8.ValidString(vertex.GetKey()) ||
		len(vertex.GetKey()) > receiptVertexWALMaxBytes {
		return errors.New("Vertex Put identity must be nonempty UTF-8")
	}
	if err := rejectProtoUnknownFields(vertex.ProtoReflect()); err != nil {
		return fmt.Errorf("Vertex Put %w", err)
	}
	if nilOneofWrapper(vertex.GetValue()) {
		return errors.New("Vertex Put value has a typed-nil oneof")
	}
	switch value := vertex.GetValue().(type) {
	case *pb.Vertex_String_:
		if !utf8.ValidString(value.String_) {
			return errors.New("Vertex Put string value must be UTF-8")
		}
	case *pb.Vertex_Timestamp:
		if value.Timestamp == nil || value.Timestamp.CheckValid() != nil {
			return errors.New("Vertex Put timestamp value is invalid")
		}
	case *pb.Vertex_Duration:
		if value.Duration == nil || value.Duration.CheckValid() != nil {
			return errors.New("Vertex Put duration value is invalid")
		}
	case *pb.Vertex_Nil:
		if !value.Nil {
			return errors.New("Vertex Put nil marker must be true")
		}
	}
	if expiration := vertex.GetExpiration(); expiration != nil && expiration.CheckValid() != nil {
		return errors.New("Vertex Put expiration is invalid")
	}
	return nil
}

func prepareVertexPutReceiptCall(
	s *LanternService,
	call receiptVertexPutCall,
) ([]*pb.Vertex, []graphcache.VertexItem[string, *pb.Vertex], []mutationreceipt.Intent, error) {
	if s == nil || len(call.Items) == 0 || len(call.Items) > receiptVertexWALMaxItems ||
		call.Group == (mutationreceipt.GroupID{}) {
		return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, mutationreceipt.ErrInvalidBatch)
	}
	original := make([]*pb.Vertex, len(call.Items))
	items := make([]graphcache.VertexItem[string, *pb.Vertex], len(call.Items))
	intents := make([]mutationreceipt.Intent, len(call.Items))
	for i, item := range call.Items {
		digest, err := vertexPutDigest(item.Vertex, call.IfAbsent)
		if err != nil {
			return nil, nil, nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		expiration := prototime.Expiration(item.Vertex.GetExpiration())
		if err := s.validateExpiration(expiration); err != nil {
			return nil, nil, nil, err
		}
		vertex := proto.Clone(item.Vertex).(*pb.Vertex)
		original[i] = vertex
		items[i] = graphcache.VertexItem[string, *pb.Vertex]{
			Key: vertex.GetKey(), Value: vertex, Expiration: expiration,
		}
		intents[i] = mutationreceipt.Intent{
			ID: item.ID, Group: call.Group, Index: uint32(i), Count: uint32(len(call.Items)),
			Kind: mutationreceipt.PutVertex, Digest: digest,
		}
	}
	return original, items, intents, nil
}

func receiptVertexPutResponse(receipts []mutationreceipt.Receipt) (*pb.PutVerticesResponse, error) {
	outcomes := make([]pb.PutOutcome, len(receipts))
	for i, receipt := range receipts {
		if len(receipt.Result) != 1 {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt Vertex Put item %d has invalid original result", i))
		}
		outcome := pb.PutOutcome(receipt.Result[0])
		if outcome < pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
			outcome > pb.PutOutcome_PUT_OUTCOME_SUPERSEDED {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt Vertex Put item %d has invalid original result", i))
		}
		outcomes[i] = outcome
	}
	return &pb.PutVerticesResponse{Outcomes: outcomes}, nil
}

func normalizedVertexPutAccepted(
	accepted []graphcache.IndexedVertexPut[string, *pb.Vertex],
	indexes []int,
) ([]graphcache.IndexedVertexPut[string, *pb.Vertex], error) {
	result := make([]graphcache.IndexedVertexPut[string, *pb.Vertex], len(accepted))
	for i, item := range accepted {
		if item.Index < 0 || item.Index >= len(indexes) {
			return nil, errors.New("staged Vertex Put accepted index drift")
		}
		index := indexes[item.Index]
		switch item.Outcome {
		case graphcache.PutOutcomeAppliedAndLive:
			if item.Item.CausalBarrier || item.Item.Value == nil {
				return nil, errors.New("staged Vertex Put live effect drift")
			}
			result[i] = graphcache.IndexedVertexPut[string, *pb.Vertex]{
				Index: index,
				Item: graphcache.VertexItem[string, *pb.Vertex]{
					Key: item.Item.Key, Value: proto.Clone(item.Item.Value).(*pb.Vertex),
					Expiration: item.Item.Expiration,
				},
				Outcome: item.Outcome,
			}
		case graphcache.PutOutcomeExpired:
			result[i] = graphcache.IndexedVertexPut[string, *pb.Vertex]{
				Index: index,
				Item: graphcache.VertexItem[string, *pb.Vertex]{
					Key: item.Item.Key, CausalBarrier: true,
				},
				Outcome: item.Outcome,
			}
		default:
			return nil, errors.New("staged Vertex Put accepted outcome drift")
		}
	}
	return result, nil
}

func identityIndexes(count int) []int {
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	return indexes
}

func originVertexPutEffects(
	e *vertexPutReceiptEnvelope,
) ([]graphcache.VertexItem[string, *pb.Vertex], []int, error) {
	items := make([]graphcache.VertexItem[string, *pb.Vertex], 0, len(e.Receipts))
	indexes := make([]int, 0, len(e.Receipts))
	for i, receipt := range e.Receipts {
		if len(receipt.Result) != 1 {
			return nil, nil, errors.New("receipt Vertex Put original result is malformed")
		}
		switch pb.PutOutcome(receipt.Result[0]) {
		case pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE:
			vertex := proto.Clone(e.Original[i]).(*pb.Vertex)
			items = append(items, graphcache.VertexItem[string, *pb.Vertex]{
				Key: vertex.GetKey(), Value: vertex,
				Expiration: prototime.Expiration(vertex.GetExpiration()),
			})
			indexes = append(indexes, i)
		case pb.PutOutcome_PUT_OUTCOME_EXPIRED:
			items = append(items, graphcache.VertexItem[string, *pb.Vertex]{
				Key: e.Original[i].GetKey(), CausalBarrier: true,
			})
			indexes = append(indexes, i)
		case pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
			pb.PutOutcome_PUT_OUTCOME_SUPERSEDED:
		default:
			return nil, nil, errors.New("receipt Vertex Put original result is invalid")
		}
	}
	return items, indexes, nil
}

func (c *vertexPutReceiptCoordinator) Commit(
	ctx context.Context,
	call receiptVertexPutCall,
) (*pb.PutVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	original, items, intents, err := prepareVertexPutReceiptCall(c.service, call)
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
		return receiptVertexPutResponse(prior)
	}
	placeholders := make([][]byte, len(items))
	for i := range placeholders {
		placeholders[i] = []byte{0}
	}
	if err := storeTx.Reserve(placeholders); err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.prepareLocalMutationLocked(); err != nil {
		return nil, err
	}
	if err := s.checkVertexCapacity(len(items)); err != nil {
		return nil, err
	}
	clockFloor, err := receiptClockFloor(storeTx)
	if err != nil {
		return nil, receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(clockFloor); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("receipt Vertex Put clock floor: %w", err))
	}
	origin := s.clock.NodeID()
	seq := s.origins.LocalSeq(origin) + 1
	ts := s.clock.Now()
	graphTx, err := c.cache.BeginVertexPut(items, ts, call.IfAbsent)
	if err != nil {
		return nil, searchIndexWriteError(err)
	}
	defer graphTx.Abort()
	result := graphTx.Result()
	wireOutcomes, err := putOutcomes(result.Outcomes, len(items))
	if err != nil {
		return nil, err
	}
	results := make([][]byte, len(wireOutcomes))
	for i, outcome := range wireOutcomes {
		results[i] = []byte{byte(outcome)}
	}
	if err := storeTx.ReplaceReservedResults(results); err != nil {
		return nil, receiptStoreError(err)
	}
	receipts, err := storeTx.ReservedReceipts()
	if err != nil {
		return nil, receiptStoreError(err)
	}
	accepted, err := normalizedVertexPutAccepted(result.Accepted, identityIndexes(len(items)))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	envelope := &vertexPutReceiptEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts,
		Epoch: c.store.Epoch(), PolicyFingerprint: c.store.PolicyFingerprint(),
		IfAbsent: call.IfAbsent, Original: original, Accepted: accepted, Receipts: receipts,
	}
	envelope.Mutation = receiptVertexPutGraphMutation(envelope)
	if _, err := validateReceiptVertexPutWALEnvelope(envelope); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := s.validateReplicationFrame(envelope); err != nil {
		return nil, err
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal,
			errors.New("receipt Vertex Put could not stage contiguous origin seq"))
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
	return &pb.PutVerticesResponse{Outcomes: wireOutcomes}, nil
}

func (c *vertexPutReceiptCoordinator) validateReplicatedEnvelope(e *vertexPutReceiptEnvelope) error {
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
	return nil
}

func (c *vertexPutReceiptCoordinator) commitReplicated(
	ctx context.Context,
	origin hlc.NodeID,
	seq uint64,
	ts hlc.Timestamp,
	pending *pendingMutation,
) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	e, ok := pending.receipt.(*vertexPutReceiptEnvelope)
	if !ok || e == nil || e.Origin != origin || e.OriginSeq != seq || e.HLC != ts {
		return connect.NewError(connect.CodeInternal,
			errors.New("replication Vertex Put receipt pending identity drift"))
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
	maximal, err := maximalReceiptVertexPutEnvelope(e)
	if err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Vertex Put receipt maximal envelope: %w", err))
	}
	if err := s.validateReplicationFrame(maximal); err != nil {
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
			fmt.Errorf("replication Vertex Put receipt clock floor: %w", err))
	}
	items, indexes, err := originVertexPutEffects(e)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	graphTx, err := c.cache.BeginReplicatedVertexPut(items, ts)
	if err != nil {
		return searchIndexWriteError(err)
	}
	defer graphTx.Abort()
	accepted, err := normalizedVertexPutAccepted(graphTx.Result().Accepted, indexes)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	localEnvelope := &vertexPutReceiptEnvelope{
		Origin: origin, OriginSeq: seq, HLC: ts, Epoch: e.Epoch,
		PolicyFingerprint: e.PolicyFingerprint, IfAbsent: e.IfAbsent,
		Original: cloneReceiptVertices(e.Original),
		Accepted: accepted,
		Receipts: cloneMutationReceipts(e.Receipts),
	}
	localEnvelope.Mutation = receiptVertexPutGraphMutation(localEnvelope)
	if _, err := validateReceiptVertexPutWALEnvelope(localEnvelope); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Vertex Put receipt relay envelope: %w", err))
	}
	if err := s.validateReplicationFrame(localEnvelope); err != nil {
		return err
	}
	if prior, ok := pending.receiptWAL.(*vertexPutReceiptEnvelope); ok &&
		sameVertexPutAccepted(prior.Accepted, localEnvelope.Accepted) {
		localEnvelope = prior
	}

	if err := storeTx.Stage(); err != nil {
		return receiptStoreError(err)
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Vertex Put receipt could not stage origin %x seq %d", origin, seq))
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
			fmt.Errorf("replication Vertex Put receipt origin clock floor: %w", err))
	}
	return nil
}

func maximalReceiptVertexPutEnvelope(
	e *vertexPutReceiptEnvelope,
) (*vertexPutReceiptEnvelope, error) {
	items, indexes, err := originVertexPutEffects(e)
	if err != nil {
		return nil, err
	}
	accepted := make([]graphcache.IndexedVertexPut[string, *pb.Vertex], len(items))
	for i, item := range items {
		outcome := graphcache.PutOutcomeAppliedAndLive
		if item.CausalBarrier {
			outcome = graphcache.PutOutcomeExpired
		}
		accepted[i] = graphcache.IndexedVertexPut[string, *pb.Vertex]{
			Index:   indexes[i],
			Item:    item,
			Outcome: outcome,
		}
	}
	maximal := &vertexPutReceiptEnvelope{
		Origin:            e.Origin,
		OriginSeq:         e.OriginSeq,
		HLC:               e.HLC,
		Epoch:             e.Epoch,
		PolicyFingerprint: e.PolicyFingerprint,
		IfAbsent:          e.IfAbsent,
		Original:          cloneReceiptVertices(e.Original),
		Accepted:          accepted,
		Receipts:          cloneMutationReceipts(e.Receipts),
	}
	maximal.Mutation = receiptVertexPutGraphMutation(maximal)
	return maximal, nil
}

func (c *vertexPutReceiptCoordinator) Lookup(
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

func cloneReceiptVertices(vertices []*pb.Vertex) []*pb.Vertex {
	cloned := make([]*pb.Vertex, len(vertices))
	for i, vertex := range vertices {
		cloned[i] = proto.Clone(vertex).(*pb.Vertex)
	}
	return cloned
}

func cloneMutationReceipts(receipts []mutationreceipt.Receipt) []mutationreceipt.Receipt {
	cloned := make([]mutationreceipt.Receipt, len(receipts))
	for i, receipt := range receipts {
		cloned[i] = receipt
		cloned[i].Result = append([]byte(nil), receipt.Result...)
	}
	return cloned
}

func sameVertexPutAccepted(
	a, b []graphcache.IndexedVertexPut[string, *pb.Vertex],
) bool {
	return slices.EqualFunc(a, b, func(left, right graphcache.IndexedVertexPut[string, *pb.Vertex]) bool {
		return left.Index == right.Index && left.Outcome == right.Outcome &&
			left.Item.Key == right.Item.Key && left.Item.Expiration.Equal(right.Item.Expiration) &&
			left.Item.CausalBarrier == right.Item.CausalBarrier &&
			sameVertexPutCanonicalValue(left.Item.Value, right.Item.Value)
	})
}

func sameVertexPutCanonicalValue(left, right *pb.Vertex) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftDigest, leftErr := vertexPutDigest(left, false)
	rightDigest, rightErr := vertexPutDigest(right, false)
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

func sameReceiptVertexPutIntent(a, b *vertexPutReceiptEnvelope) bool {
	if a == nil || b == nil || a.Origin != b.Origin || a.OriginSeq != b.OriginSeq ||
		a.HLC != b.HLC || a.Epoch != b.Epoch || a.PolicyFingerprint != b.PolicyFingerprint ||
		a.IfAbsent != b.IfAbsent || len(a.Original) != len(b.Original) ||
		len(a.Receipts) != len(b.Receipts) {
		return false
	}
	for i := range a.Original {
		if !sameVertexPutCanonicalValue(a.Original[i], b.Original[i]) {
			return false
		}
		ar, br := a.Receipts[i], b.Receipts[i]
		if ar.Intent != br.Intent || ar.DeadlineMillis != br.DeadlineMillis ||
			!bytes.Equal(ar.Result, br.Result) {
			return false
		}
	}
	return true
}
