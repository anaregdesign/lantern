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
	receiptArchiveWALCutMagic       = "LRWLCUT2"
	receiptArchiveWALCutVersion     = uint16(2)
	receiptArchiveWALZeroOffset     = int64(8)
	receiptArchiveWALCutPayloadSize = 8 + 2 + 2 + sha256.Size +
		8 + 8 + sha256.Size + sha256.Size +
		8 + 8 + sha256.Size + sha256.Size
	receiptArchiveWALCutSize = receiptArchiveWALCutPayloadSize + sha256.Size
)

var errReceiptArchiveWALCut = errors.New("backup: invalid receipt archive/FileWAL cut")

type receiptArchiveWALCut struct {
	archiveSHA256  [sha256.Size]byte
	cutSeq         uint64
	cutOffset      int64
	cutSHA256      [sha256.Size]byte
	cutChainSHA256 [sha256.Size]byte
	tipSeq         uint64
	tipOffset      int64
	tipSHA256      [sha256.Size]byte
	tipChainSHA256 [sha256.Size]byte
}

// bindReceiptArchiveFileWAL creates a private byte-pairing manifest after
// validating the complete archive and FileWAL. It binds both the archive cut
// and the exact complete valid FileWAL tip observed by one stable inspection.
// The caller must provide the FileWAL belonging to the archive source and
// exclusively own its closed or otherwise non-mutating path. This function
// cannot itself prove source provenance or certify runtime tip-journal
// ownership, Store high-water, or same-epoch recovery.
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
		archiveSHA256:  sha256.Sum256(owned),
		cutSeq:         seq,
		cutOffset:      cut.Offset,
		cutSHA256:      cut.SHA256,
		cutChainSHA256: cut.ChainSHA256,
		tipSeq:         cut.ObservedLast,
		tipOffset:      cut.ObservedOffset,
		tipSHA256:      cut.ObservedSHA256,
		tipChainSHA256: cut.ObservedChainSHA256,
	}), nil
}

// stageReceiptWholeStateArchiveAtWALCut returns an entirely detached stage
// only when the manifest still binds these exact archive bytes, archive cut,
// and previously observed FileWAL tip. A valid suffix appended after that tip
// is allowed, but loss or replacement of the recorded tip and any invalid
// current suffix fail closed. This is neither a serving installer nor a
// same-epoch recovery certificate.
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
	if archive.Graph[0].GetHeader().GetCutoffLocalSeq() != manifest.cutSeq {
		return nil, fmt.Errorf("%w: archive local sequence differs from manifest", errReceiptArchiveWALCut)
	}
	cuts, err := mutationlog.InspectFileWALCuts(walPath, []uint64{manifest.cutSeq, manifest.tipSeq}, decode, validate)
	if err != nil {
		return nil, fmt.Errorf("backup: inspect FileWAL at manifest cut and tip: %w", err)
	}
	cut, tip := cuts[0], cuts[1]
	if cut.Offset != manifest.cutOffset || cut.SHA256 != manifest.cutSHA256 ||
		cut.ChainSHA256 != manifest.cutChainSHA256 {
		return nil, fmt.Errorf("%w: FileWAL prefix differs from manifest", errReceiptArchiveWALCut)
	}
	if tip.Offset != manifest.tipOffset || tip.SHA256 != manifest.tipSHA256 ||
		tip.ChainSHA256 != manifest.tipChainSHA256 {
		return nil, fmt.Errorf("%w: recorded FileWAL tip differs from manifest", errReceiptArchiveWALCut)
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
	binary.BigEndian.PutUint64(raw[44:52], cut.cutSeq)
	binary.BigEndian.PutUint64(raw[52:60], uint64(cut.cutOffset))
	copy(raw[60:92], cut.cutSHA256[:])
	copy(raw[92:124], cut.cutChainSHA256[:])
	binary.BigEndian.PutUint64(raw[124:132], cut.tipSeq)
	binary.BigEndian.PutUint64(raw[132:140], uint64(cut.tipOffset))
	copy(raw[140:172], cut.tipSHA256[:])
	copy(raw[172:204], cut.tipChainSHA256[:])
	sum := sha256.Sum256(raw[:receiptArchiveWALCutPayloadSize])
	copy(raw[receiptArchiveWALCutPayloadSize:], sum[:])
	return raw
}

