package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

const (
	receiptCombinedBaselineMagic      = "LANTCBLN"
	receiptCombinedBaselineVersion    = uint16(3)
	receiptCombinedBaselineReserved   = uint16(0)
	receiptCombinedBaselineHeaderSize = 8 + 2 + 2 + 2 + 2 + 8 + 8 + 2*sha256.Size
	receiptCombinedBaselineFooterSize = sha256.Size
	receiptCombinedBaselineMaxBytes   = 2*wholeStateArchiveMaxBytes +
		receiptCombinedBaselineHeaderSize + receiptCombinedBaselineFooterSize

	retiredCatalogArchiveMagic      = "LANTRET1"
	retiredCatalogArchiveVersion    = uint16(2)
	retiredCatalogArchiveHeaderSize = 8 + 2 + 2 + 4 + 16 + 8 + 8 + 8 + 4 + 8
	retiredCatalogArchiveFooterSize = 8 + 8 + sha256.Size
	retiredCatalogEpochRecord       = byte(1)
	retiredCatalogReceiptRecord     = byte(2)
	retiredCatalogFooterRecord      = byte(255)
	retiredCatalogEpochPayloadSize  = 16 + 8 + 8 + 8 + sha256.Size + 8
)

var errReceiptCombinedBaseline = errors.New("backup: invalid combined receipt baseline")

// ReceiptBaselineCodec is the canonical private combined baseline adapter
// used by the durable runtime.
// Its active member remains the reusable LANTARCH codec; the combined
// container also carries retired evidence.
type ReceiptBaselineCodec struct{}

type retiredCatalogArchiveMetadata struct {
	ActiveEpoch          mutationreceipt.Epoch
	ClockHighWaterMillis int64
	MaxEntries           int
	MaxBytes             uint64
}

func (ReceiptBaselineCodec) EncodeCombinedReceiptBaseline(
	ctx context.Context,
	capture dataruntime.ReceiptWholeStateCapture,
) ([]byte, error) {
	active, retired, err := encodeReceiptBaselineSections(ctx, capture)
	if err != nil {
		return nil, err
	}
	raw, err := encodeCombinedReceiptBaseline(active, retired)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

// DigestCombinedReceiptBaseline hashes the canonical archive without retaining
// either encoded section. The first pass provides the section lengths and
// checksums for the header. The second hashes the complete ordered container.
func (ReceiptBaselineCodec) DigestCombinedReceiptBaseline(
	ctx context.Context,
	capture dataruntime.ReceiptWholeStateCapture,
) ([32]byte, uint64, error) {
	archive, retired, err := validateReceiptBaselineCapture(ctx, capture)
	if err != nil {
		return [32]byte{}, 0, err
	}
	activeHash := sha256.New()
	activeSize, err := writeValidatedWholeStateArchive(ctx, activeHash, archive)
	if err != nil {
		return [32]byte{}, 0, err
	}
	retiredHash := sha256.New()
	retiredSize, err := writeRetiredCatalogArchive(ctx, retiredHash, capture.Policy, retired)
	if err != nil {
		return [32]byte{}, 0, err
	}
	var activeDigest, retiredDigest [sha256.Size]byte
	activeHash.Sum(activeDigest[:0])
	retiredHash.Sum(retiredDigest[:0])
	header, size, err := receiptBaselineHeader(activeSize, retiredSize, activeDigest, retiredDigest)
	if err != nil {
		return [32]byte{}, 0, err
	}
	h := sha256.New()
	h.Write(header[:])
	if _, err := writeValidatedWholeStateArchive(ctx, h, archive); err != nil {
		return [32]byte{}, 0, err
	}
	if _, err := writeRetiredCatalogArchive(ctx, h, capture.Policy, retired); err != nil {
		return [32]byte{}, 0, err
	}
	var digest [32]byte
	h.Sum(digest[:0])
	h.Write(digest[:])
	h.Sum(digest[:0])
	if err := ctx.Err(); err != nil {
		return [32]byte{}, 0, err
	}
	return digest, uint64(size), nil
}

func validateReceiptBaselineCapture(ctx context.Context, capture dataruntime.ReceiptWholeStateCapture) (wholeStateArchive, mutationreceipt.RetiredCatalogSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return wholeStateArchive{}, mutationreceipt.RetiredCatalogSnapshot{}, err
	}
	if capture.Retired.ClockHighWaterMillis != capture.Receipts.ClockHighWaterMillis {
		return wholeStateArchive{}, mutationreceipt.RetiredCatalogSnapshot{}, fmt.Errorf("%w: active and retired high-water differ", errReceiptCombinedBaseline)
	}
	archive := wholeStateArchive{
		Graph:    capture.Graph,
		Receipts: capture.Receipts,
		Policy:   capture.Policy,
		Origins:  capture.Origins,
	}
	if err := validateWholeStateArchive(archive); err != nil {
		return wholeStateArchive{}, mutationreceipt.RetiredCatalogSnapshot{}, err
	}
	retired, err := canonicalRetiredCatalogArchive(capture.Policy, capture.Receipts.ClockHighWaterMillis, capture.Retired)
	if err != nil {
		return wholeStateArchive{}, mutationreceipt.RetiredCatalogSnapshot{}, err
	}
	return archive, retired, ctx.Err()
}

