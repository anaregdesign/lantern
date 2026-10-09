package security

import (
	"bytes"
	"math"
	"strings"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCurrentPublicOutputCodecBoundsBeforeMarshal(t *testing.T) {
	values := []proto.Message{
		&pb.GetCurrentPrincipalResponse{},
		&pb.Vertex{Key: "x\x00\"\\\n日本語", Value: &pb.Vertex_String_{String_: strings.Repeat("escaped\n", 100)}},
		&pb.Vertex{Value: &pb.Vertex_Bytes{Bytes: bytes.Repeat([]byte{255}, 1025)}},
		&pb.Vertex{Value: &pb.Vertex_Int64{Int64: math.MinInt64}},
		&pb.Vertex{Value: &pb.Vertex_Uint64{Uint64: math.MaxUint64}},
		&pb.Vertex{Value: &pb.Vertex_Float64{Float64: math.Inf(1)}},
		&pb.Vertex{Value: &pb.Vertex_Timestamp{Timestamp: &timestamppb.Timestamp{Seconds: 253402300799, Nanos: 999999999}}},
		&pb.Vertex{Value: &pb.Vertex_Duration{Duration: &durationpb.Duration{Seconds: -315576000000, Nanos: -999999999}}},
		&pb.CurrentSecurityOriginalOutcome{Disposition: pb.CurrentSecurityDisposition(999)},
	}
	for _, message := range values {
		actual, err := protojson.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		budget := currentJSONBudget{remaining: 1 << 20}
		if !budget.message(message.ProtoReflect(), 0) || (1<<20)-budget.remaining < len(actual) {
			t.Fatalf("JSON bound underestimated %T: %d > %d", message, len(actual), (1<<20)-budget.remaining)
		}
		for _, name := range []string{"proto", "json", "json; charset=utf-8"} {
			codec := currentOutputCodec{name: name, read: 1 << 20, send: 1 << 20}
			raw, err := codec.Marshal(message)
			if err != nil {
				t.Fatal(name, err)
			}
			decoded := message.ProtoReflect().Type().New().Interface()
			if err := codec.Unmarshal(raw, decoded); err != nil || !proto.Equal(message, decoded) {
				t.Fatal("round trip", name, err)
			}
		}
	}
	large := &pb.Vertex{Value: &pb.Vertex_String_{String_: strings.Repeat("\x00", 1024)}}
	if _, err := (currentOutputCodec{name: "json", send: 2048}).Marshal(large); err == nil {
		t.Fatal("binary-sized budget admitted expanded JSON")
	}
	if _, err := (currentOutputCodec{name: "proto", send: 1024}).Marshal(large); err == nil {
		t.Fatal("oversized protobuf encoded")
	}
	if err := (currentOutputCodec{name: "json", read: 1024}).Unmarshal([]byte(`{"unrecognized":true}`), &pb.Vertex{}); err == nil {
		t.Fatal("current JSON discarded unknown fields")
	}
}

// Bounded admission/encoding seam, not end-to-end Server ON/OFF throughput.
func BenchmarkCurrentPublicOutputCodec(b *testing.B) {
	message := &pb.GetVerticesResponse{Vertices: []*pb.Vertex{{Key: "orders:one", Value: &pb.Vertex_String_{String_: strings.Repeat("bounded value 日本語\n", 2048)}}}}
	for _, name := range []string{"proto", "json"} {
		b.Run(name, func(b *testing.B) {
			codec := currentOutputCodec{name: name, read: 1 << 20, send: 1 << 20}
			first, err := codec.Marshal(message)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(first)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := codec.Marshal(message); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
