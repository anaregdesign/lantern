package replication

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

const graphSnapshotMaxBytes = 64 << 20

// These are independent retained-data bounds, not a whole-heap measurement.
// A runtime owns one shared installer/flight. The frame index, decoded current
// frame, validation maps and normal graph storage are outside the encoded quota.
type graphSnapshotStageLimits struct {
	maxFrameBytes    int
	maxBytes         uint64
	maxFrames        int
	maxIdentityBytes uint64
	maxOrigins       int
}

func defaultGraphSnapshotStageLimits() graphSnapshotStageLimits {
	return graphSnapshotStageLimits{
		maxFrameBytes: defaultSnapshotMaxFrameBytes, maxBytes: graphSnapshotMaxBytes,
		maxFrames: 1 << 20, maxIdentityBytes: 64 << 20, maxOrigins: 1 << 16,
	}
}

func (i *graphOnlySnapshotInstaller) SnapshotTransportLimits() SnapshotTransportLimits {
	return SnapshotTransportLimits{MaxFrameBytes: i.limits.maxFrameBytes, MaxStreamBytes: i.limits.maxBytes}
}

func graphSnapshotLimitError(dimension string) error {
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("Snapshot staging %s limit exceeded", dimension))
}

// retainSnapshotIdentity bounds both key material and per-map-entry metadata.
// The fixed charge is deliberately conservative accounting, not Go heap size.
func (i *graphOnlySnapshotInstaller) retainSnapshotIdentity(used *uint64, tail, head string) error {
	charge := uint64(len(tail)) + uint64(len(head)) + 128
	if charge > i.limits.maxIdentityBytes-*used {
		return graphSnapshotLimitError("identity metadata")
	}
	*used += charge
	return nil
}

// Install validates the complete remote stream, including its final status,
// while the serving publication remains intact. Only the immutable staged
// image can enter the existing fail-closed live replay. No worker or new
// deadline is created; cancellation/shutdown joins through the caller.
func (i *graphOnlySnapshotInstaller) Install(ctx context.Context, stream SnapshotStream) (SnapshotInstallResult, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotInstallResult{}, err
	}
	if !i.stageMu.TryLock() {
		return SnapshotInstallResult{}, connect.NewError(connect.CodeUnavailable, fmt.Errorf("Snapshot staging already in progress"))
	}
	defer i.stageMu.Unlock()
	stage := &graphSnapshotStage{ctx: ctx, source: stream, limits: i.limits}
	defer stage.release()
	if _, err := i.replaySnapshot(ctx, stage, false); err != nil {
		return SnapshotInstallResult{}, err
	}
	if err := checkSnapshotContext(ctx); err != nil {
		return SnapshotInstallResult{}, err
	}
	stage.source, stage.current, stage.failure = nil, nil, nil
	stage.replaying = true
	return i.replaySnapshot(ctx, stage, true)
}

// graphSnapshotStage retains canonical encoded frames only. No live graph,
// receipt state, generation or search index is touched during collection.
// Both passes run the same semantic validator. Replay drops each consumed
// encoded frame; release discards all remaining references on every exit.
type graphSnapshotStage struct {
	ctx       context.Context
	source    SnapshotStream
	limits    graphSnapshotStageLimits
	frames    [][]byte
	bytes     uint64
	index     int
	replaying bool
	current   *pb.SnapshotResponse
	failure   error
}

func (s *graphSnapshotStage) Receive() bool {
	s.current = nil
	if s.failure != nil {
		return false
	}
	// Before the transport's first Receive, its stored Do error may be the
	// only result. Preserve that error rather than masking it as absent native
	// evidence. No source frame is retained until the strict check below.
	s.failure = s.ctx.Err()
	if s.failure == nil && s.replaying {
		s.failure = checkSnapshotContext(s.ctx)
	}
	if s.failure != nil {
		return false
	}
	if s.replaying {
		if s.index == len(s.frames) {
			return false
		}
		raw := s.frames[s.index]
		s.frames[s.index] = nil
		s.index++
		s.bytes -= uint64(len(raw))
		frame := new(pb.SnapshotResponse)
		if s.failure = proto.Unmarshal(raw, frame); s.failure != nil {
			return false
		}
		s.current = frame
		return true
	}
	if !s.source.Receive() {
		s.failure = s.source.Err()
		if err := s.ctx.Err(); err != nil {
			s.failure = err
		} else if s.failure == nil || errors.Is(s.failure, io.EOF) {
			if err := checkSnapshotContext(s.ctx); err != nil {
				s.failure = err
			}
		}
		return false
	}
	if s.failure = checkSnapshotContext(s.ctx); s.failure != nil {
		return false
	}
	frame := s.source.Msg()
	if len(s.frames) >= s.limits.maxFrames {
		s.failure = graphSnapshotLimitError("frame count")
		return false
	}
	size := proto.Size(frame)
	if size > s.limits.maxFrameBytes || uint64(size) > s.limits.maxBytes-s.bytes {
		s.failure = graphSnapshotLimitError("encoded bytes")
		return false
	}
	// Origin cardinality is checked before a Header clone or retained encoding.
	if len(frame.GetHeader().GetCutoffSeqPerOrigin()) > s.limits.maxOrigins {
		s.failure = graphSnapshotLimitError("origin count")
		return false
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
	if err != nil {
		s.failure = err
		return false
	}
	if len(raw) > s.limits.maxFrameBytes || uint64(len(raw)) > s.limits.maxBytes-s.bytes {
		s.failure = graphSnapshotLimitError("encoded bytes")
		return false
	}
	s.frames = append(s.frames, raw)
	s.bytes += uint64(len(raw))
	s.current = frame
	return true
}

func (s *graphSnapshotStage) Msg() *pb.SnapshotResponse { return s.current }
func (s *graphSnapshotStage) Err() error                { return s.failure }

func (s *graphSnapshotStage) release() {
	s.source, s.current, s.frames = nil, nil, nil
	s.bytes = 0
}
