package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

const (
	receiptArchiveWALCutMagic   = "LRWLCUT1"
	receiptArchiveWALCutVersion = uint16(1)
	receiptArchiveWALCutSize    = 8 + 2 + 2 + sha256.Size + 8 + 8 + sha256.Size + sha256.Size
)

var errReceiptArchiveWALCut = errors.New("backup: invalid receipt archive/FileWAL cut")

type receiptArchiveWALCut struct {
	archiveSHA256 [sha256.Size]byte
	localSeq      uint64
	walOffset     int64
	walSHA256     [sha256.Size]byte
}

// bindReceiptArchiveFileWAL creates a private byte-pairing manifest after
// validating the complete archive and FileWAL. The caller must provide the
// FileWAL belonging to the archive source and exclusively own its closed or
// otherwise non-mutating path. This function cannot itself prove that source
// provenance or certify a durable Store high-water / complete replay tail.
func bindReceiptArchiveFileWAL(
	archiveRaw []byte, walPath string,
	decode func([]byte) (mutationlog.MutationOp, error), validate func(mutationlog.Entry) error,
) ([]byte, error) {
	if len(archiveRaw) > wholeStateArchiveMaxBytes {
		return nil, wholeStateArchiveError("archive exceeds byte limit")
	}
	owned := bytes.Clone(archiveRaw)
	archive, err := decodeWholeStateArchive(bytes.NewReader(owned))
	if err != nil {
		return nil, err
	}
	seq := archive.Graph[0].GetHeader().GetCutoffLocalSeq()
	cut, err := mutationlog.InspectFileWALCut(walPath, seq, decode, validate)
	if err != nil {
		return nil, fmt.Errorf("backup: inspect FileWAL at archive cut: %w", err)
	}
	return encodeReceiptArchiveWALCut(receiptArchiveWALCut{
		archiveSHA256: sha256.Sum256(owned),
		localSeq:      seq,
		walOffset:     cut.Offset,
		walSHA256:     cut.SHA256,
	}), nil
}

// stageReceiptWholeStateArchiveAtWALCut returns an entirely detached stage
// only when the manifest still binds these exact archive and FileWAL bytes.
// A valid suffix after the recorded cut is allowed, but any corruption in
// that suffix is rejected by the FileWAL inspector. This is neither a serving
// installer nor a same-epoch recovery certificate.
func stageReceiptWholeStateArchiveAtWALCut(
	ctx context.Context,
	archiveRaw, manifestRaw []byte,
	walPath string,
	decode func([]byte) (mutationlog.MutationOp, error),
	validate func(mutationlog.Entry) error,
	expected mutationreceipt.Config,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*receiptWholeStateStage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(archiveRaw) > wholeStateArchiveMaxBytes {
		return nil, wholeStateArchiveError("archive exceeds byte limit")
	}
	owned := bytes.Clone(archiveRaw)
	manifest, err := decodeReceiptArchiveWALCut(manifestRaw)
	if err != nil {
		return nil, err
	}
	if sha256.Sum256(owned) != manifest.archiveSHA256 {
		return nil, fmt.Errorf("%w: archive bytes differ from manifest", errReceiptArchiveWALCut)
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(owned))
	if err != nil {
		return nil, err
	}
	if archive.Graph[0].GetHeader().GetCutoffLocalSeq() != manifest.localSeq {
		return nil, fmt.Errorf("%w: archive local sequence differs from manifest", errReceiptArchiveWALCut)
	}
	cut, err := mutationlog.InspectFileWALCut(walPath, manifest.localSeq, decode, validate)
	if err != nil {
		return nil, fmt.Errorf("backup: inspect FileWAL at manifest cut: %w", err)
	}
	if cut.Offset != manifest.walOffset || cut.SHA256 != manifest.walSHA256 {
		return nil, fmt.Errorf("%w: FileWAL prefix differs from manifest", errReceiptArchiveWALCut)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stage, err := stageReceiptWholeStateArchive(ctx, bytes.NewReader(owned), expected, defaultTTL, configureGraph)
	if err != nil {
		return nil, err
	}
	return stage, nil
}

func encodeReceiptArchiveWALCut(cut receiptArchiveWALCut) []byte {
	raw := make([]byte, receiptArchiveWALCutSize)
	copy(raw[:8], receiptArchiveWALCutMagic)
	binary.BigEndian.PutUint16(raw[8:10], receiptArchiveWALCutVersion)
	copy(raw[12:44], cut.archiveSHA256[:])
	binary.BigEndian.PutUint64(raw[44:52], cut.localSeq)
	binary.BigEndian.PutUint64(raw[52:60], uint64(cut.walOffset))
	copy(raw[60:92], cut.walSHA256[:])
	sum := sha256.Sum256(raw[:receiptArchiveWALCutSize-sha256.Size])
	copy(raw[receiptArchiveWALCutSize-sha256.Size:], sum[:])
	return raw
}

func decodeReceiptArchiveWALCut(raw []byte) (receiptArchiveWALCut, error) {
	if len(raw) != receiptArchiveWALCutSize || string(raw[:8]) != receiptArchiveWALCutMagic ||
		binary.BigEndian.Uint16(raw[8:10]) != receiptArchiveWALCutVersion || raw[10] != 0 || raw[11] != 0 {
		return receiptArchiveWALCut{}, errReceiptArchiveWALCut
	}
	sum := sha256.Sum256(raw[:receiptArchiveWALCutSize-sha256.Size])
	if !bytes.Equal(raw[receiptArchiveWALCutSize-sha256.Size:], sum[:]) {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: manifest checksum differs", errReceiptArchiveWALCut)
	}
	var cut receiptArchiveWALCut
	copy(cut.archiveSHA256[:], raw[12:44])
	cut.localSeq = binary.BigEndian.Uint64(raw[44:52])
	offset := binary.BigEndian.Uint64(raw[52:60])
	if offset > math.MaxInt64 || offset < 8 || (cut.localSeq == 0) != (offset == 8) {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: invalid FileWAL prefix offset", errReceiptArchiveWALCut)
	}
	cut.walOffset = int64(offset)
	copy(cut.walSHA256[:], raw[60:92])
	return cut, nil
}
