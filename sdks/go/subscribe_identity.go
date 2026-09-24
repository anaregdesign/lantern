package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ErrInvalidIdentityCursor means a requested next-sequence cursor or a
// derived checkpoint cursor cannot be represented safely.
var ErrInvalidIdentityCursor = errors.New("invalid identity cursor")

// ErrInvalidIdentityEvent means the peer sent an unexpected or malformed
// identity-only Subscribe frame. Treat the stream as gapped and recover.
var ErrInvalidIdentityEvent = errors.New("invalid identity event")

// ErrIdentityGap means the identity stream cannot prove contiguous progress.
// Bootstrap again and revalidate resident keys before trusting cached data.
var ErrIdentityGap = errors.New("identity change gap")

// ErrIncompleteIdentityMutation means the chunk does not finish a mutation.
// Applying its identities alone cannot advance the durable origin cursor.
var ErrIncompleteIdentityMutation = errors.New("incomplete identity mutation")

// IdentityOperation is the payload-free category of an identity chunk.
type IdentityOperation = pb.IdentityOperation

const (
	IdentityPutVertex    = pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX
	IdentityDeleteVertex = pb.IdentityOperation_IDENTITY_OPERATION_DELETE_VERTEX
	IdentityAddEdge      = pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE
	IdentityPutEdge      = pb.IdentityOperation_IDENTITY_OPERATION_PUT_EDGE
	IdentityDeleteEdge   = pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE
	IdentityReceiptOnly  = pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY
)

// IdentityChange is either an *IdentityCheckpoint or an *IdentityChunk.
// A checkpoint is a single responder's atomic publication cut, never a
// cluster-wide freshness assertion. The interface prevents graph payloads
// from entering this opt-in stream API.
type IdentityChange interface{ isIdentityChange() }

// IdentityCheckpoint carries the last contiguous published sequence per
// origin at the responder's bootstrap cut. Use NextCursor only after the
// application has marked resident records Unknown and begun revalidation.
type IdentityCheckpoint struct {
	LastSeqPerOrigin map[ChangeOrigin]uint64
}

func (*IdentityCheckpoint) isIdentityChange() {}

// NextCursor converts inclusive last-sequence positions to the next-sequence
// cursor used by SubscribeIdentity. It rejects uint64 overflow.
func (c *IdentityCheckpoint) NextCursor() (ChangeCursor, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil checkpoint", ErrInvalidIdentityCursor)
	}
	next := make(ChangeCursor, len(c.LastSeqPerOrigin))
	for origin, last := range c.LastSeqPerOrigin {
		if zeroChangeOrigin(origin) || last == math.MaxUint64 {
			return nil, fmt.Errorf("%w: invalid checkpoint origin or sequence", ErrInvalidIdentityCursor)
		}
		next[origin] = last + 1
	}
	return next, nil
}

// IdentityHLC is the mutation's causal coordinate. Its Node is the origin;
// HLC ordering does not replace the per-origin cursor.
type IdentityHLC struct {
	WallNS  int64
	Logical uint32
	Node    ChangeOrigin
}

// IdentityChunk is one bounded fragment of exact Vertex or Edge identities.
// IsLast closes the mutation even when both identity slices are empty.
// ChunkIndex and FirstItemIndex let consumers durably assemble/replay a large
// mutation; neither is an origin cursor. No value, weight, contribution ID,
// credentials, or graph payload is represented here.
type IdentityChunk struct {
	Origin         ChangeOrigin
	Seq            uint64
	HLC            IdentityHLC
	Operation      IdentityOperation
	ChunkIndex     uint32
	FirstItemIndex uint32
	IsLast         bool
	VertexKeys     []string
	EdgeKeys       []EdgeRef
}

func (*IdentityChunk) isIdentityChange() {}

