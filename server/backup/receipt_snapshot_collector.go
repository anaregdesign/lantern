package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

var errReceiptSnapshotCollect = errors.New("backup: invalid receipt Snapshot stream")

// ReceiptSnapshotStream is the transport-neutral client-stream surface used
// by the detached collector. Connect's ServerStreamForClient satisfies it.
type ReceiptSnapshotStream interface {
	Receive() bool
	Msg() *pb.SnapshotResponse
	Err() error
}

// ReceiptSnapshotWireStream optionally exposes the exact protobuf bytes for
// the current message. Raw-observing transports use it to reject ambiguous or
// noncanonical encodings. Connect's decoded client stream does not expose its
// original protobuf bytes, so that path validates the parsed message and then
// deterministically re-encodes it; the incoming encoding is not authoritative.
type ReceiptSnapshotWireStream interface {
	ReceiptSnapshotStream
	ReceiptSnapshotWireBytes() []byte
}

// ReceiptSnapshotCollectorLimits bounds every network-controlled dimension.
// Callers must choose all limits explicitly; zero never means unlimited.
type ReceiptSnapshotCollectorLimits struct {
	MaxFrameBytes      uint64
	MaxFrames          uint64
	MaxTotalBytes      uint64
	MaxActiveReceipts  uint64
	MaxRetiredEpochs   uint64
	MaxRetiredReceipts uint64
	MaxOrigins         uint64
	MaxGraphFrames     uint64
}

// ReceiptSnapshotCollectorConfig describes one unwired collector/stager.
// TempDir owns only per-call temporary files. ExpectedPolicy and
// ExpectedRetiredConfig are the local immutable active and aggregate retired
// policies the candidate must satisfy.
type ReceiptSnapshotCollectorConfig struct {
	TempDir               string
	Limits                ReceiptSnapshotCollectorLimits
	ExpectedPolicy        mutationreceipt.Config
	ExpectedRetiredConfig mutationreceipt.RetiredCatalogConfig
	DefaultTTL            time.Duration
	ConfigureGraph        func(*graphcache.GraphCache[string, *pb.Vertex]) error
}

// ReceiptSnapshotCollector consumes RECEIPT_V2 streams into detached state.
// It has no reference to a serving graph, Store, origin tracker, HLC, WAL,
// generation, or subscriber and therefore cannot publish a candidate.
type ReceiptSnapshotCollector struct {
	config ReceiptSnapshotCollectorConfig
}

// ReceiptSnapshotCandidateMetadata is safe to inspect before a later atomic
// installer. Header is cloned on return and includes the complete wire receipt
// metadata and origin vector.
type ReceiptSnapshotCandidateMetadata struct {
	Header      *pb.SnapshotHeader
	Policy      mutationreceipt.Config
	SpoolSHA256 [sha256.Size]byte
	SpoolBytes  uint64
}

// ReceiptSnapshotCandidate owns one validated canonical V2 frame spool and
// one unpublished staged graph/Store/retired-catalog cut. Close removes the
// temporary file.
// The private stage is intentionally unavailable to production callers until
// a separate atomic installer consumes it inside this package.
type ReceiptSnapshotCandidate struct {
	mu             sync.Mutex
	spool          *os.File
	path           string
	metadata       ReceiptSnapshotCandidateMetadata
	stage          *receiptSnapshotV2Stage
	frameCount     uint64
	limits         ReceiptSnapshotCollectorLimits
	expectedPolicy mutationreceipt.Config
	retiredConfig  mutationreceipt.RetiredCatalogConfig
	closed         bool
}

type receiptSnapshotV2Stage struct {
	*receiptWholeStateStage
	retired *mutationreceipt.RetiredCatalog
}

