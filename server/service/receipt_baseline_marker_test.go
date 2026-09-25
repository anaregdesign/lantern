package service

import (
	"bytes"
	"errors"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

func receiptBaselineMarkerFixture() receiptBaselineMarker {
	snapshotOrigin := hlc.NodeID{2}
	localOrigin := hlc.NodeID{3}
	return receiptBaselineMarker{
		Digest:             [32]byte{1},
		Size:               4096,
		Epoch:              mutationreceipt.Epoch{4},
		PolicyFingerprint:  [32]byte{5},
		PreviousGeneration: [16]byte{6},
		RotatedGeneration:  [16]byte{7},
		SourceLocalCutoff:  9,
		SnapshotHLC:        hlc.Timestamp{WallNs: 100, Logical: 2, NodeID: snapshotOrigin},
		RestoreFloor:       hlc.Timestamp{WallNs: 101, Logical: 3, NodeID: localOrigin},
	}
}

func TestReceiptBaselineMarkerCanonicalRoundTripAndFrameBinding(t *testing.T) {
	want := receiptBaselineMarkerFixture()
	raw, err := marshalReceiptBaselineMarker(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != receiptBaselineMarkerSize {
		t.Fatalf("marker length = %d, want %d", len(raw), receiptBaselineMarkerSize)
	}
	got, err := unmarshalReceiptBaselineMarker(raw)
	if err != nil || got != want {
		t.Fatalf("marker roundtrip = %+v, %v", got, err)
	}
	again, err := marshalReceiptBaselineMarker(got)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("marker is not canonical: %v", err)
	}
	union, err := encodeReceiptWALUnion(want)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptWALUnion(union)
	if err != nil || decoded.(receiptBaselineMarker) != want {
		t.Fatalf("union roundtrip = %+v, %v", decoded, err)
	}
	if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 8, HLC: want.RestoreFloor, Op: got}); err != nil {
		t.Fatal(err)
	}
	badFrame := mutationlog.Entry{Seq: 8, HLC: want.SnapshotHLC, Op: got}
	if err := validateReceiptWALUnionEntry(badFrame); !errors.Is(err, errReceiptBaselineMarker) {
		t.Fatalf("mismatched frame HLC = %v", err)
	}
}

func TestReceiptBaselineMarkerRejectsInvalidProvenance(t *testing.T) {
	valid := receiptBaselineMarkerFixture()
	tests := map[string]func(*receiptBaselineMarker){
		"zero digest":        func(m *receiptBaselineMarker) { m.Digest = [32]byte{} },
		"oversized":          func(m *receiptBaselineMarker) { m.Size = maxReceiptBaselineBytes + 1 },
		"zero epoch":         func(m *receiptBaselineMarker) { m.Epoch = mutationreceipt.Epoch{} },
		"same generation":    func(m *receiptBaselineMarker) { m.RotatedGeneration = m.PreviousGeneration },
		"backward HLC":       func(m *receiptBaselineMarker) { m.RestoreFloor.WallNs = m.SnapshotHLC.WallNs - 1 },
		"zero snapshot node": func(m *receiptBaselineMarker) { m.SnapshotHLC.NodeID = hlc.NodeID{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			marker := valid
			mutate(&marker)
			if raw, err := marshalReceiptBaselineMarker(marker); raw != nil || !errors.Is(err, errReceiptBaselineMarker) {
				t.Fatalf("invalid marker encoded: %x, %v", raw, err)
			}
		})
	}
}
