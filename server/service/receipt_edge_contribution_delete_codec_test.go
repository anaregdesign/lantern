package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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

func receiptEdgeContributionDeleteCodecFixture(
	t *testing.T,
) (mutationlog.Entry, *edgeContributionDeleteReceiptEnvelope) {
	t.Helper()
	f := newReceiptContributionDeleteFixture(t, nil, hlc.NodeID{0x41})
	present := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "present", ContribID: graphcache.ContribID{0x80},
	}
	absent := graphcache.EdgeContributionKey[string]{
		Tail: "tail", Head: "absent", ContribID: graphcache.ContribID{0x81},
	}
	if !f.cache.AddEdgeWithExpirationContrib(
		present.Tail, present.Head, 2, time.Now().Add(time.Hour), present.ContribID,
	) {
		t.Fatal("initial Add rejected")
	}
	call := receiptContributionDeleteCall(t, f.epoch, present, absent, present)
	if _, err := f.contribution.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	entry := f.log.RetainedEntries()[0]
	e, ok := entry.Op.(*edgeContributionDeleteReceiptEnvelope)
	if !ok {
		t.Fatalf("committed operation = %T", entry.Op)
	}
	return entry, e
}

func cloneReceiptEdgeContributionDeleteCodecEnvelope(
	e *edgeContributionDeleteReceiptEnvelope,
) *edgeContributionDeleteReceiptEnvelope {
	copyOf := *e
	copyOf.Mutation = proto.Clone(e.Mutation).(*pb.Mutation)
	copyOf.OriginalKeys = append([]graphcache.EdgeContributionKey[string](nil), e.OriginalKeys...)
	copyOf.Accepted = append([]graphcache.IndexedEdgeContributionDelete[string](nil), e.Accepted...)
	copyOf.Receipts = cloneMutationReceipts(e.Receipts)
	return &copyOf
}

func TestReceiptEdgeContributionDeleteWALCodecRoundTrip(t *testing.T) {
	entry, want := receiptEdgeContributionDeleteCodecFixture(t)
	raw, err := encodeReceiptEdgeContributionDeleteWAL(entry.Op)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encodeReceiptEdgeContributionDeleteWAL(entry.Op)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("encoding not deterministic: %v", err)
	}
	decoded, err := decodeReceiptEdgeContributionDeleteWAL(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.(*edgeContributionDeleteReceiptEnvelope)
	if !ok || got.Origin != want.Origin || got.OriginSeq != want.OriginSeq ||
		got.HLC != want.HLC || got.Epoch != want.Epoch ||
		got.PolicyFingerprint != want.PolicyFingerprint ||
		!got.TombstoneExpiration.Equal(want.TombstoneExpiration) ||
		!reflect.DeepEqual(got.OriginalKeys, want.OriginalKeys) ||
		!reflect.DeepEqual(got.Accepted, want.Accepted) ||
		!reflect.DeepEqual(got.Receipts, want.Receipts) ||
		!proto.Equal(got.Mutation, want.Mutation) {
		t.Fatalf("decoded envelope differs: %T %+v, want %+v", decoded, got, want)
	}
	if got.TombstoneExpiration.Location() != time.UTC {
		t.Fatalf("decoded tombstone timezone = %v", got.TombstoneExpiration.Location())
	}
	if err := validateReceiptEdgeContributionDeleteWALEntry(mutationlog.Entry{
		Seq: entry.Seq + 20, HLC: entry.HLC, Op: got,
	}); err != nil {
		t.Fatalf("relay-local sequence was incorrectly bound to origin: %v", err)
	}
	if err := validateReceiptEdgeContributionDeleteWALEntry(mutationlog.Entry{
		Seq: entry.Seq, HLC: hlc.Timestamp{WallNs: entry.HLC.WallNs + 1, NodeID: entry.HLC.NodeID},
		Op: got,
	}); !errors.Is(err, errReceiptEdgeContributionDeleteWAL) {
		t.Fatalf("frame/envelope HLC drift = %v", err)
	}
	if got.Receipts[1].Result[0] != 0 || got.OriginalKeys[0].ContribID.IsZero() {
		t.Fatalf("false result or full identity was lost: %+v", got)
	}
}

