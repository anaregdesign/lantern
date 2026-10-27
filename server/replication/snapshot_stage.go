package replication

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func (i *graphOnlySnapshotInstaller) SnapshotTransportLimits() SnapshotTransportLimits {
	return SnapshotTransportLimits{MaxFrameBytes: i.spool.MaxFrameBytes, MaxStreamBytes: i.spool.MaxDiskBytes}
}

func graphSnapshotLimitError(dimension string) error {
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("Snapshot staging %s limit exceeded", dimension))
}

// Install owns one private disk flight, including its exact identity index and
// merge workspace. Complete remote EOF/final status, semantic validation and a
// sealed canonical-spool digest precede any live publication transition. Every
// pass retains the original native admission and caller deadline.
func (i *graphOnlySnapshotInstaller) Install(ctx context.Context, stream SnapshotStream) (result SnapshotInstallResult, err error) {
	diagnostic := newSnapshotInstallDiagnostic(time.Now())
	// Registered first: final cleanup errors and scalar counts survive all
	// scratch-release defers before one immutable failure envelope is returned.
	defer func() {
		if err != nil {
			err = diagnostic.failure(err, time.Now(), snapshotAdmissionDiagnostic(ctx))
		}
	}()
	if err := ctx.Err(); err != nil {
		return SnapshotInstallResult{}, err
	}
	if i.configurationError != nil {
		return SnapshotInstallResult{}, connect.NewError(connect.CodeFailedPrecondition, i.configurationError)
	}
	diagnostic.enter("flight_admission")
	if !i.stageMu.TryLock() {
		return SnapshotInstallResult{}, connect.NewError(connect.CodeUnavailable, errors.New("Snapshot staging already in progress"))
	}
	defer i.stageMu.Unlock()
	diagnostic.enter("scratch_setup")
	scratch, err := i.newScratch(ctx, i.spool)
	if err != nil {
		return SnapshotInstallResult{}, err
	}
	defer func() {
		if closeErr := scratch.Close(); closeErr != nil {
			diagnostic.cleanupFailed = true
			err = errors.Join(err, fmt.Errorf("close Snapshot staging owner: %w", closeErr))
		}
	}()
	file, err := scratch.create()
	if err != nil {
		return SnapshotInstallResult{}, err
	}
	stage := &graphSnapshotStage{ctx: ctx, source: stream, maxFrameBytes: i.spool.MaxFrameBytes, file: file, digest: sha256.New(), complete: scratch.Close, diagnostic: diagnostic}
	defer stage.release()
	identities, err := newSnapshotIdentityIndex(ctx, scratch)
	if err != nil {
		return SnapshotInstallResult{}, err
	}
	diagnostic.enter("capture_and_validate")
	if _, err := i.replaySnapshot(ctx, stage, false, identities, nil); err != nil {
		return SnapshotInstallResult{}, err
	}
	diagnostic.enter("identity_index")
	if err := identities.Finish(); err != nil {
		return SnapshotInstallResult{}, err
	}
	diagnostic.enter("seal")
	if err := stage.seal(); err != nil {
		return SnapshotInstallResult{}, err
	}
	diagnostic.enter("live_begin")
	return i.replaySnapshot(ctx, stage, true, nil, diagnostic)
}

// graphSnapshotStage retains no dataset-sized frame slice. Each frame is
// encoded, length-prefixed and checksummed in the one owned spool. Replay holds
// one encoded/decoded frame at a time. The configured frame budget bounds that
// work, not the entire heap: protobuf amplification, per-frame contribution
// rows, the Header origin vector and the live graph remain separate costs.
type graphSnapshotStage struct {
	ctx           context.Context
	source        SnapshotStream
	maxFrameBytes int
	file          *snapshotScratchFile
	digest        hash.Hash
	bytes         uint64
	frameCount    uint64
	index         uint64
	replaying     bool
	current       *pb.SnapshotResponse
	failure       error
	complete      func() error
	diagnostic    *snapshotInstallDiagnostic
}