// NextCursor returns a copy of current advanced through this final chunk.
// The caller must first durably apply every chunk of the mutation and commit
// its identities with this cursor. Replayed final chunks are idempotent;
// skips and uint64 overflow fail closed. This method does not persist state.
func (c *IdentityChunk) NextCursor(current ChangeCursor) (ChangeCursor, error) {
	if c == nil || !c.IsLast {
		return nil, ErrIncompleteIdentityMutation
	}
	if zeroChangeOrigin(c.Origin) || c.Seq == 0 || c.Seq == math.MaxUint64 {
		return nil, fmt.Errorf("%w: invalid chunk origin or sequence", ErrInvalidIdentityCursor)
	}
	next, err := copyIdentityCursor(current, false)
	if err != nil {
		return nil, err
	}
	want := next[c.Origin]
	if want == 0 {
		want = 1
	}
	if c.Seq > want {
		return nil, fmt.Errorf("%w: origin %s expected seq %d, received %d", ErrIdentityGap, c.Origin, want, c.Seq)
	}
	if c.Seq == want {
		next[c.Origin] = c.Seq + 1
	}
	return next, nil
}

func zeroChangeOrigin(origin ChangeOrigin) bool { return origin == (ChangeOrigin{}) }

func copyIdentityCursor(cursor ChangeCursor, requireNonEmpty bool) (ChangeCursor, error) {
	if requireNonEmpty && len(cursor) == 0 {
		return nil, fmt.Errorf("%w: resume requires a nonempty next-sequence cursor; use BootstrapIdentity first", ErrInvalidIdentityCursor)
	}
	copy := make(ChangeCursor, len(cursor))
	for origin, seq := range cursor {
		if zeroChangeOrigin(origin) || seq == 0 {
			return nil, fmt.Errorf("%w: origin %s has invalid next sequence %d", ErrInvalidIdentityCursor, origin, seq)
		}
		copy[origin] = seq
	}
	return copy, nil
}

func parseIdentityCheckpoint(wire *pb.IdentityCheckpoint) (*IdentityCheckpoint, error) {
	if wire == nil || proto.Size(&pb.SubscribeResponse{Event: &pb.SubscribeResponse_Checkpoint{Checkpoint: wire}}) > 1<<20 {
		return nil, fmt.Errorf("%w: missing or oversized checkpoint", ErrInvalidIdentityEvent)
	}
	checkpoint := &IdentityCheckpoint{LastSeqPerOrigin: make(map[ChangeOrigin]uint64, len(wire.GetLastSeqPerOrigin()))}
	for encoded, last := range wire.GetLastSeqPerOrigin() {
		origin, err := ParseChangeOrigin(encoded)
		if err != nil || zeroChangeOrigin(origin) || origin.String() != encoded || last == math.MaxUint64 {
			return nil, fmt.Errorf("%w: malformed checkpoint position", ErrInvalidIdentityEvent)
		}
		checkpoint.LastSeqPerOrigin[origin] = last
	}
	return checkpoint, nil
}