func encodeReceiptBaselineSections(ctx context.Context, capture dataruntime.ReceiptWholeStateCapture) ([]byte, []byte, error) {
	archive, _, err := validateReceiptBaselineCapture(ctx, capture)
	if err != nil {
		return nil, nil, err
	}
	var active, retired bytes.Buffer
	if _, err := writeValidatedWholeStateArchive(ctx, &active, archive); err != nil {
		return nil, nil, err
	}
	if _, err := writeRetiredCatalogArchive(ctx, &retired, capture.Policy, capture.Retired); err != nil {
		return nil, nil, err
	}
	return active.Bytes(), retired.Bytes(), ctx.Err()
}

func (ReceiptBaselineCodec) StageCombinedReceiptBaseline(
	ctx context.Context,
	raw []byte,
	expected mutationreceipt.Config,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*dataruntime.ReceiptBaselineCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	activeRaw, retired, retiredMetadata, err := decodeCombinedReceiptBaseline(raw)
	if err != nil {
		return nil, err
	}

	archive, err := decodeWholeStateArchiveBytes(activeRaw, true)
	if err != nil {
		return nil, err
	}
	stage, err := stageDecodedReceiptWholeStateArchive(ctx, archive, expected, defaultTTL, configureGraph)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stage == nil || stage.graph == nil || stage.receipts == nil {
		return nil, fmt.Errorf("%w: staged baseline is incomplete", errWholeStateArchive)
	}
	activeState, err := stage.receipts.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("%w: snapshot staged active Store: %v", errReceiptCombinedBaseline, err)
	}
	if retired.ClockHighWaterMillis != activeState.ClockHighWaterMillis {
		return nil, fmt.Errorf("%w: active and retired high-water differ", errReceiptCombinedBaseline)
	}
	if retiredMetadata.ActiveEpoch != stage.policy.Epoch ||
		retiredMetadata.MaxEntries != stage.policy.MaxEntries ||
		retiredMetadata.MaxBytes != stage.policy.MaxBytes ||
		retiredMetadata.ClockHighWaterMillis != activeState.ClockHighWaterMillis {
		return nil, fmt.Errorf("%w: active and retired policy metadata differ", errReceiptCombinedBaseline)
	}
	if err := validateRetiredCatalogArchive(stage.policy, activeState.ClockHighWaterMillis, retired); err != nil {
		return nil, err
	}
	return receiptBaselineCandidate(archive, stage, retired), nil
}

func receiptBaselineCandidate(archive wholeStateArchive, stage *receiptWholeStateStage, retired mutationreceipt.RetiredCatalogSnapshot) *dataruntime.ReceiptBaselineCandidate {
	return &dataruntime.ReceiptBaselineCandidate{
		NamespaceFormat: archive.Graph[0].GetHeader().GetNamespaceFormat(),
		Graph:           stage.graph,
		Receipts:        stage.receipts,
		Retired:         retired,
		Policy:          stage.policy,
		Origins:         append([]dataruntime.OriginState(nil), stage.origins...),
		CutoffLocalSeq:  stage.cutoffLocalSeq,
		CutoffHLC:       stage.cutoffHLC,
	}
}

func encodeCombinedReceiptBaseline(active, retired []byte) ([]byte, error) {
	header, total, err := combinedReceiptBaselineHeader(active, retired)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 0, total)
	raw = append(raw, header[:]...)
	raw = append(raw, active...)
	raw = append(raw, retired...)
	digest := sha256.Sum256(raw)
	raw = append(raw, digest[:]...)
	return raw, nil
}

