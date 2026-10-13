package service

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestNativeProtoRejectsAmbiguousCredentials(t *testing.T) {
	codec := nativeProtoCodec{}
	field := func(number protowire.Number, data []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), data)
	}
	id := field(1, []byte("attempt"))
	google := field(3, field(1, []byte("provider-token")))
	valid := append(append([]byte{}, google...), id...) // Field order is immaterial.
	for name, data := range map[string][]byte{
		"duplicate":        append(append([]byte{}, valid...), id...),
		"oneof":            append(append([]byte{}, valid...), field(4, field(1, []byte("apple-token")))...),
		"nested duplicate": field(3, append(field(1, []byte("one")), field(1, []byte("two"))...)),
		"unknown":          append(append([]byte{}, valid...), field(100, []byte("unknown"))...),
		"nested unknown":   field(3, field(100, []byte("unknown"))),
		"truncated":        {0x1a, 0xff},
	} {
		t.Run(name, func(t *testing.T) {
			if codec.Unmarshal(data, new(pb.CompleteNativeLoginRequest)) == nil {
				t.Fatal("ambiguous native credential reached typed decoding")
			}
		})
	}
	var decoded pb.CompleteNativeLoginRequest
	if err := codec.Unmarshal(valid, &decoded); err != nil || decoded.GetGoogle().GetIdToken() != "provider-token" {
		t.Fatal("valid unordered protobuf rejected", err)
	}
	encoded, err := codec.Marshal(&decoded)
	var roundTrip pb.CompleteNativeLoginRequest
	if err != nil || codec.Unmarshal(encoded, &roundTrip) != nil || !proto.Equal(&decoded, &roundTrip) {
		t.Fatal("native codec round trip")
	}
}
