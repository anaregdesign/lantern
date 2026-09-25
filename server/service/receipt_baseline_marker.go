package service

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

const (
	receiptBaselineMarkerVersion = 1
	receiptBaselineMarkerSize    = 4 + sha256.Size + 8 +
		len(mutationreceipt.Epoch{}) + sha256.Size +
		2*16 + 8 + 2*(8+4+len(hlc.NodeID{}))
	maxReceiptBaselineBytes = 512 << 20
)

var errReceiptBaselineMarker = errors.New("service: invalid RECEIPT_V1 baseline marker")

// receiptBaselineMarker is a private WAL record that makes one immutable
// sidecar the serving baseline. The FileWAL frame sequence is the compaction
// boundary and its HLC must equal RestoreFloor. It is deliberately not a
// protobuf or public replication operation.
type receiptBaselineMarker struct {
	Digest             [sha256.Size]byte
	Size               uint64
	Epoch              mutationreceipt.Epoch
	PolicyFingerprint  [sha256.Size]byte
	PreviousGeneration [16]byte
	RotatedGeneration  [16]byte
	SourceLocalCutoff  uint64
	SnapshotHLC        hlc.Timestamp
	RestoreFloor       hlc.Timestamp
}

func validateReceiptBaselineMarker(marker receiptBaselineMarker) error {
	if marker.Digest == ([sha256.Size]byte{}) ||
		marker.Size == 0 || marker.Size > maxReceiptBaselineBytes ||
		marker.Epoch == (mutationreceipt.Epoch{}) ||
		marker.PolicyFingerprint == ([sha256.Size]byte{}) ||
		marker.PreviousGeneration == ([16]byte{}) ||
		marker.RotatedGeneration == ([16]byte{}) ||
		marker.PreviousGeneration == marker.RotatedGeneration ||
		marker.SnapshotHLC.WallNs <= 0 ||
		marker.SnapshotHLC.NodeID == (hlc.NodeID{}) ||
		marker.RestoreFloor.WallNs <= 0 ||
		marker.RestoreFloor.NodeID == (hlc.NodeID{}) ||
		marker.RestoreFloor.Less(marker.SnapshotHLC) {
		return errReceiptBaselineMarker
	}
	return nil
}

func marshalReceiptBaselineMarker(marker receiptBaselineMarker) ([]byte, error) {
	if err := validateReceiptBaselineMarker(marker); err != nil {
		return nil, err
	}
	raw := make([]byte, receiptBaselineMarkerSize)
	binary.BigEndian.PutUint32(raw[:4], receiptBaselineMarkerVersion)
	off := 4
	copy(raw[off:], marker.Digest[:])
	off += sha256.Size
	binary.BigEndian.PutUint64(raw[off:], marker.Size)
	off += 8
	copy(raw[off:], marker.Epoch[:])
	off += len(marker.Epoch)
	copy(raw[off:], marker.PolicyFingerprint[:])
	off += sha256.Size
	copy(raw[off:], marker.PreviousGeneration[:])
	off += len(marker.PreviousGeneration)
	copy(raw[off:], marker.RotatedGeneration[:])
	off += len(marker.RotatedGeneration)
	binary.BigEndian.PutUint64(raw[off:], marker.SourceLocalCutoff)
	off += 8
	off = putReceiptBaselineTimestamp(raw, off, marker.SnapshotHLC)
	_ = putReceiptBaselineTimestamp(raw, off, marker.RestoreFloor)
	return raw, nil
}

func unmarshalReceiptBaselineMarker(raw []byte) (receiptBaselineMarker, error) {
	if len(raw) != receiptBaselineMarkerSize ||
		binary.BigEndian.Uint32(raw[:4]) != receiptBaselineMarkerVersion {
		return receiptBaselineMarker{}, errReceiptBaselineMarker
	}
	var marker receiptBaselineMarker
	off := 4
	copy(marker.Digest[:], raw[off:off+sha256.Size])
	off += sha256.Size
	marker.Size = binary.BigEndian.Uint64(raw[off:])
	off += 8
	copy(marker.Epoch[:], raw[off:off+len(marker.Epoch)])
	off += len(marker.Epoch)
	copy(marker.PolicyFingerprint[:], raw[off:off+sha256.Size])
	off += sha256.Size
	copy(marker.PreviousGeneration[:], raw[off:off+len(marker.PreviousGeneration)])
	off += len(marker.PreviousGeneration)
	copy(marker.RotatedGeneration[:], raw[off:off+len(marker.RotatedGeneration)])
	off += len(marker.RotatedGeneration)
	marker.SourceLocalCutoff = binary.BigEndian.Uint64(raw[off:])
	off += 8
	marker.SnapshotHLC, off = readReceiptBaselineTimestamp(raw, off)
	marker.RestoreFloor, off = readReceiptBaselineTimestamp(raw, off)
	if off != len(raw) {
		return receiptBaselineMarker{}, errReceiptBaselineMarker
	}
	if err := validateReceiptBaselineMarker(marker); err != nil {
		return receiptBaselineMarker{}, err
	}
	return marker, nil
}

func putReceiptBaselineTimestamp(dst []byte, off int, ts hlc.Timestamp) int {
	binary.BigEndian.PutUint64(dst[off:], uint64(ts.WallNs))
	off += 8
	binary.BigEndian.PutUint32(dst[off:], ts.Logical)
	off += 4
	copy(dst[off:], ts.NodeID[:])
	return off + len(ts.NodeID)
}

func readReceiptBaselineTimestamp(raw []byte, off int) (hlc.Timestamp, int) {
	ts := hlc.Timestamp{
		WallNs:  int64(binary.BigEndian.Uint64(raw[off:])),
		Logical: binary.BigEndian.Uint32(raw[off+8:]),
	}
	off += 12
	copy(ts.NodeID[:], raw[off:off+len(ts.NodeID)])
	return ts, off + len(ts.NodeID)
}

func validateReceiptBaselineEntry(entrySequence uint64, frameHLC hlc.Timestamp, marker receiptBaselineMarker) error {
	if entrySequence == 0 || !frameHLC.Equal(marker.RestoreFloor) {
		return fmt.Errorf("%w: marker frame does not match its restore floor", errReceiptBaselineMarker)
	}
	return validateReceiptBaselineMarker(marker)
}