func TestReceiptEdgeContributionDeleteWALCodecRejectsEnvelopeDrift(t *testing.T) {
	_, original := receiptEdgeContributionDeleteCodecFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*edgeContributionDeleteReceiptEnvelope)
	}{
		{"zero target", func(e *edgeContributionDeleteReceiptEnvelope) { e.OriginalKeys[0].ContribID = graphcache.ContribID{} }},
		{"invalid UTF-8", func(e *edgeContributionDeleteReceiptEnvelope) { e.OriginalKeys[0].Tail = "\xff" }},
		{"operation ID", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[0].ID[0] ^= 1 }},
		{"duplicate operation ID", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[1].ID = e.Receipts[0].ID }},
		{"wrong family", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[0].Kind = mutationreceipt.DeleteEdge }},
		{"Add reverse binding", func(e *edgeContributionDeleteReceiptEnvelope) {
			e.Receipts[0].HasContrib = true
			e.Receipts[0].ContribID = mutationreceipt.ContribID{0x80}
		}},
		{"digest", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[0].Digest[0] ^= 1 }},
		{"false absent", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[1].Result = nil }},
		{"result invalid", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[1].Result[0] = 2 }},
		{"deadline", func(e *edgeContributionDeleteReceiptEnvelope) { e.Receipts[1].DeadlineMillis++ }},
		{"tombstone expiration", func(e *edgeContributionDeleteReceiptEnvelope) { e.TombstoneExpiration = time.Time{} }},
		{"accepted target drift", func(e *edgeContributionDeleteReceiptEnvelope) {
			e.Accepted[0].Key.ContribID[0] ^= 1
		}},
		{"accepted index order", func(e *edgeContributionDeleteReceiptEnvelope) {
			e.Accepted[1].Index = e.Accepted[0].Index
		}},
		{"graph projection", func(e *edgeContributionDeleteReceiptEnvelope) {
			e.Mutation.GetOp().GetDeleteEdgeContributions().Contributions[0].ContribId[0] ^= 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := cloneReceiptEdgeContributionDeleteCodecEnvelope(original)
			tc.mutate(e)
			if _, err := encodeReceiptEdgeContributionDeleteWAL(e); !errors.Is(err, errReceiptEdgeContributionDeleteWAL) {
				t.Fatalf("invalid envelope encoded: %v", err)
			}
		})
	}
	if _, err := encodeReceiptEdgeContributionDeleteWAL(&pb.Mutation{}); !errors.Is(err, errReceiptEdgeContributionDeleteWAL) {
		t.Fatalf("wrong WAL family = %v", err)
	}
	oversized := cloneReceiptEdgeContributionDeleteCodecEnvelope(original)
	oversized.OriginalKeys[0].Tail = strings.Repeat("x", receiptEdgeContributionDeleteWALMaxBytes)
	if _, err := encodeReceiptEdgeContributionDeleteWAL(oversized); !errors.Is(err, errReceiptEdgeContributionDeleteWALCapacity) {
		t.Fatalf("oversized WAL projection = %v", err)
	}
}

func TestReceiptEdgeContributionDeleteWALCodecRejectsMalformedBytes(t *testing.T) {
	_, original := receiptEdgeContributionDeleteCodecFixture(t)
	valid, err := encodeReceiptEdgeContributionDeleteWAL(original)
	if err != nil {
		t.Fatal(err)
	}
	off := receiptEdgeContributionDeleteWALHeaderSize
	firstTailLen := int(binary.BigEndian.Uint32(valid[off+115 : off+119]))
	firstHeadLenOffset := off + 119 + firstTailLen
	firstHeadLen := int(binary.BigEndian.Uint32(valid[firstHeadLenOffset : firstHeadLenOffset+4]))
	firstContribIDOffset := firstHeadLenOffset + 4 + firstHeadLen
	acceptedOffset := off
	for range original.Receipts {
		tailLen := int(binary.BigEndian.Uint32(valid[acceptedOffset+115 : acceptedOffset+119]))
		headLenOffset := acceptedOffset + 119 + tailLen
		headLen := int(binary.BigEndian.Uint32(valid[headLenOffset : headLenOffset+4]))
		acceptedOffset += receiptEdgeContributionDeleteWALItemSize + tailLen + headLen
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:receiptEdgeContributionDeleteWALHeaderSize-1] }},
		{"truncated item", func(b []byte) []byte { return b[:len(b)-1] }},
		{"trailing bytes", func(b []byte) []byte { return append(b, 0) }},
		{"oversized bytes", func([]byte) []byte { return make([]byte, receiptEdgeContributionDeleteWALMaxBytes+1) }},
		{"unknown version", func(b []byte) []byte { b[4]++; return b }},
		{"reserved header", func(b []byte) []byte { b[5] = 1; return b }},
		{"origin sequence", func(b []byte) []byte { binary.BigEndian.PutUint64(b[24:32], 0); return b }},
		{"policy fingerprint", func(b []byte) []byte { clear(b[76:108]); return b }},
		{"retention", func(b []byte) []byte { b[123]++; return b }},
		{"item count", func(b []byte) []byte { binary.BigEndian.PutUint32(b[124:128], ^uint32(0)); return b }},
		{"accepted count", func(b []byte) []byte { binary.BigEndian.PutUint32(b[128:132], 4); return b }},
		{"operation ID version", func(b []byte) []byte { b[off]++; return b }},
		{"family", func(b []byte) []byte { b[off+73] = byte(mutationreceipt.DeleteEdge); return b }},
		{"digest", func(b []byte) []byte { b[off+74] ^= 1; return b }},
		{"false result", func(b []byte) []byte { b[off+114] = 2; return b }},
		{"invalid UTF-8", func(b []byte) []byte { b[off+119] = 0xff; return b }},
		{"ContribID missing", func(b []byte) []byte {
			clear(b[firstContribIDOffset : firstContribIDOffset+24])
			return b
		}},
		{"accepted index", func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[acceptedOffset:acceptedOffset+4], 99)
			return b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := tc.mutate(append([]byte(nil), valid...))
			if _, err := decodeReceiptEdgeContributionDeleteWAL(bad); !errors.Is(err, errReceiptEdgeContributionDeleteWAL) {
				t.Fatalf("malformed WAL decoded: %v", err)
			}
		})
	}
}
