package graphv1_test

// This is one of the few hand-written files under pb/ — the package is
// otherwise buf-generated. It lives here, rather than in a consumer module,
// because the boundary it guards (proto.Unmarshal into the generated message
// types) is the wire-decode surface owned by pb: every read/write RPC crosses
// it with attacker-influenced bytes. buf never touches *_test.go, so the
// "generated code is up to date" CI gate (go generate ./... + git diff) leaves
// it untouched. Precedent: graphv1connect/graphv1connect_test.go is likewise a
// hand-written test inside pb/.

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// addProtoSeeds seeds a fuzz target with a baseline of pathological byte
// inputs (empty, truncated tag, overlong varint) plus the canonical wire
// encoding of each supplied message. Every decoder must survive all of them.
func addProtoSeeds(f *testing.F, msgs ...proto.Message) {
	f.Helper()
	f.Add([]byte(nil))
	f.Add([]byte{})
	f.Add([]byte{0x08})                                                       // field 1, varint tag with no value (truncated)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // overlong/garbage varint
	for _, m := range msgs {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatalf("seed marshal: %v", err)
		}
		f.Add(b)
	}
}

// roundTripProto asserts that a message which decoded cleanly survives a
// Marshal→Unmarshal cycle unchanged. dst must be a fresh zero message of the
// same concrete type as src. proto.Equal treats NaN floats as equal, so
// fuzzed non-finite values do not produce spurious mismatches.
func roundTripProto(t *testing.T, src, dst proto.Message) {
	t.Helper()
	b, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("re-marshal decoded message: %v", err)
	}
	if err := proto.Unmarshal(b, dst); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !proto.Equal(src, dst) {
		t.Fatalf("round-trip mismatch:\n before=%v\n after =%v", src, dst)
	}
}

// FuzzVertexUnmarshal fuzzes the Vertex wire-decode boundary every read RPC
// crosses. Decoding arbitrary bytes must never panic, and any input that
// decodes cleanly must survive a Marshal/Unmarshal round-trip.
func FuzzVertexUnmarshal(f *testing.F) {
	addProtoSeeds(f,
		&pb.Vertex{Key: "alice"},
		&pb.Vertex{Key: "n", Value: &pb.Vertex_Int64{Int64: 42}},
		&pb.Vertex{Key: "s", Value: &pb.Vertex_String_{String_: "hello"}},
		&pb.Vertex{Key: "nil", Value: &pb.Vertex_Nil{Nil: true}},
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		var v pb.Vertex
		if proto.Unmarshal(data, &v) != nil {
			return
		}
		roundTripProto(t, &v, &pb.Vertex{})
	})
}

// FuzzEdgeUnmarshal fuzzes the Edge wire-decode boundary.
func FuzzEdgeUnmarshal(f *testing.F) {
	addProtoSeeds(f,
		&pb.Edge{Tail: "alice", Head: "bob", Weight: 1.5},
		&pb.Edge{Tail: "a", Head: "b"},
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		var e pb.Edge
		if proto.Unmarshal(data, &e) != nil {
			return
		}
		roundTripProto(t, &e, &pb.Edge{})
	})
}

// FuzzIlluminateRequestUnmarshal fuzzes a representative request message: it
// carries scalar, enum, and reserved-field-number surface, making it the
// richest decode in the RPC set.
func FuzzIlluminateRequestUnmarshal(f *testing.F) {
	addProtoSeeds(f,
		&pb.IlluminateRequest{Seed: "alice"},
		&pb.IlluminateRequest{
			Seed:         "alice",
			Weighting:    pb.Weighting_WEIGHTING_TFIDF,
			VertexPrefix: "users/",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{
				Step: 2, FanOut: 5,
				Objective: pb.Objective_OBJECTIVE_MAXIMIZE,
				Reduction: pb.Reduction_REDUCTION_SHORTEST_PATH_TREE,
			}},
		},
		&pb.IlluminateRequest{
			Seed:   "alice",
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 5, RestartProb: 0.2, Epsilon: 1e-3}},
		},
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		var r pb.IlluminateRequest
		if proto.Unmarshal(data, &r) != nil {
			return
		}
		roundTripProto(t, &r, &pb.IlluminateRequest{})
	})
}
