package client

import (
	"context"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// EdgeAddInput is one receipt-less Add with a caller-owned contribution ID.
// Persist the ID and exact input before sending so a later selective Delete
// can name the contribution even if the Add response is lost.
type EdgeAddInput struct {
	Edge      EdgeInput
	ContribID ContribID
}

// AddEdgesWithIDs sends caller-owned contribution IDs in a chunked Add batch.
// The returned effective weights are request-index aligned. A lost response
// still leaves the original result uncertain: IDs alone are not receipts, and
// neither WithRetry nor Failover automatically replays this mutation.
func (l *Lantern) AddEdgesWithIDs(ctx context.Context, inputs []EdgeAddInput) ([]float32, error) {
	edges, ids, err := explicitAddInputs(inputs)
	if err != nil {
		return nil, err
	}
	weights, err := l.addEdgesWithIDs(ctx, edges, ids)
	if err != nil {
		return nil, err
	}
	if len(weights) != len(inputs) {
		return nil, fmt.Errorf("lantern: server returned %d Add outcomes for %d contributions", len(weights), len(inputs))
	}
	return weights, nil
}

// AddEdgeWithID is the one-item relative-TTL facade over AddEdgesWithIDs.
func (l *Lantern) AddEdgeWithID(ctx context.Context, tail, head string, weight float32, ttl time.Duration, id ContribID) (float32, error) {
	return l.AddEdgeAtWithID(ctx, tail, head, weight, expirationFromTTL(ttl), id)
}

// AddEdgeAtWithID is the one-item absolute-expiration facade over AddEdgesWithIDs.
func (l *Lantern) AddEdgeAtWithID(ctx context.Context, tail, head string, weight float32, expiration time.Time, id ContribID) (float32, error) {
	weights, err := l.AddEdgesWithIDs(ctx, []EdgeAddInput{{
		Edge:      EdgeInput{Tail: tail, Head: head, Weight: weight, Expiration: expiration},
		ContribID: id,
	}})
	if err != nil {
		return 0, err
	}
	return weights[0], nil
}

func explicitAddInputs(inputs []EdgeAddInput) ([]EdgeInput, [][]byte, error) {
	edges := make([]EdgeInput, len(inputs))
	ids := make([][]byte, len(inputs))
	for i, input := range inputs {
		edge := input.Edge
		if edge.Tail == "" || edge.Head == "" || !utf8.ValidString(edge.Tail) || !utf8.ValidString(edge.Head) {
			return nil, nil, fmt.Errorf("%w: contributions[%d] requires nonempty UTF-8 tail and head", ErrInvalidArgument, i)
		}
		if math.IsNaN(float64(edge.Weight)) || math.IsInf(float64(edge.Weight), 0) {
			return nil, nil, fmt.Errorf("%w: contributions[%d] weight must be finite", ErrInvalidArgument, i)
		}
		if expiration := expirationTimestamp(edge.Expiration); expiration != nil {
			if err := expiration.CheckValid(); err != nil {
				return nil, nil, fmt.Errorf("%w: contributions[%d] expiration: %v", ErrInvalidArgument, i, err)
			}
		}
		if input.ContribID == (ContribID{}) {
			return nil, nil, fmt.Errorf("%w: contributions[%d] ID must be a nonzero 24-byte ContribID", ErrInvalidArgument, i)
		}
		edges[i] = edge
		ids[i] = input.ContribID.Bytes()
	}
	return edges, ids, nil
}