func parseIdentityChunk(wire *pb.IdentityChunk) (*IdentityChunk, error) {
	if wire == nil || proto.Size(&pb.SubscribeResponse{Event: &pb.SubscribeResponse_IdentityChunk{IdentityChunk: wire}}) > 1<<20 || len(wire.GetVertexKeys())+len(wire.GetEdgeKeys()) > 1024 || wire.GetSeq() == 0 || wire.GetSeq() == math.MaxUint64 {
		return nil, fmt.Errorf("%w: malformed or oversized chunk", ErrInvalidIdentityEvent)
	}
	origin, err := ChangeOriginFromBytes(wire.GetOrigin())
	if err != nil || zeroChangeOrigin(origin) {
		return nil, fmt.Errorf("%w: malformed chunk origin", ErrInvalidIdentityEvent)
	}
	hlc := wire.GetHlc()
	if hlc == nil || string(hlc.GetNodeId()) != string(origin[:]) {
		return nil, fmt.Errorf("%w: chunk HLC origin mismatch", ErrInvalidIdentityEvent)
	}
	switch wire.GetOperation() {
	case IdentityPutVertex, IdentityDeleteVertex:
		if len(wire.GetEdgeKeys()) != 0 {
			return nil, fmt.Errorf("%w: vertex chunk contains Edge keys", ErrInvalidIdentityEvent)
		}
	case IdentityAddEdge, IdentityPutEdge, IdentityDeleteEdge:
		if len(wire.GetVertexKeys()) != 0 {
			return nil, fmt.Errorf("%w: Edge chunk contains Vertex keys", ErrInvalidIdentityEvent)
		}
	case IdentityReceiptOnly:
		if len(wire.GetVertexKeys()) != 0 || len(wire.GetEdgeKeys()) != 0 ||
			!wire.GetIsLast() || wire.GetChunkIndex() != 0 || wire.GetFirstItemIndex() != 0 {
			return nil, fmt.Errorf("%w: receipt-only marker must be one final zero-key chunk", ErrInvalidIdentityEvent)
		}
	default:
		return nil, fmt.Errorf("%w: unknown operation %d", ErrInvalidIdentityEvent, wire.GetOperation())
	}
	count := uint64(len(wire.GetVertexKeys()) + len(wire.GetEdgeKeys()))
	if (!wire.GetIsLast() && count == 0) || uint64(wire.GetFirstItemIndex())+count > math.MaxUint32 {
		return nil, fmt.Errorf("%w: invalid chunk item bounds", ErrInvalidIdentityEvent)
	}
	chunk := &IdentityChunk{
		Origin:         origin,
		Seq:            wire.GetSeq(),
		HLC:            IdentityHLC{WallNS: hlc.GetWallNs(), Logical: hlc.GetLogical(), Node: origin},
		Operation:      wire.GetOperation(),
		ChunkIndex:     wire.GetChunkIndex(),
		FirstItemIndex: wire.GetFirstItemIndex(),
		IsLast:         wire.GetIsLast(),
		VertexKeys:     append([]string(nil), wire.GetVertexKeys()...),
		EdgeKeys:       make([]EdgeRef, 0, len(wire.GetEdgeKeys())),
	}
	for _, edge := range wire.GetEdgeKeys() {
		if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" {
			return nil, fmt.Errorf("%w: empty Edge identity", ErrInvalidIdentityEvent)
		}
		chunk.EdgeKeys = append(chunk.EdgeKeys, EdgeRef{Tail: edge.GetTail(), Head: edge.GetHead()})
	}
	for _, key := range chunk.VertexKeys {
		if key == "" {
			return nil, fmt.Errorf("%w: empty Vertex identity", ErrInvalidIdentityEvent)
		}
	}
	return chunk, nil
}

type identityStreamTracker struct {
	expected ChangeCursor
	active   *identityChunkProgress
}

type identityChunkProgress struct {
	origin    ChangeOrigin
	seq       uint64
	operation IdentityOperation
	index     uint32
	nextItem  uint64
}

func (t *identityStreamTracker) accept(chunk *IdentityChunk) error {
	count := uint64(len(chunk.VertexKeys) + len(chunk.EdgeKeys))
	if previous := t.active; previous != nil {
		if chunk.Origin != previous.origin || chunk.Seq != previous.seq || chunk.Operation != previous.operation || previous.index == math.MaxUint32 || chunk.ChunkIndex != previous.index+1 || uint64(chunk.FirstItemIndex) != previous.nextItem {
			return fmt.Errorf("%w: noncontiguous mutation chunk", ErrInvalidIdentityEvent)
		}
	} else {
		want := t.expected[chunk.Origin]
		if want == 0 {
			want = 1
		}
		if chunk.Seq != want {
			return fmt.Errorf("%w: origin %s expected seq %d, received %d", ErrIdentityGap, chunk.Origin, want, chunk.Seq)
		}
		if chunk.ChunkIndex != 0 || chunk.FirstItemIndex != 0 {
			return fmt.Errorf("%w: mutation must start with chunk and item index zero", ErrInvalidIdentityEvent)
		}
	}
	if chunk.IsLast {
		t.expected[chunk.Origin] = chunk.Seq + 1
		t.active = nil
	} else if count == 0 {
		return fmt.Errorf("%w: empty non-final chunk", ErrInvalidIdentityEvent)
	} else {
		t.active = &identityChunkProgress{
			origin: chunk.Origin, seq: chunk.Seq, operation: chunk.Operation,
			index: chunk.ChunkIndex, nextItem: uint64(chunk.FirstItemIndex) + count,
		}
	}
	return nil
}