// NewReceiptSnapshotCollector validates the bounded collector configuration.
func NewReceiptSnapshotCollector(config ReceiptSnapshotCollectorConfig) (*ReceiptSnapshotCollector, error) {
	if config.TempDir == "" {
		return nil, errors.New("backup: receipt Snapshot collector requires a temporary directory")
	}
	info, err := os.Stat(config.TempDir)
	if err != nil {
		return nil, fmt.Errorf("backup: stat receipt Snapshot temporary directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("backup: receipt Snapshot temporary path is not a directory")
	}
	limits := config.Limits
	maxInt := uint64(^uint(0) >> 1)
	if limits.MaxFrameBytes == 0 || limits.MaxFrameBytes > wholeStateArchiveMaxFrame ||
		limits.MaxFrames < 2 ||
		limits.MaxFrames > maxInt ||
		limits.MaxTotalBytes == 0 || limits.MaxTotalBytes > wholeStateArchiveMaxBytes ||
		limits.MaxActiveReceipts == 0 || limits.MaxRetiredEpochs == 0 ||
		limits.MaxRetiredReceipts == 0 || limits.MaxOrigins == 0 ||
		limits.MaxGraphFrames == 0 ||
		limits.MaxActiveReceipts > limits.MaxFrames ||
		limits.MaxRetiredEpochs > limits.MaxFrames ||
		limits.MaxRetiredReceipts > limits.MaxFrames ||
		limits.MaxGraphFrames > limits.MaxFrames {
		return nil, errors.New("backup: receipt Snapshot collector limits are invalid")
	}
	if _, err := mutationreceipt.New(config.ExpectedPolicy); err != nil {
		return nil, fmt.Errorf("backup: receipt Snapshot expected policy: %w", err)
	}
	retiredConfig := config.ExpectedRetiredConfig
	if retiredConfig.ActiveEpoch != config.ExpectedPolicy.Epoch {
		return nil, errors.New("backup: receipt Snapshot retired catalog active epoch differs from expected policy")
	}
	retiredConfig.ClockHighWater = time.Time{}
	if _, err := mutationreceipt.NewRetiredCatalog(retiredConfig); err != nil {
		return nil, fmt.Errorf("backup: receipt Snapshot expected retired catalog policy: %w", err)
	}
	if config.DefaultTTL <= 0 {
		return nil, errors.New("backup: receipt Snapshot collector requires a positive default TTL")
	}
	return &ReceiptSnapshotCollector{config: config}, nil
}

func receiptSnapshotCollectError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errReceiptSnapshotCollect, fmt.Sprintf(format, args...))
}

