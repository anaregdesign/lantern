package security

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"connectrpc.com/connect"
	_ "connectrpc.com/grpchealth"
	_ "connectrpc.com/grpcreflect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestCurrentPublicDecodeAllPublicSchemasCertified(t *testing.T) {
	methods := 0
	for _, path := range []string{"graph/v1/graph.proto", "graph/v1/security.proto", "graph/v1/changes.proto", "connectext/grpc/health/v1/health.proto", "connectext/grpc/reflection/v1/reflection.proto"} {
		file, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if err != nil {
			t.Fatal(err)
		}
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			ms := services.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				method := ms.Get(j)
				methods++
				typ := currentDecodeSchemas()[method.Input()]
				if typ == nil || typ.Elem().Size() > currentDecodeMaxStruct {
					t.Fatalf("uncertified public input %s", method.FullName())
				}
			}
		}
	}
	if methods < 50 {
		t.Fatal("public method inventory incomplete", methods)
	}
	t.Logf("certified %d public method input schemas, including health/reflection", methods)
}

func TestCurrentPublicDecodeCompactAllocationRegression(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		count int
		raw   []byte
	}{
		{"review-empty-vertices", 128 << 10, 65536, bytes.Repeat([]byte{0x0a, 0}, 65536)},
		{"review-keyed-vertices", 1 << 20, 200000, bytes.Repeat([]byte{0x0a, 3, 0x0a, 1, 'a'}, 200000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "unchanged"}}}
			message := proto.Clone(original).(*pb.PutVerticesRequest)
			c := currentOutputCodec{name: "proto", read: tc.limit, send: tc.limit}
			err := c.Unmarshal(tc.raw, message)
			if connect.CodeOf(err) != connect.CodeResourceExhausted || !proto.Equal(message, original) {
				t.Fatal("must refuse before destination allocation/reset", err)
			}
			allocations := testing.AllocsPerRun(20, func() { _ = c.Unmarshal(tc.raw, message) })
			if allocations > 8 {
				t.Fatal("refusal allocates proportional request structures", allocations)
			}
			reservation := uint64(4*tc.limit+10*tc.limit+1<<20) + currentDecodeCharge(tc.limit)
			minimumOld := uint64(tc.count) * uint64(unsafe.Sizeof(pb.Vertex{})+unsafe.Sizeof((*pb.Vertex)(nil)))
			t.Logf("raw=%d, old_struct_minimum=%d, new_credit=%d, nodes=%d, refused_before_decode=true, allocations=%.0f", len(tc.raw), minimumOld, reservation, currentDecodeNodes(tc.limit), allocations)
		})
	}
}

func TestCurrentPublicDecodeStructuralBoundaries(t *testing.T) {
	const read = 128 << 10
	nodes := currentDecodeNodes(read)
	for _, name := range []string{"proto", "json", "json; charset=utf-8"} {
		for _, delta := range []int{-1, 0, 1} {
			t.Run(name+"/"+map[int]string{-1: "below", 0: "at", 1: "above"}[delta], func(t *testing.T) {
				n := nodes - 1 + delta
				raw := bytes.Repeat([]byte{0x0a, 0}, n) // root + each repeated scalar
				if name != "proto" {
					n = nodes - 4 + delta // root + object + field name + array + values
					raw = []byte(`{"keys":[` + strings.TrimSuffix(strings.Repeat(`"",`, n), ",") + `]}`)
				}
				message := &pb.GetVerticesRequest{Keys: []string{"unchanged"}}
				c := currentOutputCodec{name: name, read: read, send: read}
				err := c.Unmarshal(raw, message)
				if delta > 0 {
					if connect.CodeOf(err) != connect.CodeResourceExhausted || !reflect.DeepEqual(message.Keys, []string{"unchanged"}) {
						t.Fatal("above bound changed destination", err)
					}
				} else if err != nil || len(message.Keys) != n {
					t.Fatal("legal boundary refused", len(message.Keys), n, err)
				}
			})
		}
	}
	// Packed scalars need an element count, not just the containing bytes field.
	for _, kind := range []string{"varint", "fixed32"} {
		for _, delta := range []int{0, 1} {
			n := nodes - 2 + delta
			payload := make([]byte, n)
			var message proto.Message = &pb.DeleteVerticesResponse{}
			if kind == "fixed32" {
				payload = make([]byte, 4*n)
				message = &pb.AddEdgesResponse{}
			}
			raw := protowire.AppendBytes([]byte{0x12}, payload)
			err := (currentOutputCodec{name: "proto", read: read, send: read}).Unmarshal(raw, message)
			if (err != nil) != (delta > 0) {
				t.Fatal("packed boundary", kind, delta, err)
			}
		}
	}
}

