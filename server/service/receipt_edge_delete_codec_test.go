package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func receiptEdgeDeleteCodecFixture(t *testing.T, wal mutationlog.WAL) (mutationlog.Entry, *edgeDeleteReceiptEnvelope) {
	t.Helper()
	f := newReceiptEdgeDeleteFixture(t, wal)
	f.cache.AddEdgeWithExpiration("tail", "present", 1, time.Now().Add(time.Hour))
	call := receiptDeleteCall(t, f.epoch,
		graphcache.EdgeKey[string]{Tail: "tail", Head: "present"},
		graphcache.EdgeKey[string]{Tail: "tail", Head: "absent"},
		graphcache.EdgeKey[string]{Tail: "tail", Head: "present"},
	)
	if _, err := f.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	entry := f.log.RetainedEntries()[0]
	envelope, ok := entry.Op.(*edgeDeleteReceiptEnvelope)
	if !ok {
		t.Fatalf("log operation = %T", entry.Op)
	}
	return entry, envelope
}

func cloneReceiptEdgeDeleteCodecEnvelope(e *edgeDeleteReceiptEnvelope) *edgeDeleteReceiptEnvelope {
	clone := *e
	clone.Mutation = proto.Clone(e.Mutation).(*pb.Mutation)
	clone.OriginalKeys = append([]graphcache.EdgeKey[string](nil), e.OriginalKeys...)
	clone.Accepted = append([]graphcache.IndexedEdgeDelete[string](nil), e.Accepted...)
	clone.Receipts = append([]mutationreceipt.Receipt(nil), e.Receipts...)
	for i := range clone.Receipts {
		clone.Receipts[i].Result = append([]byte(nil), clone.Receipts[i].Result...)
	}
	return &clone
}

func TestReceiptEdgeDeleteWALCodecRoundTrip(t *testing.T) {
	entry, want := receiptEdgeDeleteCodecFixture(t, nil)
	raw, err := encodeReceiptEdgeDeleteWAL(entry.Op)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > receiptEdgeDeleteWALMaxBytes || len(raw) < receiptEdgeDeleteWALHeaderSize {
		t.Fatalf("encoded size = %d", len(raw))
	}
	again, err := encodeReceiptEdgeDeleteWAL(entry.Op)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("encoding is not deterministic: %v", err)
	}
	decodedOp, err := decodeReceiptEdgeDeleteWAL(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := decodedOp.(*edgeDeleteReceiptEnvelope)
	if got.Origin != want.Origin || got.OriginSeq != want.OriginSeq || got.HLC != want.HLC ||
		got.Epoch != want.Epoch || got.PolicyFingerprint != want.PolicyFingerprint ||
		!got.TombstoneExpiration.Equal(want.TombstoneExpiration) ||
		!reflect.DeepEqual(got.OriginalKeys, want.OriginalKeys) ||
		!reflect.DeepEqual(got.Accepted, want.Accepted) ||
		!reflect.DeepEqual(got.Receipts, want.Receipts) || !proto.Equal(got.Mutation, want.Mutation) {
		t.Fatalf("decoded envelope differs from committed envelope: got %+v, want %+v", got, want)
	}
	if got.TombstoneExpiration.Location() != time.UTC {
		t.Fatalf("decoded tombstone location = %v, want UTC", got.TombstoneExpiration.Location())
	}
	if err := validateReceiptEdgeDeleteWALEntry(mutationlog.Entry{Seq: entry.Seq + 19, HLC: entry.HLC, Op: got}); err != nil {
		t.Fatalf("relay-local seq was wrongly bound to OriginSeq: %v", err)
	}
	entry.HLC.Logical++
	entry.Op = got
	if err := validateReceiptEdgeDeleteWALEntry(entry); !errors.Is(err, errReceiptEdgeDeleteWAL) {
		t.Fatalf("frame/envelope HLC drift = %v", err)
	}
	entry.HLC = got.HLC
	entry.Seq = 0
	if err := validateReceiptEdgeDeleteWALEntry(entry); !errors.Is(err, errReceiptEdgeDeleteWAL) {
		t.Fatalf("zero relay-local seq = %v", err)
	}
}

