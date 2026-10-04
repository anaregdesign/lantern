package service

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestChangeCursorFixedEncryptedBindingAndRotation(t *testing.T) {
	key1, key2 := ChangeCursorKey{Version: 1, Key: [32]byte{1}}, ChangeCursorKey{Version: 2, Key: [32]byte{2}}
	codec, err := newChangeCursorCodec([]ChangeCursorKey{key1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	binding := [32]byte{8}
	request := changeRequestBinding(&pb.WatchChangesRequest{Prefix: "orders:", Projection: pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY})
	next := map[string]uint64{hex.EncodeToString(bytes.Repeat([]byte{7}, 16)): 371}
	cursor, err := codec.seal(binding, request, next, now)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := codec.seal(binding, request, nil, now)
	if err != nil || len(empty) != len(cursor) || bytes.Contains(cursor, bytes.Repeat([]byte{7}, 16)) {
		t.Fatal("origin count or identity leaked through cursor", err)
	}
	decoded, err := codec.open(cursor, binding, request, now)
	if err != nil || !reflect.DeepEqual(decoded, next) {
		t.Fatal(decoded, err)
	}
	for _, skew := range []time.Duration{-time.Second, -changeCursorClockMargin} {
		if decoded, err := codec.open(cursor, binding, request, now.Add(skew)); err != nil || !reflect.DeepEqual(decoded, next) {
			t.Fatal("qualified slow replica rejected the original cursor", skew, err)
		}
	}
	tampered := append([]byte(nil), cursor...)
	tampered[len(tampered)-1] ^= 1
	for name, check := range map[string]func() error{
		"tamper":           func() error { _, err := codec.open(tampered, binding, request, now); return err },
		"principal/policy": func() error { _, err := codec.open(cursor, [32]byte{9}, request, now); return err },
		"scope":            func() error { _, err := codec.open(cursor, binding, [32]byte{9}, now); return err },
		"expiry": func() error {
			_, err := codec.open(cursor, binding, request, now.Add(changeCursorLifetime))
			return err
		},
		"future clock": func() error {
			_, err := codec.open(cursor, binding, request, now.Add(-changeCursorClockMargin-time.Second))
			return err
		},
		"length": func() error { _, err := codec.open(cursor[:len(cursor)-1], binding, request, now); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if check() == nil {
				t.Fatal("unbound cursor accepted")
			}
		})
	}
	rotated, err := newChangeCursorCodec([]ChangeCursorKey{key1, key2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.open(cursor, binding, request, now); err != nil {
		t.Fatal("retained old key could not resume", err)
	}
	retired, err := newChangeCursorCodec([]ChangeCursorKey{key2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retired.open(cursor, binding, request, now); err == nil {
		t.Fatal("retired key retained authority")
	}
	oversized := make(map[string]uint64)
	for i := range maxChangeOrigins + 1 {
		origin := [16]byte{byte(i + 1), 1}
		oversized[hex.EncodeToString(origin[:])] = 1
	}
	if _, err := codec.seal(binding, request, oversized, now); err == nil {
		t.Fatal("unbounded origin cursor admitted")
	}
	for _, keys := range [][]ChangeCursorKey{nil, {{Version: 1}}, {key1, key1}, {key2}} {
		if _, err := newChangeCursorCodec(keys, 1); err == nil {
			t.Fatal("invalid cursor key ring", keys)
		}
	}
}

func BenchmarkChangeCursorSeal(b *testing.B) {
	codec, err := newChangeCursorCodec([]ChangeCursorKey{{Version: 1, Key: [32]byte{1}}}, 1)
	if err != nil {
		b.Fatal(err)
	}
	next := map[string]uint64{hex.EncodeToString(bytes.Repeat([]byte{7}, 16)): 371}
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := codec.seal([32]byte{1}, [32]byte{2}, next, now); err != nil {
			b.Fatal(err)
		}
	}
}
