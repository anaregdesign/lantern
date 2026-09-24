package mutationreceipt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestIDWireContract(t *testing.T) {
	epoch := Epoch{1, 2, 3}
	random := [24]byte{4, 5, 6}
	issued := time.Date(2026, 9, 24, 12, 34, 56, 789000000, time.UTC)
	id, err := NewID(epoch, issued, random)
	if err != nil {
		t.Fatal(err)
	}
	got := binary.BigEndian.Uint64(id[17:25])
	if id[0] != 1 || !bytes.Equal(id[1:17], epoch[:]) || got != uint64(issued.UnixMilli()) {
		t.Fatalf("wire ID does not contain version, epoch, and network-order UTC milliseconds: %x", id)
	}
	raw := id.Bytes()
	decoded, err := DecodeID(raw)
	if err != nil || decoded != id {
		t.Fatalf("DecodeID = %x, %v", decoded, err)
	}
	raw[0] = 255
	if id[0] != 1 {
		t.Fatal("Bytes exposed the ID's backing storage")
	}

	tests := []struct {
		name string
		edit func([]byte) []byte
	}{
		{"short", func(b []byte) []byte { return b[:len(b)-1] }},
		{"unknown version", func(b []byte) []byte { b[0] = 2; return b }},
		{"zero epoch", func(b []byte) []byte { clear(b[1:17]); return b }},
		{"zero randomness", func(b []byte) []byte { clear(b[25:]); return b }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeID(tt.edit(id.Bytes()))
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("DecodeID error = %v, want ErrInvalidID", err)
			}
		})
	}
	if _, err := NewID(Epoch{}, issued, random); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("zero epoch error = %v", err)
	}
	if _, err := NewID(epoch, issued, [24]byte{}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("zero randomness error = %v", err)
	}
}