func combinedReceiptBaselineHeader(active, retired []byte) ([receiptCombinedBaselineHeaderSize]byte, int, error) {
	activeDigest := sha256.Sum256(active)
	retiredDigest := sha256.Sum256(retired)
	return receiptBaselineHeader(len(active), len(retired), activeDigest, retiredDigest)
}

func receiptBaselineHeader(activeSize, retiredSize int, activeDigest, retiredDigest [sha256.Size]byte) ([receiptCombinedBaselineHeaderSize]byte, int, error) {
	var raw [receiptCombinedBaselineHeaderSize]byte
	if activeSize == 0 || activeSize > wholeStateArchiveMaxBytes ||
		retiredSize == 0 || retiredSize > wholeStateArchiveMaxBytes ||
		activeSize > math.MaxInt-retiredSize-receiptCombinedBaselineHeaderSize-receiptCombinedBaselineFooterSize {
		return raw, 0, fmt.Errorf("%w: section size exceeds limit", errReceiptCombinedBaseline)
	}
	total := receiptCombinedBaselineHeaderSize + activeSize + retiredSize + receiptCombinedBaselineFooterSize
	if total > receiptCombinedBaselineMaxBytes {
		return raw, 0, fmt.Errorf("%w: container exceeds byte limit", errReceiptCombinedBaseline)
	}
	copy(raw[:], receiptCombinedBaselineMagic)
	binary.BigEndian.PutUint16(raw[8:10], receiptCombinedBaselineVersion)
	binary.BigEndian.PutUint16(raw[10:12], wholeStateArchiveVersion)
	binary.BigEndian.PutUint16(raw[12:14], retiredCatalogArchiveVersion)
	binary.BigEndian.PutUint16(raw[14:16], receiptCombinedBaselineReserved)
	binary.BigEndian.PutUint64(raw[16:24], uint64(activeSize))
	binary.BigEndian.PutUint64(raw[24:32], uint64(retiredSize))
	copy(raw[32:64], activeDigest[:])
	copy(raw[64:96], retiredDigest[:])
	return raw, total, nil
}

func decodeCombinedReceiptBaseline(
	raw []byte,
) ([]byte, mutationreceipt.RetiredCatalogSnapshot, retiredCatalogArchiveMetadata, error) {
	if len(raw) < receiptCombinedBaselineHeaderSize+receiptCombinedBaselineFooterSize ||
		len(raw) > receiptCombinedBaselineMaxBytes ||
		string(raw[:8]) != receiptCombinedBaselineMagic ||
		binary.BigEndian.Uint16(raw[8:10]) != receiptCombinedBaselineVersion ||
		binary.BigEndian.Uint16(raw[10:12]) != wholeStateArchiveVersion ||
		binary.BigEndian.Uint16(raw[12:14]) != retiredCatalogArchiveVersion ||
		binary.BigEndian.Uint16(raw[14:16]) != receiptCombinedBaselineReserved {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
	}
	activeSize := binary.BigEndian.Uint64(raw[16:24])
	retiredSize := binary.BigEndian.Uint64(raw[24:32])
	payloadSize := uint64(len(raw) - receiptCombinedBaselineHeaderSize - receiptCombinedBaselineFooterSize)
	if activeSize == 0 || activeSize > wholeStateArchiveMaxBytes ||
		retiredSize == 0 || retiredSize > wholeStateArchiveMaxBytes ||
		activeSize > payloadSize || retiredSize != payloadSize-activeSize {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
	}
	payloadEnd := len(raw) - receiptCombinedBaselineFooterSize
	digest := sha256.Sum256(raw[:payloadEnd])
	if !bytes.Equal(raw[payloadEnd:], digest[:]) {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, fmt.Errorf("%w: container checksum mismatch", errReceiptCombinedBaseline)
	}
	activeStart := receiptCombinedBaselineHeaderSize
	activeEnd := activeStart + int(activeSize)
	active := raw[activeStart:activeEnd:activeEnd]
	retiredRaw := raw[activeEnd:payloadEnd]
	activeDigest := sha256.Sum256(active)
	retiredDigest := sha256.Sum256(retiredRaw)
	if !bytes.Equal(raw[32:64], activeDigest[:]) || !bytes.Equal(raw[64:96], retiredDigest[:]) {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, fmt.Errorf("%w: section checksum mismatch", errReceiptCombinedBaseline)
	}
	retired, retiredMetadata, err := decodeRetiredCatalogArchive(retiredRaw)
	if err != nil {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, err
	}
	canonicalRetired, err := encodeRetiredCatalogArchiveWithoutPolicy(retiredMetadata, retired)
	if err != nil {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, err
	}
	// The fixed header, section sizes and digests above already verify the
	// container and its active bytes. Only the retired section was re-encoded.
	if !bytes.Equal(canonicalRetired, retiredRaw) {
		return nil, mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, fmt.Errorf("%w: container is not canonical", errReceiptCombinedBaseline)
	}
	return active, retired, retiredMetadata, nil
}

