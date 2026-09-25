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
	MaxFrameBytes  uint64
	MaxFrames      uint64
	MaxTotalBytes  uint64
	MaxReceipts    uint64
	MaxOrigins     uint64
	MaxGraphFrames uint64
}

// ReceiptSnapshotCollectorConfig describes one unwired collector/stager.
// TempDir owns only per-call temporary files; ExpectedPolicy is the local
// immutable epoch policy the candidate must match.
type ReceiptSnapshotCollectorConfig struct {
	TempDir        string
	Limits         ReceiptSnapshotCollectorLimits
	ExpectedPolicy mutationreceipt.Config
	DefaultTTL     time.Duration
	ConfigureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error
}

// ReceiptSnapshotCollector consumes RECEIPT_V1 streams into detached state.
// It has no reference to a serving graph, Store, origin tracker, HLC, WAL,
// generation, or subscriber and therefore cannot publish a candidate.
type ReceiptSnapshotCollector struct {
	config ReceiptSnapshotCollectorConfig
}

// ReceiptSnapshotCandidateMetadata is safe to inspect before a later atomic
// installer. Header is cloned on return and includes the complete wire receipt
// metadata and origin vector.
type ReceiptSnapshotCandidateMetadata struct {
	Header        *pb.SnapshotHeader
	Policy        mutationreceipt.Config
	ArchiveSHA256 [sha256.Size]byte
	ArchiveBytes  uint64
}

// ReceiptSnapshotCandidate owns one validated canonical archive temp file and
// one unpublished staged graph/Store cut. Close removes the temporary file.
// The private stage is intentionally unavailable to production callers until
// a separate atomic installer consumes it inside this package.
type ReceiptSnapshotCandidate struct {
	mu       sync.Mutex
	archive  *os.File
	path     string
	metadata ReceiptSnapshotCandidateMetadata
	stage    *receiptWholeStateStage
	closed   bool
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
	if limits.MaxFrameBytes == 0 || limits.MaxFrameBytes > wholeStateArchiveMaxFrame ||
		limits.MaxFrames < 2 ||
		limits.MaxTotalBytes == 0 || limits.MaxTotalBytes > wholeStateArchiveMaxBytes ||
		limits.MaxReceipts == 0 || limits.MaxOrigins == 0 || limits.MaxGraphFrames == 0 ||
		limits.MaxReceipts > limits.MaxFrames || limits.MaxGraphFrames > limits.MaxFrames {
		return nil, errors.New("backup: receipt Snapshot collector limits are invalid")
	}
	if _, err := mutationreceipt.New(config.ExpectedPolicy); err != nil {
		return nil, fmt.Errorf("backup: receipt Snapshot expected policy: %w", err)
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
// breach, archive failure, or detached graph/Store staging failure.
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
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spoolPath)
	}()

	var (
		frameCount uint64
		totalBytes uint64
		receipts   uint64
		origins    uint64
		graph      uint64
		sawHeader  bool
		sawFooter  bool
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
		if err := c.acceptReceiptSnapshotFrame(
			frame, frameCount, &sawHeader, &sawFooter, &receipts, &origins, &graph,
		); err != nil {
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
	if !sawHeader || !sawFooter {
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
	capture, err := service.DecodeReceiptSnapshotFrames(frames)
	if err != nil {
		return nil, receiptSnapshotCollectError("%v", err)
	}
	if uint64(len(capture.Receipts.Receipts)) > c.config.Limits.MaxReceipts ||
		uint64(len(capture.Origins)) > c.config.Limits.MaxOrigins ||
		uint64(len(capture.Graph)-2) > c.config.Limits.MaxGraphFrames {
		return nil, receiptSnapshotCollectError("decoded section count exceeds limit")
	}

	archiveFile, err := os.CreateTemp(c.config.TempDir, ".lantern-receipt-snapshot-*.archive")
	if err != nil {
		return nil, fmt.Errorf("backup: create receipt Snapshot archive: %w", err)
	}
	archivePath := archiveFile.Name()
	keepArchive := false
	defer func() {
		if !keepArchive {
			_ = archiveFile.Close()
			_ = os.Remove(archivePath)
		}
	}()
	archive := wholeStateArchive{
		Graph: capture.Graph, Receipts: capture.Receipts,
		Policy: capture.Policy, Origins: capture.Origins,
	}
	if err := encodeWholeStateArchive(archiveFile, archive); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest, size, err := digestReceiptSnapshotArchive(archiveFile)
	if err != nil {
		return nil, err
	}
	if size > c.config.Limits.MaxTotalBytes {
		return nil, receiptSnapshotCollectError("canonical archive exceeds total byte limit")
	}
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("backup: rewind receipt Snapshot archive: %w", err)
	}
	stage, err := stageReceiptWholeStateArchive(
		ctx,
		archiveFile,
		c.config.ExpectedPolicy,
		c.config.DefaultTTL,
		c.config.ConfigureGraph,
	)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := archiveFile.Chmod(0o400); err != nil {
		return nil, fmt.Errorf("backup: protect receipt Snapshot archive: %w", err)
	}
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("backup: rewind staged receipt Snapshot archive: %w", err)
	}

	candidate := &ReceiptSnapshotCandidate{
		archive: archiveFile,
		path:    archivePath,
		metadata: ReceiptSnapshotCandidateMetadata{
			Header:        wireHeader,
			Policy:        capture.Policy,
			ArchiveSHA256: digest,
			ArchiveBytes:  size,
		},
		stage: stage,
	}
	keepArchive = true
	return candidate, nil
}