// Collect fully receives, validates, canonicalizes, and stages one stream. No
// candidate escapes on cancellation, receive failure, protocol error, limit
// breach, spool failure, or detached graph/Store/catalog staging failure.
func (c *ReceiptSnapshotCollector) Collect(
	ctx context.Context,
	stream ReceiptSnapshotStream,
) (*ReceiptSnapshotCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || stream == nil {
		return nil, receiptSnapshotCollectError("collector or stream is nil")
	}

	spool, err := os.CreateTemp(c.config.TempDir, ".lantern-receipt-snapshot-*.stream")
	if err != nil {
		return nil, fmt.Errorf("backup: create receipt Snapshot spool: %w", err)
	}
	spoolPath := spool.Name()
	keepSpool := false
	defer func() {
		if !keepSpool {
			_ = spool.Close()
			_ = os.Remove(spoolPath)
		}
	}()

	var (
		frameCount uint64
		totalBytes uint64
		state      receiptSnapshotCollectState
	)
	for stream.Receive() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame := stream.Msg()
		if err := service.ValidateReceiptSnapshotFrame(frame); err != nil {
			return nil, receiptSnapshotCollectError("%v", err)
		}
		raw, err := receiptSnapshotFrameBytes(stream, frame)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 || uint64(len(raw)) > c.config.Limits.MaxFrameBytes {
			return nil, receiptSnapshotCollectError("frame exceeds byte limit")
		}
		if frameCount == c.config.Limits.MaxFrames {
			return nil, receiptSnapshotCollectError("frame count exceeds limit")
		}
		recordBytes := uint64(4 + len(raw))
		if recordBytes > c.config.Limits.MaxTotalBytes-totalBytes {
			return nil, receiptSnapshotCollectError("stream exceeds total byte limit")
		}
		if err := c.acceptReceiptSnapshotFrame(frame, frameCount, &state); err != nil {
			return nil, err
		}
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
		if err := writeReceiptSnapshotSpool(spool, size[:], raw); err != nil {
			return nil, err
		}
		frameCount++
		totalBytes += recordBytes
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("backup: receive receipt Snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !state.sawHeader || !state.sawFooter {
		return nil, receiptSnapshotCollectError("stream ended before complete header/footer framing")
	}
	if err := spool.Sync(); err != nil {
		return nil, fmt.Errorf("backup: sync receipt Snapshot spool: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("backup: rewind receipt Snapshot spool: %w", err)
	}
	frames, err := readReceiptSnapshotSpool(ctx, spool, frameCount, c.config.Limits)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wireHeader := proto.Clone(frames[0].GetHeader()).(*pb.SnapshotHeader)
	retiredConfig := c.config.ExpectedRetiredConfig
	retiredConfig.ClockHighWater = time.Time{}
	capture, err := service.DecodeReceiptSnapshotFrames(
		frames,
		c.config.ExpectedPolicy,
		retiredConfig,
	)
	if err != nil {
		return nil, receiptSnapshotCollectError("%v", err)
	}
	var retiredReceipts uint64
	for _, member := range capture.Retired.Epochs {
		count := uint64(len(member.State.Receipts))
		if count > c.config.Limits.MaxRetiredReceipts-retiredReceipts {
			return nil, receiptSnapshotCollectError("decoded retired receipt count exceeds limit")
		}
		retiredReceipts += count
	}
	if uint64(len(capture.Receipts.Receipts)) > c.config.Limits.MaxActiveReceipts ||
		uint64(len(capture.Retired.Epochs)) > c.config.Limits.MaxRetiredEpochs ||
		retiredReceipts > c.config.Limits.MaxRetiredReceipts ||
		uint64(len(capture.Origins)) > c.config.Limits.MaxOrigins ||
		uint64(len(capture.Graph)-2) > c.config.Limits.MaxGraphFrames {
		return nil, receiptSnapshotCollectError("decoded section count exceeds limit")
	}
	canonicalFrames, err := service.PrepareReceiptSnapshotFrames(capture, c.config.ExpectedPolicy)
	if err != nil {
		return nil, receiptSnapshotCollectError("reconstruct canonical frames: %v", err)
	}
	if len(canonicalFrames) != len(frames) {
		return nil, receiptSnapshotCollectError("canonical frame count changed after decode")
	}
	for i := range frames {
		if !proto.Equal(canonicalFrames[i], frames[i]) {
			return nil, receiptSnapshotCollectError("frame %d is not canonical", i)
		}
	}
	digest, size, err := digestReceiptSnapshotSpool(spool)
	if err != nil {
		return nil, err
	}
	if size > c.config.Limits.MaxTotalBytes {
		return nil, receiptSnapshotCollectError("canonical spool exceeds total byte limit")
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("backup: rewind receipt Snapshot spool: %w", err)
	}
	stage, err := c.stageReceiptSnapshot(ctx, capture)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := spool.Chmod(0o400); err != nil {
		return nil, fmt.Errorf("backup: protect receipt Snapshot spool: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("backup: rewind staged receipt Snapshot spool: %w", err)
	}

	candidate := &ReceiptSnapshotCandidate{
		spool: spool,
		path:  spoolPath,
		metadata: ReceiptSnapshotCandidateMetadata{
			Header:      wireHeader,
			Policy:      capture.Policy,
			SpoolSHA256: digest,
			SpoolBytes:  size,
		},
		stage:          stage,
		frameCount:     frameCount,
		limits:         c.config.Limits,
		expectedPolicy: c.config.ExpectedPolicy,
		retiredConfig:  retiredConfig,
	}
	keepSpool = true
	return candidate, nil
}

func receiptSnapshotFrameBytes(
	stream ReceiptSnapshotStream,
	frame *pb.SnapshotResponse,
) ([]byte, error) {
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
	if err != nil {
		return nil, receiptSnapshotCollectError("marshal frame: %v", err)
	}
	if exact, ok := stream.(ReceiptSnapshotWireStream); ok {
		raw := bytes.Clone(exact.ReceiptSnapshotWireBytes())
		if len(raw) == 0 {
			return nil, receiptSnapshotCollectError("wire stream omitted current frame bytes")
		}
		decoded := &pb.SnapshotResponse{}
		if err := validateReceiptSnapshotFrameWire(raw); err != nil {
			return nil, receiptSnapshotCollectError("invalid frame wire: %v", err)
		}
		if err := proto.Unmarshal(raw, decoded); err != nil || !proto.Equal(decoded, frame) {
			return nil, receiptSnapshotCollectError("wire frame differs from decoded message")
		}
		if !bytes.Equal(raw, canonical) {
			return nil, receiptSnapshotCollectError("wire frame is not canonically encoded")
		}
	}
	if err := validateReceiptSnapshotFrameWire(canonical); err != nil {
		return nil, receiptSnapshotCollectError("invalid frame wire: %v", err)
	}
	return canonical, nil
}

type receiptSnapshotCollectState struct {
	sawHeader       bool
	sawFooter       bool
	graphStarted    bool
	activeEpoch     mutationreceipt.Epoch
	retiredEpochs   map[mutationreceipt.Epoch]struct{}
	activeReceipts  uint64
	retiredReceipts uint64
	origins         uint64
	graphFrames     uint64
	previousReceipt mutationreceipt.ID
	havePreviousRow bool
}

func (c *ReceiptSnapshotCollector) acceptReceiptSnapshotFrame(
	frame *pb.SnapshotResponse,
	frameIndex uint64,
	state *receiptSnapshotCollectState,
) error {
	if state.sawFooter {
		return receiptSnapshotCollectError("trailing frame after footer")
	}
	if header := frame.GetHeader(); header != nil {
		if frameIndex != 0 || state.sawHeader {
			return receiptSnapshotCollectError("duplicate or out-of-order header")
		}
		if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V2 {
			return receiptSnapshotCollectError("Snapshot format downgrade")
		}
		metadata := header.GetReceiptMetadata()
		if metadata == nil || metadata.GetActivePolicy() == nil {
			return receiptSnapshotCollectError("receipt metadata is missing")
		}
		state.origins = uint64(len(metadata.GetOriginCutoffs()))
		if state.origins > c.config.Limits.MaxOrigins {
			return receiptSnapshotCollectError("origin count exceeds limit")
		}
		if uint64(len(metadata.GetRetiredPolicies())) > c.config.Limits.MaxRetiredEpochs {
			return receiptSnapshotCollectError("retired epoch count exceeds limit")
		}
		if !snapshotCollectorEpoch(metadata.GetActivePolicy().GetDeploymentEpoch(), &state.activeEpoch) {
			return receiptSnapshotCollectError("active receipt epoch is invalid")
		}
		state.retiredEpochs = make(map[mutationreceipt.Epoch]struct{}, len(metadata.GetRetiredPolicies()))
		for _, policy := range metadata.GetRetiredPolicies() {
			var epoch mutationreceipt.Epoch
			if !snapshotCollectorEpoch(policy.GetDeploymentEpoch(), &epoch) ||
				epoch == state.activeEpoch {
				return receiptSnapshotCollectError("retired receipt epoch is invalid")
			}
			if _, duplicate := state.retiredEpochs[epoch]; duplicate {
				return receiptSnapshotCollectError("duplicate retired receipt epoch")
			}
			state.retiredEpochs[epoch] = struct{}{}
		}
		state.sawHeader = true
		return nil
	}
	if !state.sawHeader {
		return receiptSnapshotCollectError("body or footer before header")
	}
	if frame.GetFooter() != nil {
		state.sawFooter = true
		return nil
	}
	if row := frame.GetReceipt(); row != nil {
		if state.graphStarted {
			return receiptSnapshotCollectError("receipt row follows graph data")
		}
		id, err := mutationreceipt.DecodeID(row.GetOperationId())
		if err != nil {
			return receiptSnapshotCollectError("receipt operation ID is invalid")
		}
		if state.havePreviousRow && bytes.Compare(state.previousReceipt[:], id[:]) >= 0 {
			return receiptSnapshotCollectError("receipt rows are not in strict epoch and ID order")
		}
		state.previousReceipt = id
		state.havePreviousRow = true
		epoch := snapshotCollectorReceiptEpoch(id)
		if epoch == state.activeEpoch {
			if state.activeReceipts == c.config.Limits.MaxActiveReceipts {
				return receiptSnapshotCollectError("active receipt count exceeds limit")
			}
			state.activeReceipts++
			return nil
		}
		if _, declared := state.retiredEpochs[epoch]; !declared {
			return receiptSnapshotCollectError("receipt row belongs to an undeclared epoch")
		}
		if state.retiredReceipts == c.config.Limits.MaxRetiredReceipts {
			return receiptSnapshotCollectError("retired receipt count exceeds limit")
		}
		state.retiredReceipts++
		return nil
	}
	state.graphStarted = true
	if state.graphFrames == c.config.Limits.MaxGraphFrames {
		return receiptSnapshotCollectError("graph frame count exceeds limit")
	}
	state.graphFrames++
	return nil
}

func snapshotCollectorEpoch(raw []byte, epoch *mutationreceipt.Epoch) bool {
	if len(raw) != len(*epoch) {
		return false
	}
	copy(epoch[:], raw)
	return *epoch != (mutationreceipt.Epoch{})
}

func snapshotCollectorReceiptEpoch(id mutationreceipt.ID) mutationreceipt.Epoch {
	var epoch mutationreceipt.Epoch
	raw := id.Bytes()
	copy(epoch[:], raw[1:17])
	return epoch
}

func writeReceiptSnapshotSpool(file *os.File, chunks ...[]byte) error {
	for _, chunk := range chunks {
		for len(chunk) != 0 {
			n, err := file.Write(chunk)
			if err != nil {
				return fmt.Errorf("backup: write receipt Snapshot spool: %w", err)
			}
			if n == 0 {
				return fmt.Errorf("backup: write receipt Snapshot spool: %w", io.ErrShortWrite)
			}
			chunk = chunk[n:]
		}
	}
	return nil
}

func readReceiptSnapshotSpool(
	ctx context.Context,
	file *os.File,
	frameCount uint64,
	limits ReceiptSnapshotCollectorLimits,
) ([]*pb.SnapshotResponse, error) {
	if frameCount > uint64(^uint(0)>>1) {
		return nil, receiptSnapshotCollectError("frame count is not representable")
	}
	frames := make([]*pb.SnapshotResponse, 0, int(frameCount))
	var total uint64
	for range frameCount {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var prefix [4]byte
		if _, err := io.ReadFull(file, prefix[:]); err != nil {
			return nil, receiptSnapshotCollectError("truncated frame prefix: %v", err)
		}
		size := uint64(binary.BigEndian.Uint32(prefix[:]))
		if size == 0 || size > limits.MaxFrameBytes || size+4 > limits.MaxTotalBytes-total {
			return nil, receiptSnapshotCollectError("invalid spooled frame length")
		}
		raw := make([]byte, int(size))
		if _, err := io.ReadFull(file, raw); err != nil {
			return nil, receiptSnapshotCollectError("truncated frame: %v", err)
		}
		if err := validateReceiptSnapshotFrameWire(raw); err != nil {
			return nil, receiptSnapshotCollectError("invalid spooled frame wire: %v", err)
		}
		frame := &pb.SnapshotResponse{}
		if err := proto.Unmarshal(raw, frame); err != nil {
			return nil, receiptSnapshotCollectError("decode spooled frame: %v", err)
		}
		if err := service.ValidateReceiptSnapshotFrame(frame); err != nil {
			return nil, receiptSnapshotCollectError("%v", err)
		}
		frames = append(frames, frame)
		total += size + 4
	}
	var trailing [1]byte
	if n, err := file.Read(trailing[:]); err != io.EOF || n != 0 {
		if err != nil {
			return nil, receiptSnapshotCollectError("read trailing spool bytes: %v", err)
		}
		return nil, receiptSnapshotCollectError("trailing spool bytes")
	}
	return frames, nil
}

func validateReceiptSnapshotFrameWire(raw []byte) error {
	return validateArchiveMessageWire(
		raw,
		(&pb.SnapshotResponse{}).ProtoReflect().Descriptor(),
		0,
	)
}

func (c *ReceiptSnapshotCollector) stageReceiptSnapshot(
	ctx context.Context,
	capture service.ReceiptWholeStateCapture,
) (*receiptSnapshotV2Stage, error) {
	stageFile, err := os.CreateTemp(c.config.TempDir, ".lantern-receipt-snapshot-*.stage")
	if err != nil {
		return nil, fmt.Errorf("backup: create receipt Snapshot stage: %w", err)
	}
	stagePath := stageFile.Name()
	defer func() {
		_ = stageFile.Close()
		_ = os.Remove(stagePath)
	}()
	archive := wholeStateArchive{
		Graph: capture.Graph, Receipts: capture.Receipts,
		Policy: capture.Policy, Origins: capture.Origins,
	}
	if err := encodeWholeStateArchive(stageFile, archive); err != nil {
		return nil, err
	}
	if _, err := stageFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("backup: rewind receipt Snapshot stage: %w", err)
	}
	wholeState, err := stageReceiptWholeStateArchive(
		ctx,
		stageFile,
		c.config.ExpectedPolicy,
		c.config.DefaultTTL,
		c.config.ConfigureGraph,
	)
	if err != nil {
		return nil, err
	}
	retiredConfig := c.config.ExpectedRetiredConfig
	retiredConfig.ClockHighWater = capture.Receipts.ClockHighWater()
	retired, err := mutationreceipt.NewRetiredCatalogFromSnapshot(retiredConfig, capture.Retired)
	if err != nil {
		return nil, fmt.Errorf("backup: stage retired receipt catalog: %w", err)
	}
	canonical, err := retired.Snapshot(capture.Receipts.ClockHighWater())
	if err != nil {
		return nil, fmt.Errorf("backup: snapshot staged retired receipt catalog: %w", err)
	}
	if !reflect.DeepEqual(canonical, capture.Retired) {
		return nil, receiptSnapshotCollectError("staged retired receipt catalog is not lossless")
	}
	return &receiptSnapshotV2Stage{
		receiptWholeStateStage: wholeState,
		retired:                retired,
	}, nil
}

func digestReceiptSnapshotSpool(file *os.File) ([sha256.Size]byte, uint64, error) {
	var digest [sha256.Size]byte
	info, err := file.Stat()
	if err != nil {
		return digest, 0, fmt.Errorf("backup: stat receipt Snapshot spool: %w", err)
	}
	if info.Size() < 0 || uint64(info.Size()) > wholeStateArchiveMaxBytes {
		return digest, 0, receiptSnapshotCollectError("canonical spool has invalid size")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return digest, 0, fmt.Errorf("backup: rewind receipt Snapshot spool: %w", err)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return digest, 0, fmt.Errorf("backup: hash receipt Snapshot spool: %w", err)
	}
	if n != info.Size() {
		return digest, 0, receiptSnapshotCollectError("canonical spool size changed while hashing")
	}
	copy(digest[:], hash.Sum(nil))
	return digest, uint64(n), nil
}

// Metadata returns an owned snapshot of the candidate's immutable cut.
func (c *ReceiptSnapshotCandidate) Metadata() ReceiptSnapshotCandidateMetadata {
	if c == nil {
		return ReceiptSnapshotCandidateMetadata{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	metadata := c.metadata
	if metadata.Header != nil {
		metadata.Header = proto.Clone(metadata.Header).(*pb.SnapshotHeader)
	}
	return metadata
}

// WriteSpool copies the canonical deterministic V2 frame spool without
// exposing its temporary path. The candidate remains reusable until Close.
func (c *ReceiptSnapshotCandidate) WriteSpool(w io.Writer) error {
	if c == nil || w == nil {
		return errors.New("backup: receipt Snapshot candidate or writer is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.spool == nil {
		return errors.New("backup: receipt Snapshot candidate is closed")
	}
	if _, err := c.spool.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup: rewind receipt Snapshot candidate: %w", err)
	}
	n, err := io.CopyN(w, c.spool, int64(c.metadata.SpoolBytes))
	if err != nil {
		return fmt.Errorf("backup: copy receipt Snapshot candidate: %w", err)
	}
	if uint64(n) != c.metadata.SpoolBytes {
		return fmt.Errorf("backup: copy receipt Snapshot candidate: %w", io.ErrShortWrite)
	}
	_, seekErr := c.spool.Seek(0, io.SeekStart)
	if seekErr != nil {
		return fmt.Errorf("backup: rewind copied receipt Snapshot candidate: %w", seekErr)
	}
	return nil
}

// Close discards the detached candidate and removes its temporary spool.
func (c *ReceiptSnapshotCandidate) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var err error
	if c.spool != nil {
		err = c.spool.Close()
		c.spool = nil
	}
	if c.path != "" {
		if removeErr := os.Remove(c.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
		c.path = ""
	}
	c.stage = nil
	return err
}