func decodeReceiptArchiveWALCut(raw []byte) (receiptArchiveWALCut, error) {
	if len(raw) != receiptArchiveWALCutSize || string(raw[:8]) != receiptArchiveWALCutMagic ||
		binary.BigEndian.Uint16(raw[8:10]) != receiptArchiveWALCutVersion || raw[10] != 0 || raw[11] != 0 {
		return receiptArchiveWALCut{}, errReceiptArchiveWALCut
	}
	sum := sha256.Sum256(raw[:receiptArchiveWALCutPayloadSize])
	if !bytes.Equal(raw[receiptArchiveWALCutPayloadSize:], sum[:]) {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: manifest checksum differs", errReceiptArchiveWALCut)
	}
	var cut receiptArchiveWALCut
	copy(cut.archiveSHA256[:], raw[12:44])
	cut.cutSeq = binary.BigEndian.Uint64(raw[44:52])
	cutOffset := binary.BigEndian.Uint64(raw[52:60])
	copy(cut.cutSHA256[:], raw[60:92])
	copy(cut.cutChainSHA256[:], raw[92:124])
	cut.tipSeq = binary.BigEndian.Uint64(raw[124:132])
	tipOffset := binary.BigEndian.Uint64(raw[132:140])
	copy(cut.tipSHA256[:], raw[140:172])
	copy(cut.tipChainSHA256[:], raw[172:204])
	if cutOffset > math.MaxInt64 || tipOffset > math.MaxInt64 {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: FileWAL witness offset overflows", errReceiptArchiveWALCut)
	}
	cut.cutOffset = int64(cutOffset)
	cut.tipOffset = int64(tipOffset)
	if cut.archiveSHA256 == ([sha256.Size]byte{}) {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: zero archive digest", errReceiptArchiveWALCut)
	}
	if err := validateReceiptArchiveWALWitness("cut", cut.cutSeq, cut.cutOffset, cut.cutSHA256, cut.cutChainSHA256); err != nil {
		return receiptArchiveWALCut{}, err
	}
	if err := validateReceiptArchiveWALWitness("tip", cut.tipSeq, cut.tipOffset, cut.tipSHA256, cut.tipChainSHA256); err != nil {
		return receiptArchiveWALCut{}, err
	}
	if cut.cutSeq > cut.tipSeq || cut.cutOffset > cut.tipOffset {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: archive cut exceeds observed FileWAL tip", errReceiptArchiveWALCut)
	}
	if cut.cutSeq == cut.tipSeq {
		if cut.cutOffset != cut.tipOffset || cut.cutSHA256 != cut.tipSHA256 ||
			cut.cutChainSHA256 != cut.tipChainSHA256 {
			return receiptArchiveWALCut{}, fmt.Errorf("%w: equal FileWAL sequences have different witnesses", errReceiptArchiveWALCut)
		}
	} else if cut.cutOffset >= cut.tipOffset {
		return receiptArchiveWALCut{}, fmt.Errorf("%w: later FileWAL tip does not advance offset", errReceiptArchiveWALCut)
	}
	return cut, nil
}

func validateReceiptArchiveWALWitness(
	name string,
	seq uint64,
	offset int64,
	digest, chain [sha256.Size]byte,
) error {
	if offset < receiptArchiveWALZeroOffset ||
		(seq == 0) != (offset == receiptArchiveWALZeroOffset) {
		return fmt.Errorf("%w: invalid FileWAL %s offset", errReceiptArchiveWALCut, name)
	}
	if digest == ([sha256.Size]byte{}) || chain == ([sha256.Size]byte{}) {
		return fmt.Errorf("%w: zero FileWAL %s witness", errReceiptArchiveWALCut, name)
	}
	return nil
}