func (s *graphSnapshotStage) Receive() bool {
	s.current = nil
	if s.failure != nil {
		return false
	}
	// The transport's stored Do error may be its only result before the first
	// Receive. Do not replace it with missing native evidence at that boundary.
	s.failure = s.ctx.Err()
	if s.failure == nil && s.replaying {
		s.failure = checkSnapshotContext(s.ctx)
	}
	if s.failure != nil {
		return false
	}
	if s.replaying {
		return s.receiveStaged()
	}
	if !s.source.Receive() {
		s.failure = s.source.Err()
		if err := s.ctx.Err(); err != nil {
			s.failure = err
		} else if s.failure == nil || errors.Is(s.failure, io.EOF) {
			s.failure = checkSnapshotContext(s.ctx)
		}
		return false
	}
	if s.failure = checkSnapshotContext(s.ctx); s.failure != nil {
		return false
	}
	frame := s.source.Msg()
	if proto.Size(frame) > s.maxFrameBytes {
		s.failure = graphSnapshotLimitError("frame bytes")
		return false
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
	if err != nil {
		s.failure = err
		return false
	}
	if len(raw) > s.maxFrameBytes {
		s.failure = graphSnapshotLimitError("frame bytes")
		return false
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(raw)))
	checksum := sha256.Sum256(raw)
	for _, chunk := range [][]byte{size[:], raw, checksum[:]} {
		if s.failure = checkSnapshotContext(s.ctx); s.failure != nil {
			return false
		}
		n, err := s.file.Write(chunk)
		if err == nil && n != len(chunk) {
			err = io.ErrShortWrite
		}
		if err != nil {
			s.failure = err
			return false
		}
		_, _ = s.digest.Write(chunk)
		s.bytes += uint64(n)
	}
	s.frameCount++
	if s.diagnostic != nil {
		s.diagnostic.stagedFrames, s.diagnostic.stagedBytes = s.frameCount, s.bytes
	}
	s.current = frame
	return true
}

func (s *graphSnapshotStage) receiveStaged() bool {
	if s.file == nil {
		return false
	}
	if s.index == s.frameCount {
		// All payload/index files and the owner lease/directory must close
		// before verified finish. Original admission is checked again after
		// this potentially blocking cleanup by the live validator.
		s.failure = s.file.Close()
		s.file = nil
		if s.failure == nil && s.complete != nil {
			s.failure = s.complete()
		}
		if s.failure != nil && s.diagnostic != nil {
			s.diagnostic.cleanupFailed = true
		}
		return false
	}
	var size [8]byte
	if _, s.failure = io.ReadFull(s.file, size[:]); s.failure != nil {
		return false
	}
	length := binary.BigEndian.Uint64(size[:])
	if length > uint64(s.maxFrameBytes) || length > s.bytes {
		s.failure = snapshotProtocolError("invalid staged frame length")
		return false
	}
	raw := make([]byte, int(length))
	if _, s.failure = io.ReadFull(s.file, raw); s.failure != nil {
		return false
	}
	var want [sha256.Size]byte
	if _, s.failure = io.ReadFull(s.file, want[:]); s.failure != nil {
		return false
	}
	if sha256.Sum256(raw) != want {
		s.failure = snapshotProtocolError("staged frame digest mismatch")
		return false
	}
	if s.failure = checkSnapshotContext(s.ctx); s.failure != nil {
		return false
	}
	frame := new(pb.SnapshotResponse)
	if s.failure = proto.Unmarshal(raw, frame); s.failure != nil {
		return false
	}
	s.index++
	s.current = frame
	return true
}

// seal verifies the full canonical spool before the first live Begin. Files
// have no directory link and their only writer is this owner; the source never
// receives a handle. Per-frame digests additionally check each replay read.
func (s *graphSnapshotStage) seal() error {
	if err := checkSnapshotContext(s.ctx); err != nil {
		return err
	}
	if s.failure != nil || s.file.Size() != s.bytes {
		return snapshotProtocolError("incomplete canonical spool")
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	verified := sha256.New()
	var buffer [64 << 10]byte
	var read uint64
	for {
		if err := checkSnapshotContext(s.ctx); err != nil {
			return err
		}
		n, err := s.file.Read(buffer[:])
		read += uint64(n)
		_, _ = verified.Write(buffer[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if read != s.bytes || !equalSnapshotDigest(verified.Sum(nil), s.digest.Sum(nil)) {
		return snapshotProtocolError("canonical spool digest mismatch")
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := checkSnapshotContext(s.ctx); err != nil {
		return err
	}
	s.source, s.current, s.failure, s.digest = nil, nil, nil, nil
	s.replaying = true
	return nil
}

func equalSnapshotDigest(a, b []byte) bool {
	if len(a) != sha256.Size || len(b) != sha256.Size {
		return false
	}
	var mismatch byte
	for index := range a {
		mismatch |= a[index] ^ b[index]
	}
	return mismatch == 0
}

func (s *graphSnapshotStage) Msg() *pb.SnapshotResponse { return s.current }
func (s *graphSnapshotStage) Err() error                { return s.failure }

func (s *graphSnapshotStage) release() {
	s.source, s.current, s.digest, s.complete = nil, nil, nil, nil
	if s.file != nil {
		_ = s.file.Close() // The enclosing scratch owner retries/reports cleanup.
		s.file = nil
	}
	s.bytes, s.frameCount = 0, 0
}
