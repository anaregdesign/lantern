package rpcwire

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestStrictJSONCodec(t *testing.T) {
	codec := strictJSONCodec{}
	for _, data := range []string{`{"unknown":true}`, `{"expectedRevision":"1","expectedRevision":"2"}`} {
		if err := codec.Unmarshal([]byte(data), &pb.ApplySecurityChangesRequest{}); err == nil {
			t.Fatal("ambiguous JSON accepted", data)
		}
	}
	want := &pb.ApplySecurityChangesRequest{ExpectedRevision: 1}
	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got pb.ApplySecurityChangesRequest
	if err := codec.Unmarshal(data, &got); err != nil || !proto.Equal(&got, want) {
		t.Fatal("JSON round trip changed", err)
	}
	if _, err := codec.Marshal(struct{}{}); err == nil {
		t.Fatal("non-protobuf marshal accepted")
	}
	if err := codec.Unmarshal([]byte(`{}`), &struct{}{}); err == nil {
		t.Fatal("non-protobuf unmarshal accepted")
	}
}
