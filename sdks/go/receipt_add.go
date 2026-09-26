package client

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

const (
	// ContribIDSize is the canonical Edge Add contribution-ID width.
	ContribIDSize = 24
)

// ContribID is a caller-owned Edge Add contribution identity. Persist it with
// the receipt context and exact mutation input before the first send.
type ContribID [ContribIDSize]byte

// ContribIDFromBytes validates and copies a contribution ID.
func ContribIDFromBytes(raw []byte) (ContribID, error) {
	var id ContribID
	if err := copyNonzeroReceiptBytes("contribution ID", id[:], raw); err != nil {
		return ContribID{}, err
	}
	return id, nil
}

// ParseContribID decodes a canonical hexadecimal contribution ID.
func ParseContribID(encoded string) (ContribID, error) {
	raw, err := decodeReceiptHex("contribution ID", encoded, ContribIDSize)
	if err != nil {
		return ContribID{}, err
	}
	return ContribIDFromBytes(raw)
}

// Bytes returns a copy of the canonical wire representation.
func (id ContribID) Bytes() []byte { return append([]byte(nil), id[:]...) }

// String returns the canonical lowercase hexadecimal representation.
func (id ContribID) String() string { return hex.EncodeToString(id[:]) }

// MarshalText implements encoding.TextMarshaler.
func (id ContribID) MarshalText() ([]byte, error) {
	if id == (ContribID{}) {
		return nil, invalidReceiptError("contribution ID must be nonzero")
	}
	return []byte(id.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (id *ContribID) UnmarshalText(text []byte) error {
	parsed, err := ParseContribID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// NewContribID mints one caller-owned contribution ID. The SDK does not retain
// or automatically apply it; callers must persist and explicitly supply it.
func (l *Lantern) NewContribID() (ContribID, error) {
	source := defaultReceiptIdentitySource()
	if l != nil {
		source = l.receiptIDs.normalized()
	}
	return mintContribID(source)
}

func mintContribID(source receiptIdentitySource) (ContribID, error) {
	source = source.normalized()
	var id ContribID
	if _, err := io.ReadFull(source.random, id[:]); err != nil {
		return ContribID{}, fmt.Errorf("client: mint contribution ID: %w", err)
	}
	if id == (ContribID{}) {
		return ContribID{}, invalidReceiptError("minted contribution ID must be nonzero")
	}
	return id, nil
}

// EdgeAddReceiptInput binds one additive edge mutation to its persistent
// caller-owned contribution ID.
type EdgeAddReceiptInput struct {
	Edge      EdgeInput
	ContribID ContribID
}

// EdgeAddReceiptResult is the exact original request-index-aligned result of
// one receipt-bearing Edge Add.
type EdgeAddReceiptResult struct {
	Edge            EdgeRef
	ContribID       ContribID
	OperationID     ReceiptOperationID
	EffectiveWeight float32
}

// AddEdgesWithReceipt performs one plural-canonical receipt-bearing Edge Add.
// The contribution IDs, receipt context, and exact inputs must be persisted
// before the first send and reused unchanged after an ambiguous response.
func (l *Lantern) AddEdgesWithReceipt(
	ctx context.Context,
	inputs []EdgeAddReceiptInput,
	receiptContext ReceiptContext,
) ([]EdgeAddReceiptResult, error) {
	request, stableInputs, stableContext, err := receiptAddEdgesRequest(inputs, receiptContext)
	if err != nil {
		return nil, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()

	var response *pb.AddEdgesResponse
	err = l.executeReceiptMutation(
		ctx,
		ReceiptMutationAddEdge,
		stableContext.Continuity,
		func(callCtx context.Context) error {
			resp, err := unaryOnce(callCtx, request, l.client.AddEdges)
			if err == nil {
				response = resp
			}
			return err
		},
	)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, receiptProtocolError("Edge Add response is nil")
	}
	if response.GetWritten() != int32(len(stableInputs)) {
		return nil, receiptProtocolError(
			"Edge Add written count %d does not match request count %d",
			response.GetWritten(),
			len(stableInputs),
		)
	}
	weights := response.GetEffectiveWeights()
	if len(weights) != len(stableInputs) {
		return nil, receiptProtocolError(
			"Edge Add effective-weight count %d does not match request count %d",
			len(weights),
			len(stableInputs),
		)
	}
	results := make([]EdgeAddReceiptResult, len(stableInputs))
	for i, input := range stableInputs {
		results[i] = EdgeAddReceiptResult{
			Edge: EdgeRef{
				Tail: input.Edge.Tail,
				Head: input.Edge.Head,
			},
			ContribID:       input.ContribID,
			OperationID:     stableContext.OperationIDs[i],
			EffectiveWeight: weights[i],
		}
	}
	return results, nil
}

// AddEdgeWithReceipt is the one-item, relative-TTL facade over
// AddEdgesWithReceipt. Positive ttl is anchored to the persisted operation
// ID's issuance time so retries and later replays keep one absolute expiration.
func (l *Lantern) AddEdgeWithReceipt(
	ctx context.Context,
	tail, head string,
	weight float32,
	ttl time.Duration,
	contribID ContribID,
	receiptContext ReceiptContext,
) (EdgeAddReceiptResult, error) {
	expiration, err := receiptAddExpirationFromTTL(receiptContext, ttl)
	if err != nil {
		return EdgeAddReceiptResult{}, err
	}
	return l.AddEdgeAtWithReceipt(
		ctx,
		tail,
		head,
		weight,
		expiration,
		contribID,
		receiptContext,
	)
}

// AddEdgeAtWithReceipt is the one-item, absolute-expiration facade over
// AddEdgesWithReceipt.
func (l *Lantern) AddEdgeAtWithReceipt(
	ctx context.Context,
	tail, head string,
	weight float32,
	expiration time.Time,
	contribID ContribID,
	receiptContext ReceiptContext,
) (EdgeAddReceiptResult, error) {
	results, err := l.AddEdgesWithReceipt(
		ctx,
		[]EdgeAddReceiptInput{{
			Edge: EdgeInput{
				Tail:       tail,
				Head:       head,
				Weight:     weight,
				Expiration: expiration,
			},
			ContribID: contribID,
		}},
		receiptContext,
	)
	if err != nil {
		return EdgeAddReceiptResult{}, err
	}
	if len(results) != 1 {
		return EdgeAddReceiptResult{}, receiptProtocolError(
			"singular Edge Add returned %d items",
			len(results),
		)
	}
	return results[0], nil
}

func receiptAddEdgesRequest(
	inputs []EdgeAddReceiptInput,
	receiptContext ReceiptContext,
) (*pb.AddEdgesRequest, []EdgeAddReceiptInput, ReceiptContext, error) {
	if len(inputs) == 0 || uint64(len(inputs)) > math.MaxInt32 {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt Edge Add item count must be between 1 and %d",
			int64(math.MaxInt32),
		)
	}
	if err := receiptContext.Validate(len(inputs)); err != nil {
		return nil, nil, ReceiptContext{}, err
	}
	if receiptContext.Mutation != ReceiptMutationAddEdge {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation,
			ReceiptMutationAddEdge,
		)
	}

	stableInputs := append([]EdgeAddReceiptInput(nil), inputs...)
	stableContext := receiptContext.Clone()
	edges := make([]*pb.Edge, len(stableInputs))
	contribIDs := make([][]byte, len(stableInputs))
	seenContribs := make(map[ContribID]struct{}, len(stableInputs))
	for i, input := range stableInputs {
		edge := input.Edge
		if edge.Tail == "" || edge.Head == "" {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"edges[%d] tail and head must be nonempty",
				i,
			)
		}
		if !utf8.ValidString(edge.Tail) || !utf8.ValidString(edge.Head) {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"edges[%d] tail and head must be valid UTF-8",
				i,
			)
		}
		if math.IsNaN(float64(edge.Weight)) || math.IsInf(float64(edge.Weight), 0) {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"edges[%d] weight must be finite",
				i,
			)
		}
		expiration := timestamppb.New(edge.Expiration)
		if err := expiration.CheckValid(); err != nil {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"edges[%d] expiration: %v",
				i,
				err,
			)
		}
		if input.ContribID == (ContribID{}) {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"contrib_ids[%d] must be nonzero",
				i,
			)
		}
		if _, duplicate := seenContribs[input.ContribID]; duplicate {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"contrib_ids[%d] duplicates an earlier item",
				i,
			)
		}
		seenContribs[input.ContribID] = struct{}{}
		edges[i] = &pb.Edge{
			Tail:       edge.Tail,
			Head:       edge.Head,
			Weight:     edge.Weight,
			Expiration: expiration,
		}
		contribIDs[i] = input.ContribID.Bytes()
	}
	return &pb.AddEdgesRequest{
		Edges:          edges,
		ContribIds:     contribIDs,
		ReceiptContext: receiptContextToProto(stableContext),
	}, stableInputs, stableContext, nil
}

func receiptAddExpirationFromTTL(
	receiptContext ReceiptContext,
	ttl time.Duration,
) (time.Time, error) {
	if err := receiptContext.Validate(1); err != nil {
		return time.Time{}, err
	}
	if receiptContext.Mutation != ReceiptMutationAddEdge {
		return time.Time{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation,
			ReceiptMutationAddEdge,
		)
	}
	if ttl <= 0 {
		return time.Time{}, nil
	}
	issuedAt, err := receiptContext.OperationIDs[0].IssuedAt()
	if err != nil {
		return time.Time{}, err
	}
	expiration := issuedAt.Add(ttl)
	if err := timestamppb.New(expiration).CheckValid(); err != nil {
		return time.Time{}, invalidReceiptError("Edge Add expiration: %v", err)
	}
	return expiration, nil
}
