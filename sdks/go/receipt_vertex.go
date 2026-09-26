package client

import (
	"context"
	"math"
	"time"
	"unicode/utf8"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// VertexPutReceiptResult is one request-index-aligned exact Vertex Put result.
// Outcome is the original server application-time outcome, not current
// liveness when a replay response arrives.
type VertexPutReceiptResult struct {
	Key         string
	OperationID ReceiptOperationID
	Outcome     PutOutcome
}

// VertexDeleteReceiptResult is one request-index-aligned exact Vertex Delete
// result.
type VertexDeleteReceiptResult struct {
	Key         string
	OperationID ReceiptOperationID
	Existed     bool
}

// PutVerticesWithReceipt atomically upserts one logical batch using a
// caller-owned ReceiptContext. It never chunks the batch. Persist
// receiptContext before the first call and reuse it byte-for-byte after an
// ambiguous response.
func (l *Lantern) PutVerticesWithReceipt(
	ctx context.Context,
	inputs []VertexInput,
	receiptContext ReceiptContext,
) ([]VertexPutReceiptResult, error) {
	return l.putVerticesWithReceipt(ctx, inputs, receiptContext, false)
}

// PutVerticesIfAbsentWithReceipt is PutVerticesWithReceipt with an atomic
// per-key live-value existence condition.
func (l *Lantern) PutVerticesIfAbsentWithReceipt(
	ctx context.Context,
	inputs []VertexInput,
	receiptContext ReceiptContext,
) ([]VertexPutReceiptResult, error) {
	return l.putVerticesWithReceipt(ctx, inputs, receiptContext, true)
}

func (l *Lantern) putVerticesWithReceipt(
	ctx context.Context,
	inputs []VertexInput,
	receiptContext ReceiptContext,
	ifAbsent bool,
) ([]VertexPutReceiptResult, error) {
	request, stableInputs, stableContext, err := receiptPutVerticesRequest(
		inputs,
		receiptContext,
		ifAbsent,
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()

	var response *pb.PutVerticesResponse
	err = l.executeReceiptMutation(ctx, ReceiptMutationPutVertex, stableContext.Continuity, func(ctx context.Context) error {
		resp, err := unaryOnce(ctx, request, l.client.PutVertices)
		if err == nil {
			response = resp
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return vertexPutReceiptResults(stableInputs, stableContext.OperationIDs, response)
}

// PutVertexWithReceipt is the one-item, relative-TTL facade over
// PutVerticesWithReceipt. A positive ttl starts at the persisted operation
// ID's issuance time so a later replay keeps the exact absolute expiration.
// A non-positive ttl stores the vertex permanently.
func (l *Lantern) PutVertexWithReceipt(
	ctx context.Context,
	key string,
	value any,
	ttl time.Duration,
	receiptContext ReceiptContext,
) (VertexPutReceiptResult, error) {
	expiration, err := receiptPutExpirationFromTTL(receiptContext, ttl)
	if err != nil {
		return VertexPutReceiptResult{}, err
	}
	return l.PutVertexAtWithReceipt(ctx, key, value, expiration, receiptContext)
}

// PutVertexAtWithReceipt is the one-item, absolute-expiration facade over
// PutVerticesWithReceipt.
func (l *Lantern) PutVertexAtWithReceipt(
	ctx context.Context,
	key string,
	value any,
	expiration time.Time,
	receiptContext ReceiptContext,
) (VertexPutReceiptResult, error) {
	results, err := l.PutVerticesWithReceipt(
		ctx,
		[]VertexInput{{Key: key, Value: value, Expiration: expiration}},
		receiptContext,
	)
	if err != nil {
		return VertexPutReceiptResult{}, err
	}
	if len(results) != 1 {
		return VertexPutReceiptResult{}, receiptProtocolError("singular Vertex Put returned %d items", len(results))
	}
	return results[0], nil
}

// PutVertexIfAbsentWithReceipt is the one-item, relative-TTL facade over
// PutVerticesIfAbsentWithReceipt. A positive ttl starts at the persisted
// operation ID's issuance time so a later replay keeps the exact absolute
// expiration.
func (l *Lantern) PutVertexIfAbsentWithReceipt(
	ctx context.Context,
	key string,
	value any,
	ttl time.Duration,
	receiptContext ReceiptContext,
) (VertexPutReceiptResult, error) {
	expiration, err := receiptPutExpirationFromTTL(receiptContext, ttl)
	if err != nil {
		return VertexPutReceiptResult{}, err
	}
	return l.PutVertexIfAbsentAtWithReceipt(ctx, key, value, expiration, receiptContext)
}

// PutVertexIfAbsentAtWithReceipt is the one-item, absolute-expiration facade
// over PutVerticesIfAbsentWithReceipt.
func (l *Lantern) PutVertexIfAbsentAtWithReceipt(
	ctx context.Context,
	key string,
	value any,
	expiration time.Time,
	receiptContext ReceiptContext,
) (VertexPutReceiptResult, error) {
	results, err := l.PutVerticesIfAbsentWithReceipt(
		ctx,
		[]VertexInput{{Key: key, Value: value, Expiration: expiration}},
		receiptContext,
	)
	if err != nil {
		return VertexPutReceiptResult{}, err
	}
	if len(results) != 1 {
		return VertexPutReceiptResult{}, receiptProtocolError(
			"singular conditional Vertex Put returned %d items",
			len(results),
		)
	}
	return results[0], nil
}

// DeleteVerticesWithReceipt atomically removes one logical batch using a
// caller-owned ReceiptContext and returns one exact existence result per key.
// It never chunks the batch.
func (l *Lantern) DeleteVerticesWithReceipt(
	ctx context.Context,
	keys []string,
	receiptContext ReceiptContext,
) ([]VertexDeleteReceiptResult, error) {
	request, stableKeys, stableContext, err := receiptDeleteVerticesRequest(keys, receiptContext)
	if err != nil {
		return nil, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()

	var response *pb.DeleteVerticesResponse
	err = l.executeReceiptMutation(ctx, ReceiptMutationDeleteVertex, stableContext.Continuity, func(ctx context.Context) error {
		resp, err := unaryOnce(ctx, request, l.client.DeleteVertices)
		if err == nil {
			response = resp
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return vertexDeleteReceiptResults(stableKeys, stableContext.OperationIDs, response)
}

// DeleteVertexWithReceipt is the one-item facade over
// DeleteVerticesWithReceipt.
func (l *Lantern) DeleteVertexWithReceipt(
	ctx context.Context,
	key string,
	receiptContext ReceiptContext,
) (VertexDeleteReceiptResult, error) {
	results, err := l.DeleteVerticesWithReceipt(ctx, []string{key}, receiptContext)
	if err != nil {
		return VertexDeleteReceiptResult{}, err
	}
	if len(results) != 1 {
		return VertexDeleteReceiptResult{}, receiptProtocolError(
			"singular Vertex Delete returned %d items",
			len(results),
		)
	}
	return results[0], nil
}

func receiptPutVerticesRequest(
	inputs []VertexInput,
	receiptContext ReceiptContext,
	ifAbsent bool,
) (*pb.PutVerticesRequest, []VertexInput, ReceiptContext, error) {
	if len(inputs) == 0 || uint64(len(inputs)) > math.MaxUint32 {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt Vertex Put item count must be between 1 and %d",
			uint64(math.MaxUint32),
		)
	}
	if err := receiptContext.Validate(len(inputs)); err != nil {
		return nil, nil, ReceiptContext{}, err
	}
	if receiptContext.Mutation != ReceiptMutationPutVertex {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation,
			ReceiptMutationPutVertex,
		)
	}
	stableContext := receiptContext.Clone()
	stableInputs := make([]VertexInput, len(inputs))
	vertices := make([]*pb.Vertex, len(inputs))
	for i, input := range inputs {
		if input.Key == "" || !utf8.ValidString(input.Key) {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"vertices[%d] must have a nonempty UTF-8 key",
				i,
			)
		}
		stableInputs[i] = input
		if value, ok := input.Value.([]byte); ok {
			stableInputs[i].Value = append([]byte(nil), value...)
		}
		vertex, err := nativeVertex{
			key:        stableInputs[i].Key,
			value:      stableInputs[i].Value,
			expiration: stableInputs[i].Expiration,
		}.asVertex()
		if err != nil {
			return nil, nil, ReceiptContext{}, err
		}
		if err := vertex.GetExpiration().CheckValid(); err != nil {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"vertices[%d] has invalid expiration: %v",
				i,
				err,
			)
		}
		vertices[i] = proto.Clone(vertex).(*pb.Vertex)
	}
	return &pb.PutVerticesRequest{
		Vertices:       vertices,
		IfAbsent:       ifAbsent,
		ReceiptContext: receiptContextToProto(stableContext),
	}, stableInputs, stableContext, nil
}

func receiptPutExpirationFromTTL(
	receiptContext ReceiptContext,
	ttl time.Duration,
) (time.Time, error) {
	if err := receiptContext.Validate(1); err != nil {
		return time.Time{}, err
	}
	if receiptContext.Mutation != ReceiptMutationPutVertex {
		return time.Time{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation,
			ReceiptMutationPutVertex,
		)
	}
	if ttl <= 0 {
		return time.Time{}, nil
	}
	issuedAt, err := receiptContext.OperationIDs[0].IssuedAt()
	if err != nil {
		return time.Time{}, err
	}
	return issuedAt.Add(ttl), nil
}

func receiptDeleteVerticesRequest(
	keys []string,
	receiptContext ReceiptContext,
) (*pb.DeleteVerticesRequest, []string, ReceiptContext, error) {
	if len(keys) == 0 || uint64(len(keys)) > math.MaxUint32 {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt Vertex Delete item count must be between 1 and %d",
			uint64(math.MaxUint32),
		)
	}
	if err := receiptContext.Validate(len(keys)); err != nil {
		return nil, nil, ReceiptContext{}, err
	}
	if receiptContext.Mutation != ReceiptMutationDeleteVertex {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation,
			ReceiptMutationDeleteVertex,
		)
	}
	stableContext := receiptContext.Clone()
	stableKeys := append([]string(nil), keys...)
	for i, key := range stableKeys {
		if key == "" || !utf8.ValidString(key) {
			return nil, nil, ReceiptContext{}, invalidReceiptError(
				"keys[%d] must be nonempty UTF-8",
				i,
			)
		}
	}
	return &pb.DeleteVerticesRequest{
		Keys:           stableKeys,
		ReceiptContext: receiptContextToProto(stableContext),
	}, stableKeys, stableContext, nil
}

func vertexPutReceiptResults(
	inputs []VertexInput,
	operationIDs []ReceiptOperationID,
	response *pb.PutVerticesResponse,
) ([]VertexPutReceiptResult, error) {
	if response == nil {
		return nil, receiptProtocolError("Vertex Put response is nil")
	}
	if len(response.GetOutcomes()) != len(inputs) {
		return nil, receiptProtocolError(
			"Vertex Put outcome count %d does not match request count %d",
			len(response.GetOutcomes()),
			len(inputs),
		)
	}
	results := make([]VertexPutReceiptResult, len(inputs))
	for i, rawOutcome := range response.GetOutcomes() {
		outcome, err := putOutcomeFromProto(rawOutcome)
		if err != nil {
			return nil, receiptProtocolError("Vertex Put outcome[%d]: %v", i, err)
		}
		results[i] = VertexPutReceiptResult{
			Key:         inputs[i].Key,
			OperationID: operationIDs[i],
			Outcome:     outcome,
		}
	}
	return results, nil
}

func vertexDeleteReceiptResults(
	keys []string,
	operationIDs []ReceiptOperationID,
	response *pb.DeleteVerticesResponse,
) ([]VertexDeleteReceiptResult, error) {
	if response == nil {
		return nil, receiptProtocolError("Vertex Delete response is nil")
	}
	if len(response.GetExisted()) != len(keys) {
		return nil, receiptProtocolError(
			"Vertex Delete outcome count %d does not match request count %d",
			len(response.GetExisted()),
			len(keys),
		)
	}
	var deleted int32
	results := make([]VertexDeleteReceiptResult, len(keys))
	for i, existed := range response.GetExisted() {
		if existed {
			deleted++
		}
		results[i] = VertexDeleteReceiptResult{
			Key:         keys[i],
			OperationID: operationIDs[i],
			Existed:     existed,
		}
	}
	if response.GetDeleted() != deleted {
		return nil, receiptProtocolError(
			"Vertex Delete deleted=%d does not match %d true outcomes",
			response.GetDeleted(),
			deleted,
		)
	}
	return results, nil
}