func encodeRetiredCatalogArchive(
	active mutationreceipt.Config,
	state mutationreceipt.RetiredCatalogSnapshot,
) ([]byte, error) {
	if err := validateRetiredCatalogArchive(active, state.ClockHighWaterMillis, state); err != nil {
		return nil, err
	}
	return marshalRetiredCatalogArchive(active, state)
}

func encodeRetiredCatalogArchiveWithoutPolicy(
	metadata retiredCatalogArchiveMetadata,
	state mutationreceipt.RetiredCatalogSnapshot,
) ([]byte, error) {
	active := mutationreceipt.Config{
		Epoch:      metadata.ActiveEpoch,
		Retention:  time.Millisecond,
		MaxEntries: metadata.MaxEntries,
		MaxBytes:   metadata.MaxBytes,
	}
	if err := validateRetiredCatalogArchive(active, metadata.ClockHighWaterMillis, state); err != nil {
		return nil, err
	}
	return marshalRetiredCatalogArchive(active, state)
}

func marshalRetiredCatalogArchive(
	active mutationreceipt.Config,
	state mutationreceipt.RetiredCatalogSnapshot,
) ([]byte, error) {
	var out bytes.Buffer
	if _, err := writeRetiredCatalogArchive(context.Background(), &out, active, state); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeRetiredCatalogArchive(ctx context.Context, w io.Writer, active mutationreceipt.Config, state mutationreceipt.RetiredCatalogSnapshot) (int, error) {
	var header bytes.Buffer
	header.WriteString(retiredCatalogArchiveMagic)
	writeArchiveU16(&header, retiredCatalogArchiveVersion)
	writeArchiveU16(&header, 0)
	writeArchiveU32(&header, 0)
	header.Write(active.Epoch[:])
	writeArchiveU64(&header, uint64(state.ClockHighWaterMillis))
	writeArchiveU64(&header, uint64(active.MaxEntries))
	writeArchiveU64(&header, active.MaxBytes)
	writeArchiveU32(&header, uint32(len(state.Epochs)))
	receiptCount := uint64(0)
	for _, member := range state.Epochs {
		if uint64(len(member.State.Receipts)) > math.MaxUint64-receiptCount {
			return 0, fmt.Errorf("%w: receipt count overflow", errReceiptCombinedBaseline)
		}
		receiptCount += uint64(len(member.State.Receipts))
	}
	writeArchiveU64(&header, receiptCount)
	counts := []uint64{uint64(len(state.Epochs)), receiptCount}
	return writeArchiveSection(ctx, w, header.Bytes(), counts, func(visit func(byte, []byte) error) error {
		for _, member := range state.Epochs {
			var payload bytes.Buffer
			payload.Write(member.Policy.Epoch[:])
			writeArchiveU64(&payload, uint64(member.Policy.Retention/time.Millisecond))
			writeArchiveU64(&payload, uint64(member.Policy.MaxEntries))
			writeArchiveU64(&payload, member.Policy.MaxBytes)
			payload.Write(member.State.PolicyFingerprint[:])
			writeArchiveU64(&payload, uint64(len(member.State.Receipts)))
			if err := visit(retiredCatalogEpochRecord, payload.Bytes()); err != nil {
				return err
			}
			for _, receipt := range member.State.Receipts {
				if err := visit(retiredCatalogReceiptRecord, encodeArchiveReceipt(receipt)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func decodeRetiredCatalogArchive(
	raw []byte,
) (mutationreceipt.RetiredCatalogSnapshot, retiredCatalogArchiveMetadata, error) {
	if len(raw) < retiredCatalogArchiveHeaderSize+5+retiredCatalogArchiveFooterSize ||
		len(raw) > wholeStateArchiveMaxBytes ||
		string(raw[:8]) != retiredCatalogArchiveMagic ||
		binary.BigEndian.Uint16(raw[8:10]) != retiredCatalogArchiveVersion ||
		binary.BigEndian.Uint16(raw[10:12]) != 0 ||
		binary.BigEndian.Uint32(raw[12:16]) != 0 {
		return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
	}
	metadata, err := retiredCatalogMetadataFromArchive(raw)
	if err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, err
	}
	highWater := binary.BigEndian.Uint64(raw[32:40])
	epochCount := binary.BigEndian.Uint32(raw[56:60])
	receiptCount := binary.BigEndian.Uint64(raw[60:68])
	if highWater > math.MaxInt64 || uint64(epochCount) > uint64(metadata.MaxEntries) ||
		receiptCount > uint64(metadata.MaxEntries) {
		return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
	}
	state := mutationreceipt.RetiredCatalogSnapshot{
		Version:              1, // Core snapshot schema is independent of archive framing.
		ClockHighWaterMillis: int64(highWater),
		Epochs:               make([]mutationreceipt.RetiredEpochSnapshot, 0),
	}
	off := retiredCatalogArchiveHeaderSize
	var current *mutationreceipt.RetiredEpochSnapshot
	var remaining uint64
	var decodedReceipts uint64
	for off < len(raw) {
		start := off
		if len(raw)-off < 5 {
			return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
		}
		kind := raw[off]
		size := binary.BigEndian.Uint32(raw[off+1 : off+5])
		off += 5
		if size == 0 || size > wholeStateArchiveMaxFrame || uint64(size) > uint64(len(raw)-off) {
			return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
		}
		payload := raw[off : off+int(size)]
		off += int(size)
		switch kind {
		case retiredCatalogEpochRecord:
			if remaining != 0 || len(payload) != retiredCatalogEpochPayloadSize ||
				uint64(len(state.Epochs)) == uint64(epochCount) {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
			}
			member, count, err := decodeRetiredCatalogEpoch(payload, state.ClockHighWaterMillis)
			if err != nil || count == 0 || count > receiptCount-decodedReceipts {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
			}
			state.Epochs = append(state.Epochs, member)
			current = &state.Epochs[len(state.Epochs)-1]
			remaining = count
		case retiredCatalogReceiptRecord:
			if current == nil || remaining == 0 {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
			}
			receipt, err := decodeArchiveReceipt(payload)
			if err != nil {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, err
			}
			current.State.Receipts = append(current.State.Receipts, receipt)
			remaining--
			decodedReceipts++
		case retiredCatalogFooterRecord:
			if remaining != 0 || off != len(raw) || len(payload) != retiredCatalogArchiveFooterSize ||
				uint64(len(state.Epochs)) != uint64(epochCount) || decodedReceipts != receiptCount ||
				binary.BigEndian.Uint64(payload[:8]) != uint64(epochCount) ||
				binary.BigEndian.Uint64(payload[8:16]) != receiptCount {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
			}
			digest := sha256.Sum256(raw[:start])
			if !bytes.Equal(payload[16:], digest[:]) {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
			}
			active := mutationreceipt.Config{
				Epoch:      metadata.ActiveEpoch,
				Retention:  time.Millisecond,
				MaxEntries: metadata.MaxEntries,
				MaxBytes:   metadata.MaxBytes,
			}
			if err := validateRetiredCatalogArchive(active, state.ClockHighWaterMillis, state); err != nil {
				return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, err
			}
			return state, metadata, nil
		default:
			return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
		}
	}
	return mutationreceipt.RetiredCatalogSnapshot{}, retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
}

func retiredCatalogMetadataFromArchive(raw []byte) (retiredCatalogArchiveMetadata, error) {
	if len(raw) < retiredCatalogArchiveHeaderSize {
		return retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
	}
	var epoch mutationreceipt.Epoch
	copy(epoch[:], raw[16:32])
	highWater := binary.BigEndian.Uint64(raw[32:40])
	maxEntries := binary.BigEndian.Uint64(raw[40:48])
	maxBytes := binary.BigEndian.Uint64(raw[48:56])
	maxInt := uint64(^uint(0) >> 1)
	if epoch == (mutationreceipt.Epoch{}) || highWater > math.MaxInt64 ||
		maxEntries == 0 || maxEntries > maxInt || maxBytes == 0 {
		return retiredCatalogArchiveMetadata{}, errReceiptCombinedBaseline
	}
	return retiredCatalogArchiveMetadata{
		ActiveEpoch:          epoch,
		ClockHighWaterMillis: int64(highWater),
		MaxEntries:           int(maxEntries),
		MaxBytes:             maxBytes,
	}, nil
}

func decodeRetiredCatalogEpoch(
	raw []byte,
	highWaterMillis int64,
) (mutationreceipt.RetiredEpochSnapshot, uint64, error) {
	var member mutationreceipt.RetiredEpochSnapshot
	if len(raw) != retiredCatalogEpochPayloadSize {
		return member, 0, errReceiptCombinedBaseline
	}
	copy(member.Policy.Epoch[:], raw[:16])
	retentionMS := binary.BigEndian.Uint64(raw[16:24])
	maxEntries := binary.BigEndian.Uint64(raw[24:32])
	member.Policy.MaxBytes = binary.BigEndian.Uint64(raw[32:40])
	copy(member.State.PolicyFingerprint[:], raw[40:72])
	count := binary.BigEndian.Uint64(raw[72:80])
	maxInt := uint64(^uint(0) >> 1)
	if member.Policy.Epoch == (mutationreceipt.Epoch{}) ||
		retentionMS > uint64(math.MaxInt64/int64(time.Millisecond)) ||
		maxEntries == 0 || maxEntries > maxInt || member.Policy.MaxBytes == 0 ||
		count == 0 || count > maxInt {
		return mutationreceipt.RetiredEpochSnapshot{}, 0, errReceiptCombinedBaseline
	}
	member.Policy.Retention = time.Duration(retentionMS) * time.Millisecond
	member.Policy.MaxEntries = int(maxEntries)
	member.State.Version = 1 // Core snapshot schema is independent of archive framing.
	member.State.Epoch = member.Policy.Epoch
	member.State.ClockHighWaterMillis = highWaterMillis
	member.State.Receipts = make([]mutationreceipt.Receipt, 0)
	return member, count, nil
}

func validateRetiredCatalogArchive(
	active mutationreceipt.Config,
	activeHighWaterMillis int64,
	state mutationreceipt.RetiredCatalogSnapshot,
) error {
	_, err := canonicalRetiredCatalogArchive(active, activeHighWaterMillis, state)
	return err
}

func canonicalRetiredCatalogArchive(
	active mutationreceipt.Config,
	activeHighWaterMillis int64,
	state mutationreceipt.RetiredCatalogSnapshot,
) (mutationreceipt.RetiredCatalogSnapshot, error) {
	if state.ClockHighWaterMillis != activeHighWaterMillis {
		return mutationreceipt.RetiredCatalogSnapshot{}, fmt.Errorf("%w: retired high-water differs from active Store", errReceiptCombinedBaseline)
	}
	config, err := retiredCatalogConfigFromActive(active, activeHighWaterMillis)
	if err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, err
	}
	catalog, err := mutationreceipt.NewRetiredCatalogFromSnapshot(config, state)
	if err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, errors.Join(errReceiptCombinedBaseline, err)
	}
	canonical, err := catalog.Snapshot(time.UnixMilli(activeHighWaterMillis))
	if err != nil {
		return mutationreceipt.RetiredCatalogSnapshot{}, errors.Join(errReceiptCombinedBaseline, err)
	}
	if !reflect.DeepEqual(canonical, state) {
		return mutationreceipt.RetiredCatalogSnapshot{}, fmt.Errorf("%w: retired catalog is not canonical", errReceiptCombinedBaseline)
	}
	return canonical, nil
}

func retiredCatalogConfigFromActive(
	active mutationreceipt.Config,
	highWaterMillis int64,
) (mutationreceipt.RetiredCatalogConfig, error) {
	if highWaterMillis < 0 {
		return mutationreceipt.RetiredCatalogConfig{}, mutationreceipt.ErrInvalidClock
	}
	config := mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    active.Epoch,
		MaxEntries:     active.MaxEntries,
		MaxBytes:       active.MaxBytes,
		ClockHighWater: time.UnixMilli(highWaterMillis),
	}
	if _, err := mutationreceipt.NewRetiredCatalog(config); err != nil {
		return mutationreceipt.RetiredCatalogConfig{}, err
	}
	return config, nil
}
