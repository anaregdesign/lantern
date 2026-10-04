package client

import (
	"bytes"
	"errors"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestPublicChangeFrame(t *testing.T) {
	cursor, err := NewScopedChangeCursor([]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	copy := cursor.Bytes()
	copy[0] = 9
	if !bytes.Equal(cursor.Bytes(), []byte{1, 2, 3}) {
		t.Fatal("cursor storage alias")
	}
	raw := &pb.WatchChangesResponse{Cursor: cursor.Bytes(), Invalidations: []*pb.ChangeInvalidation{{Identity: &pb.ChangeInvalidation_VertexKey{VertexKey: "orders:1"}}}}
	frame, err := decodeChangeFrame(raw, ChangeIdentity)
	if err != nil || frame.Invalidations[0].Key != "orders:1" {
		t.Fatal(frame, err)
	}
	raw.Cursor[0] = 9
	if frame.Cursor.Bytes()[0] != 1 {
		t.Fatal("wire cursor alias")
	}
	item := raw.Invalidations[0]
	item.CurrentImage = &pb.ChangeInvalidation_Vertex{Vertex: &pb.Vertex{Key: "orders:1"}}
	if _, err := decodeChangeFrame(raw, ChangeIdentity); !errors.Is(err, ErrChangeGap) {
		t.Fatal("identity disclosed image", err)
	}
	if _, err := decodeChangeFrame(raw, ChangeValue); err != nil {
		t.Fatal(err)
	}
	item.GetVertex().Key = "orders:2"
	if _, err := decodeChangeFrame(raw, ChangeValue); !errors.Is(err, ErrChangeGap) {
		t.Fatal("mismatched image", err)
	}
	item.GetVertex().Key = "orders:1"
	item.GetVertex().ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
	if _, err := decodeChangeFrame(raw, ChangeValue); !errors.Is(err, ErrChangeGap) {
		t.Fatal("future image silently accepted", err)
	}
	for _, invalid := range []*pb.WatchChangesResponse{nil, {}, {Bootstrap: true}, {Bootstrap: true, Cursor: []byte{1}, Invalidations: []*pb.ChangeInvalidation{{}}}, {Cursor: []byte{1}, Invalidations: []*pb.ChangeInvalidation{nil}}} {
		if _, err := decodeChangeFrame(invalid, ChangeIdentity); !errors.Is(err, ErrChangeGap) {
			t.Fatal(invalid, err)
		}
	}
}