func identityStreamError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err == nil || errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: identity stream ended without a cursor-safe terminal event", ErrIdentityGap)
	}
	wrapped := wrapConnectErr(err)
	if connect.CodeOf(err) == connect.CodeFailedPrecondition {
		return errors.Join(ErrIdentityGap, wrapped)
	}
	return wrapped
}

// BootstrapIdentity opens an identity-only stream with an atomic responder
// checkpoint as its first event. After marking resident keys Unknown, callers
// can revalidate them in bounded reads while applying subsequent chunks.
// A checkpoint alone does not prove cluster-wide freshness. Stop iteration
// or cancel ctx to close the stream and release its subscriber.
func (l *Lantern) BootstrapIdentity(ctx context.Context) iter.Seq2[IdentityChange, error] {
	return l.streamIdentity(ctx, nil, true)
}

// SubscribeIdentity resumes the identity-only stream with a nonempty vector
// of next per-origin sequences. Origins absent from the vector start at seq 1.
// Keep the vector durable and use the same one across endpoint failover;
// duplicates are possible. A gap requires BootstrapIdentity and resident-key
// revalidation. Stop iteration or cancel ctx to release the subscriber.
func (l *Lantern) SubscribeIdentity(ctx context.Context, cursor ChangeCursor) iter.Seq2[IdentityChange, error] {
	return l.streamIdentity(ctx, cursor, false)
}

func (l *Lantern) streamIdentity(ctx context.Context, cursor ChangeCursor, bootstrap bool) iter.Seq2[IdentityChange, error] {
	return func(yield func(IdentityChange, error) bool) {
		checked, err := copyIdentityCursor(cursor, !bootstrap)
		if err != nil {
			yield(nil, err)
			return
		}
		wire := make(map[string]uint64, len(checked))
		for origin, seq := range checked {
			wire[origin.String()] = seq
		}
		stream, err := l.replicationClient.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
			FromSeqPerOrigin: wire,
			Projection:       pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
			Bootstrap:        bootstrap,
		}))
		if err != nil {
			yield(nil, identityStreamError(ctx, err))
			return
		}
		defer func() { _ = stream.Close() }()
		tracker := identityStreamTracker{expected: checked}
		checkpointSeen := false
		for stream.Receive() {
			frame := stream.Msg()
			if bootstrap && !checkpointSeen {
				checkpoint, parseErr := parseIdentityCheckpoint(frame.GetCheckpoint())
				if parseErr == nil {
					tracker.expected, parseErr = checkpoint.NextCursor()
				}
				if parseErr != nil {
					yield(nil, errors.Join(ErrIdentityGap, parseErr))
					return
				}
				checkpointSeen = true
				if !yield(checkpoint, nil) {
					return
				}
				continue
			}
			if frame.GetCheckpoint() != nil || frame.GetMutation() != nil {
				yield(nil, errors.Join(ErrIdentityGap, ErrInvalidIdentityEvent))
				return
			}
			chunk, parseErr := parseIdentityChunk(frame.GetIdentityChunk())
			if parseErr == nil {
				parseErr = tracker.accept(chunk)
			}
			if parseErr != nil {
				yield(nil, errors.Join(ErrIdentityGap, parseErr))
				return
			}
			if !yield(chunk, nil) {
				return
			}
		}
		if bootstrap && !checkpointSeen && stream.Err() == nil {
			yield(nil, errors.Join(ErrIdentityGap, ErrInvalidIdentityEvent))
			return
		}
		yield(nil, identityStreamError(ctx, stream.Err()))
	}
}