func TestReceiptEdgeDeleteWALCodecReceiptOnly(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "causally-rejected"}
	if _, err := f.cache.DeleteEdgesHLCChecked([]graphcache.EdgeKey[string]{key},
		hlc.Timestamp{WallNs: time.Now().Add(time.Hour).UnixNano()}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	call := receiptDeleteCall(t, f.epoch, key)
	if _, err := f.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	envelope := f.log.RetainedEntries()[0].Op.(*edgeDeleteReceiptEnvelope)
	if len(envelope.Accepted) != 0 || envelope.Receipts[0].Result[0] != 0 {
		t.Fatalf("expected receipt-only no-op envelope: %+v", envelope)
	}
	raw, err := encodeReceiptEdgeDeleteWAL(envelope)
	if err != nil {
		t.Fatal(err)
	}
	op, err := decodeReceiptEdgeDeleteWAL(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := op.(*edgeDeleteReceiptEnvelope)
	if len(got.Accepted) != 0 || len(got.Mutation.GetOp().GetDeleteEdges().GetEdges()) != 0 ||
		!reflect.DeepEqual(got.Receipts, envelope.Receipts) {
		t.Fatalf("receipt-only round-trip = %+v", got)
	}
}

func TestReceiptEdgeDeleteWALCodecFileFrameIntegrity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptEdgeDeleteWAL)
	if err != nil {
		t.Fatal(err)
	}
	entry, want := receiptEdgeDeleteCodecFixture(t, wal)
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	visits := 0
	err = mutationlog.ReplayFileWAL(path, decodeReceiptEdgeDeleteWAL, func(got mutationlog.Entry) error {
		visits++
		if err := validateReceiptEdgeDeleteWALEntry(got); err != nil {
			return err
		}
		decoded := got.Op.(*edgeDeleteReceiptEnvelope)
		if got.Seq != entry.Seq || decoded.OriginSeq != want.OriginSeq ||
			!reflect.DeepEqual(decoded.Receipts, want.Receipts) {
			return errors.New("replayed envelope differs from committed envelope")
		}
		return nil
	})
	if err != nil || visits != 1 {
		t.Fatalf("FileWAL replay = %v, visits=%d", err, visits)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	visits = 0
	err = mutationlog.ReplayFileWAL(path, decodeReceiptEdgeDeleteWAL, func(mutationlog.Entry) error {
		visits++
		return nil
	})
	if !errors.Is(err, mutationlog.ErrFileWALCorrupt) || visits != 0 {
		t.Fatalf("corrupt FileWAL frame = %v, visits=%d", err, visits)
	}
}