func TestCurrentPublicDecodeNestedUnknownAndUnsupported(t *testing.T) {
	c := currentOutputCodec{name: "proto", read: 128 << 10, send: 128 << 10}
	// A normal nested/oneof request remains supported in both encodings.
	request := &pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Name: "ordinary"}}}}}}
	for _, name := range []string{"proto", "json"} {
		codec := c
		codec.name = name
		raw, err := codec.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded pb.PrepareSecurityChangesRequest
		if err = codec.Unmarshal(raw, &decoded); err != nil || !proto.Equal(request, &decoded) {
			t.Fatal("nested request", name, err)
		}
	}
	// Opaque unknown bytes do not masquerade as nested messages and remain
	// forward compatible. Their retained/growing storage uses the byte credit.
	raw := protowire.AppendBytes(protowire.AppendTag(nil, 1000, protowire.BytesType), bytes.Repeat([]byte{0xff}, 4096))
	var unknown pb.GetVerticesRequest
	if err := c.Unmarshal(raw, &unknown); err != nil || !bytes.Equal(raw, unknown.ProtoReflect().GetUnknown()) {
		t.Fatal("unknown bytes", err)
	}
	// All unknown tags are still charged, including zero-payload fields.
	var dense []byte
	// Complete each tag with its scalar; do not merely test malformed framing.
	for range currentDecodeNodes(c.read) {
		dense = protowire.AppendVarint(protowire.AppendTag(dense, 1000, protowire.VarintType), 0)
	}
	if err := c.Unmarshal(dense, &unknown); connect.CodeOf(err) != connect.CodeResourceExhausted || !bytes.Equal(raw, unknown.ProtoReflect().GetUnknown()) {
		t.Fatal("unknown field budget/reset", err)
	}
	// Public schemas have no maps. Private peer maps and dynamic/custom WKT
	// implementations require a separate allocation proof and must not fall
	// through to the public decoder, even for a tiny wire representation.
	for _, m := range []proto.Message{&pb.SubscribeRequest{FromSeqPerOrigin: map[string]uint64{"unchanged": 1}}, dynamicpb.NewMessage(request.ProtoReflect().Descriptor()), &anypb.Any{TypeUrl: "unchanged"}} {
		before := proto.Clone(m)
		for _, name := range []string{"proto", "json"} {
			codec := c
			codec.name = name
			input := []byte{}
			if name == "json" {
				input = []byte(`{"fromSeqPerOrigin":{"a":"1"}}`)
			}
			if err := codec.Unmarshal(input, m); err == nil || !proto.Equal(m, before) {
				t.Fatal("uncertified schema allocated/reset", name, err)
			}
		}
	}
	// Existing JSON unknown-field semantics remain rejecting.
	if err := (currentOutputCodec{name: "json", read: 4096}).Unmarshal([]byte(`{"unknown":[{},{},{}]}`), &pb.GetVerticesRequest{}); err == nil {
		t.Fatal("discarded unknown JSON")
	}
	for _, levels := range []int{98, 99, 100} {
		raw = nil
		for range levels {
			raw = protowire.AppendTag(raw, 1000, protowire.StartGroupType)
		}
		for range levels {
			raw = protowire.AppendTag(raw, 1000, protowire.EndGroupType)
		}
		err := c.Unmarshal(raw, &pb.GetVerticesRequest{})
		if (err != nil) != (levels == 100) {
			t.Fatal("unknown group depth", levels, err)
		}
	}
	for _, levels := range []int{99, 100, 101} {
		b := currentDecodeBudget{remaining: 1000}
		err := b.json([]byte(strings.Repeat("[", levels) + strings.Repeat("]", levels)))
		if (err != nil) != (levels == 101) {
			t.Fatal("JSON container depth", levels, err)
		}
	}
}

func TestCurrentPublicDecodeByteBoundaryAndLargePayload(t *testing.T) {
	// Escapes/base64 or long byte payloads do not consume one structural unit
	// per payload byte, and still use the original pre-compression byte bound.
	message := &pb.Vertex{Key: "orders:one", Value: &pb.Vertex_Bytes{Bytes: bytes.Repeat([]byte{0xff}, 4096)}}
	for _, name := range []string{"proto", "json", "json; charset=utf-8"} {
		raw, err := (currentOutputCodec{name: name, send: 128 << 10}).Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, delta := range []int{-1, 0, 1} {
			target := &pb.Vertex{Key: "unchanged"}
			err = (currentOutputCodec{name: name, read: len(raw) + delta}).Unmarshal(raw, target)
			if delta < 0 {
				if err == nil || target.Key != "unchanged" {
					t.Fatal("byte overflow changed target", err)
				}
			} else if err != nil || !proto.Equal(message, target) {
				t.Fatal("payload boundary", name, delta, err)
			}
		}
	}
}
