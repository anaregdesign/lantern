package client

import (
	"errors"
	"math"
	"reflect"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestIdentityCursorCheckedAdvancement(t *testing.T) {
	origin := ChangeOrigin{0x31}
	other := ChangeOrigin{0x42}
	checkpoint := &IdentityCheckpoint{LastSeqPerOrigin: map[ChangeOrigin]uint64{origin: 4, other: 0}}
	next, err := checkpoint.NextCursor()
	if err != nil || !reflect.DeepEqual(next, ChangeCursor{origin: 5, other: 1}) {
		t.Fatalf("checkpoint NextCursor = (%v,%v)", next, err)
	}
	checkpoint.LastSeqPerOrigin[origin] = 99
	if next[origin] != 5 {
		t.Fatal("checkpoint cursor must be copied")
	}
	checkpoint.LastSeqPerOrigin[origin] = math.MaxUint64
	if _, err := checkpoint.NextCursor(); !errors.Is(err, ErrInvalidIdentityCursor) {
		t.Fatalf("overflow = %v", err)
	}

	chunk := &IdentityChunk{Origin: origin, Seq: 5}
	if _, err := chunk.NextCursor(next); !errors.Is(err, ErrIncompleteIdentityMutation) {
		t.Fatalf("nonfinal cursor = %v", err)
	}
	chunk.IsLast = true
	advanced, err := chunk.NextCursor(next)
	if err != nil || advanced[origin] != 6 || next[origin] != 5 || advanced[other] != 1 {
		t.Fatalf("final NextCursor = (%v,%v); input=%v", advanced, err, next)
	}
	duplicate, err := chunk.NextCursor(advanced)
	if err != nil || !reflect.DeepEqual(duplicate, advanced) {
		t.Fatalf("duplicate final NextCursor = (%v,%v)", duplicate, err)
	}
	chunk.Seq = 7
	if _, err := chunk.NextCursor(next); !errors.Is(err, ErrIdentityGap) {
		t.Fatalf("skipped seq = %v", err)
	}
	chunk.Seq = math.MaxUint64
	if _, err := chunk.NextCursor(next); !errors.Is(err, ErrInvalidIdentityCursor) {
		t.Fatalf("overflow seq = %v", err)
	}
	if _, err := copyIdentityCursor(nil, true); !errors.Is(err, ErrInvalidIdentityCursor) {
		t.Fatalf("empty resume = %v", err)
	}
	if _, err := copyIdentityCursor(ChangeCursor{origin: 0}, true); !errors.Is(err, ErrInvalidIdentityCursor) {
		t.Fatalf("zero next seq = %v", err)
	}
}

func TestIdentityFrameParsingAndChunkContinuity(t *testing.T) {
	origin := ChangeOrigin{0x31}
	wire := &pb.IdentityChunk{
		Origin: append([]byte(nil), origin[:]...), Seq: 2, Hlc: &pb.HLCTimestamp{NodeId: append([]byte(nil), origin[:]...), WallNs: 10},
		Operation: IdentityDeleteEdge, EdgeKeys: []*pb.EdgeKey{{Tail: "a", Head: "b"}},
	}
	chunk, err := parseIdentityChunk(wire)
	if err != nil || chunk.Origin != origin || chunk.EdgeKeys[0] != (EdgeRef{Tail: "a", Head: "b"}) {
		t.Fatalf("parse chunk = (%+v,%v)", chunk, err)
	}
	wire.EdgeKeys[0].Tail = "changed"
	wire.Origin[0] = 0xff
	if chunk.EdgeKeys[0].Tail != "a" || chunk.Origin != origin {
		t.Fatal("parsed chunk aliases mutable wire data")
	}
	tracker := identityStreamTracker{expected: ChangeCursor{origin: 2}}
	if err := tracker.accept(chunk); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	last := *chunk
	last.ChunkIndex = 1
	last.FirstItemIndex = 1
	last.IsLast = true
	if err := tracker.accept(&last); err != nil || tracker.expected[origin] != 3 {
		t.Fatalf("final chunk = (%v,%v)", tracker.expected, err)
	}
	if err := tracker.accept(&last); !errors.Is(err, ErrIdentityGap) {
		t.Fatalf("repeated chunk in same stream = %v", err)
	}

	bad := *wire
	bad.Origin = origin[:]
	bad.Hlc = &pb.HLCTimestamp{NodeId: []byte{0x99}}
	if _, err := parseIdentityChunk(&bad); !errors.Is(err, ErrInvalidIdentityEvent) {
		t.Fatalf("HLC mismatch = %v", err)
	}
	bad.Hlc = &pb.HLCTimestamp{NodeId: origin[:]}
	bad.Operation = pb.IdentityOperation_IDENTITY_OPERATION_UNSPECIFIED
	if _, err := parseIdentityChunk(&bad); !errors.Is(err, ErrInvalidIdentityEvent) {
		t.Fatalf("unknown operation = %v", err)
	}
	bad.Operation = IdentityDeleteEdge
	bad.EdgeKeys = nil
	bad.VertexKeys = []string{"not-an-edge"}
	if _, err := parseIdentityChunk(&bad); !errors.Is(err, ErrInvalidIdentityEvent) {
		t.Fatalf("mixed operation identity = %v", err)
	}
	checkpoint, err := parseIdentityCheckpoint(&pb.IdentityCheckpoint{LastSeqPerOrigin: map[string]uint64{origin.String(): 2}})
	if err != nil || checkpoint.LastSeqPerOrigin[origin] != 2 {
		t.Fatalf("parse checkpoint = (%v,%v)", checkpoint, err)
	}
	if _, err := parseIdentityCheckpoint(&pb.IdentityCheckpoint{LastSeqPerOrigin: map[string]uint64{"BAD": 2}}); !errors.Is(err, ErrInvalidIdentityEvent) {
		t.Fatalf("malformed checkpoint = %v", err)
	}
}