func TestReceiptEdgeDeleteWALCodecRejectsEnvelopeDrift(t *testing.T) {
	_, original := receiptEdgeDeleteCodecFixture(t, nil)
	tests := []struct {
		name   string
		mutate func(*edgeDeleteReceiptEnvelope)
	}{
		{"origin seq", func(e *edgeDeleteReceiptEnvelope) { e.OriginSeq++ }},
		{"mutation origin", func(e *edgeDeleteReceiptEnvelope) { e.Mutation.Origin[0] ^= 1 }},
		{"mutation HLC", func(e *edgeDeleteReceiptEnvelope) { e.Mutation.Hlc.Logical++ }},
		{"mutation projection", func(e *edgeDeleteReceiptEnvelope) { e.Mutation.GetOp().GetDeleteEdges().Edges[0].Head = "different" }},
		{"epoch", func(e *edgeDeleteReceiptEnvelope) { e.Epoch[0] ^= 1 }},
		{"policy fingerprint", func(e *edgeDeleteReceiptEnvelope) { e.PolicyFingerprint = [32]byte{} }},
		{"expiration", func(e *edgeDeleteReceiptEnvelope) { e.TombstoneExpiration = time.Time{} }},
		{"missing original key", func(e *edgeDeleteReceiptEnvelope) { e.OriginalKeys = e.OriginalKeys[:2] }},
		{"invalid key", func(e *edgeDeleteReceiptEnvelope) { e.OriginalKeys[0].Tail = "\xff" }},
		{"operation ID version", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].ID[0]++ }},
		{"duplicate operation ID", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[1].ID = e.Receipts[0].ID }},
		{"group drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[1].Group[0] ^= 1 }},
		{"index drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].Index++ }},
		{"count drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].Count++ }},
		{"kind drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].Kind = mutationreceipt.PutEdge }},
		{"intent drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].Digest[0] ^= 1 }},
		{"deadline drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].DeadlineMillis++ }},
		{"result drift", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[0].Result[0] = 2 }},
		{"accepted index drift", func(e *edgeDeleteReceiptEnvelope) { e.Accepted[1].Index = e.Accepted[0].Index }},
		{"accepted key drift", func(e *edgeDeleteReceiptEnvelope) { e.Accepted[0].Key.Head = "different" }},
		{"true without accepted", func(e *edgeDeleteReceiptEnvelope) {
			e.Accepted = nil
			e.Mutation = receiptEdgeDeleteWALMutation(e)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneReceiptEdgeDeleteCodecEnvelope(original)
			tt.mutate(candidate)
			if _, err := encodeReceiptEdgeDeleteWAL(candidate); !errors.Is(err, errReceiptEdgeDeleteWAL) {
				t.Fatalf("encode drift = %v", err)
			}
		})
	}
	if _, err := encodeReceiptEdgeDeleteWAL(&pb.Mutation{}); !errors.Is(err, errReceiptEdgeDeleteWAL) {
		t.Fatalf("unexpected operation type = %v", err)
	}
	tooLarge := cloneReceiptEdgeDeleteCodecEnvelope(original)
	tooLarge.OriginalKeys[0].Tail = strings.Repeat("x", receiptEdgeDeleteWALMaxBytes)
	if _, err := encodeReceiptEdgeDeleteWAL(tooLarge); !errors.Is(err, errReceiptEdgeDeleteWAL) {
		t.Fatalf("oversized encode = %v", err)
	}
}

func receiptEdgeDeleteCodecAcceptedOffset(raw []byte) int {
	off := receiptEdgeDeleteWALHeaderSize
	count := binary.BigEndian.Uint32(raw[124:128])
	for range count {
		tailLen := binary.BigEndian.Uint32(raw[off+115 : off+119])
		headLenOffset := off + 119 + int(tailLen)
		headLen := binary.BigEndian.Uint32(raw[headLenOffset : headLenOffset+4])
		off += receiptEdgeDeleteWALItemSize + int(tailLen) + int(headLen)
	}
	return off
}

func TestReceiptEdgeDeleteWALCodecRejectsMalformedBytes(t *testing.T) {
	_, envelope := receiptEdgeDeleteCodecFixture(t, nil)
	valid, err := encodeReceiptEdgeDeleteWAL(envelope)
	if err != nil {
		t.Fatal(err)
	}
	acceptedOffset := receiptEdgeDeleteCodecAcceptedOffset(valid)
	const first = receiptEdgeDeleteWALHeaderSize
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:receiptEdgeDeleteWALHeaderSize-1] }},
		{"truncated item", func(b []byte) []byte { return b[:len(b)-1] }},
		{"trailing bytes", func(b []byte) []byte { return append(b, 0) }},
		{"oversized payload", func([]byte) []byte { return make([]byte, receiptEdgeDeleteWALMaxBytes+1) }},
		{"unknown version", func(b []byte) []byte { b[4]++; return b }},
		{"nonzero reserved header", func(b []byte) []byte { b[5] = 1; return b }},
		{"origin seq zero", func(b []byte) []byte { binary.BigEndian.PutUint64(b[24:32], 0); return b }},
		{"HLC origin drift", func(b []byte) []byte { b[44] ^= 1; return b }},
		{"epoch drift", func(b []byte) []byte { b[60] ^= 1; return b }},
		{"zero policy fingerprint", func(b []byte) []byte { clear(b[76:108]); return b }},
		{"retention drift", func(b []byte) []byte { b[123]++; return b }},
		{"oversized item count", func(b []byte) []byte { binary.BigEndian.PutUint32(b[124:128], ^uint32(0)); return b }},
		{"accepted count drift", func(b []byte) []byte { binary.BigEndian.PutUint32(b[128:132], 4); return b }},
		{"operation ID version", func(b []byte) []byte { b[first]++; return b }},
		{"group zero", func(b []byte) []byte { clear(b[first+49 : first+65]); return b }},
		{"item index drift", func(b []byte) []byte { binary.BigEndian.PutUint32(b[first+65:first+69], 1); return b }},
		{"item count drift", func(b []byte) []byte { binary.BigEndian.PutUint32(b[first+69:first+73], 1); return b }},
		{"unknown kind", func(b []byte) []byte { b[first+73] = 255; return b }},
		{"intent digest drift", func(b []byte) []byte { b[first+74] ^= 1; return b }},
		{"deadline drift", func(b []byte) []byte { b[first+113]++; return b }},
		{"invalid result", func(b []byte) []byte { b[first+114] = 2; return b }},
		{"oversized tail length", func(b []byte) []byte { binary.BigEndian.PutUint32(b[first+115:first+119], ^uint32(0)); return b }},
		{"invalid UTF-8 tail", func(b []byte) []byte { b[first+119] = 0xff; return b }},
		{"accepted index drift", func(b []byte) []byte { binary.BigEndian.PutUint32(b[acceptedOffset:acceptedOffset+4], 2); return b }},
		{"accepted key drift", func(b []byte) []byte { b[acceptedOffset+8] ^= 1; return b }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := tt.mutate(append([]byte(nil), valid...))
			if _, err := decodeReceiptEdgeDeleteWAL(bad); !errors.Is(err, errReceiptEdgeDeleteWAL) {
				t.Fatalf("decode malformed payload = %v", err)
			}
		})
	}
}