func receiptSnapshotFrameBytes(
	stream ReceiptSnapshotStream,
	frame *pb.SnapshotResponse,
) ([]byte, error) {
	var raw []byte
	if exact, ok := stream.(ReceiptSnapshotWireStream); ok {
		raw = bytes.Clone(exact.ReceiptSnapshotWireBytes())
		if len(raw) == 0 {
			return nil, receiptSnapshotCollectError("wire stream omitted current frame bytes")
		}
		decoded := &pb.SnapshotResponse{}
		if err := validateArchiveGraphFrameWire(raw); err != nil {
			return nil, receiptSnapshotCollectError("invalid frame wire: %v", err)
		}
		if err := proto.Unmarshal(raw, decoded); err != nil || !proto.Equal(decoded, frame) {
			return nil, receiptSnapshotCollectError("wire frame differs from decoded message")
		}
		return raw, nil
	}
	var err error
	raw, err = (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
	if err != nil {
		return nil, receiptSnapshotCollectError("marshal frame: %v", err)
	}
	if err := validateArchiveGraphFrameWire(raw); err != nil {
		return nil, receiptSnapshotCollectError("invalid frame wire: %v", err)
	}
	return raw, nil
}

func (c *ReceiptSnapshotCollector) acceptReceiptSnapshotFrame(
	frame *pb.SnapshotResponse,
	frameIndex uint64,
	sawHeader, sawFooter *bool,
	receipts, origins, graph *uint64,
) error {
	if *sawFooter {
		return receiptSnapshotCollectError("trailing frame after footer")
	}
	if header := frame.GetHeader(); header != nil {
		if frameIndex != 0 || *sawHeader {
			return receiptSnapshotCollectError("duplicate or out-of-order header")
		}
		if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 {
			return receiptSnapshotCollectError("Snapshot format downgrade")
		}
		if header.GetReceiptMetadata() == nil || header.GetReceiptMetadata().GetPolicy() == nil {
			return receiptSnapshotCollectError("receipt metadata is missing")
		}
		*origins = uint64(len(header.GetReceiptMetadata().GetOriginCutoffs()))
		if *origins > c.config.Limits.MaxOrigins {
			return receiptSnapshotCollectError("origin count exceeds limit")
		}
		*sawHeader = true
		return nil
	}
	if !*sawHeader {
		return receiptSnapshotCollectError("body or footer before header")
	}
	if frame.GetFooter() != nil {
		*sawFooter = true
		return nil
	}
	if frame.GetReceipt() != nil {
		if *receipts == c.config.Limits.MaxReceipts {
			return receiptSnapshotCollectError("receipt count exceeds limit")
		}
		(*receipts)++
		return nil
	}
	if *graph == c.config.Limits.MaxGraphFrames {
		return receiptSnapshotCollectError("graph frame count exceeds limit")
	}
	(*graph)++
	return nil
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
		if err := validateArchiveGraphFrameWire(raw); err != nil {
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

func digestReceiptSnapshotArchive(file *os.File) ([sha256.Size]byte, uint64, error) {
	var digest [sha256.Size]byte
	info, err := file.Stat()
	if err != nil {
		return digest, 0, fmt.Errorf("backup: stat receipt Snapshot archive: %w", err)
	}
	if info.Size() < 0 || uint64(info.Size()) > wholeStateArchiveMaxBytes {
		return digest, 0, receiptSnapshotCollectError("canonical archive has invalid size")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return digest, 0, fmt.Errorf("backup: rewind receipt Snapshot archive: %w", err)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return digest, 0, fmt.Errorf("backup: hash receipt Snapshot archive: %w", err)
	}
	if n != info.Size() {
		return digest, 0, receiptSnapshotCollectError("canonical archive size changed while hashing")
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

// WriteArchive copies the canonical deterministic archive without exposing
// its temporary path. The candidate remains reusable until Close.
func (c *ReceiptSnapshotCandidate) WriteArchive(w io.Writer) error {
	if c == nil || w == nil {
		return errors.New("backup: receipt Snapshot candidate or writer is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.archive == nil {
		return errors.New("backup: receipt Snapshot candidate is closed")
	}
	if _, err := c.archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup: rewind receipt Snapshot candidate: %w", err)
	}
	n, err := io.CopyN(w, c.archive, int64(c.metadata.ArchiveBytes))
	if err != nil {
		return fmt.Errorf("backup: copy receipt Snapshot candidate: %w", err)
	}
	if uint64(n) != c.metadata.ArchiveBytes {
		return fmt.Errorf("backup: copy receipt Snapshot candidate: %w", io.ErrShortWrite)
	}
	_, seekErr := c.archive.Seek(0, io.SeekStart)
	if seekErr != nil {
		return fmt.Errorf("backup: rewind copied receipt Snapshot candidate: %w", seekErr)
	}
	return nil
}

// Close discards the detached candidate and removes its temporary archive.
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
	if c.archive != nil {
		err = c.archive.Close()
		c.archive = nil
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
