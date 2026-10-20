package replication

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"google.golang.org/protobuf/proto"
)

func snapshotStageTestLimits(frames []*pb.SnapshotResponse) graphSnapshotStageLimits {
	limits := defaultGraphSnapshotStageLimits()
	limits.maxFrameBytes, limits.maxBytes = 0, 0
	limits.maxFrames = len(frames)
	for _, frame := range frames {
		size := proto.Size(frame)
		limits.maxFrameBytes = max(limits.maxFrameBytes, size)
		limits.maxBytes += uint64(size)
	}
	return limits
}

func snapshotStageTestFramesWithEdge() []*pb.SnapshotResponse {
	frames := graphInstallerLifecycleFrames()
	edge := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{
		Tail: "installed", Head: "other",
		Contributions: []*pb.SnapshotEdgeContribution{{Weight: 1}},
	}}}
	frames[len(frames)-1].GetFooter().EdgeCount = 1
	return append(frames[:len(frames)-1], edge, frames[len(frames)-1])
}

func TestGraphSnapshotStagingIndependentBudgetsPrecedeLiveAdmission(t *testing.T) {
	for _, dimension := range []string{"per frame", "total encoded bytes", "frame count", "origins", "identity metadata"} {
		t.Run(dimension, func(t *testing.T) {
			frames := graphInstallerLifecycleFrames()
			if dimension == "origins" {
				frames[0].GetHeader().CutoffSeqPerOrigin["origin-b"] = 8
			}
			if dimension == "identity metadata" {
				frames = snapshotStageTestFramesWithEdge()
			}
			exact := snapshotStageTestLimits(frames)
			exact.maxOrigins = len(frames[0].GetHeader().GetCutoffSeqPerOrigin())
			if dimension == "identity metadata" {
				exact.maxIdentityBytes = uint64(len("installed") + len("other") + 128)
			}
			limited := exact
			switch dimension {
			case "per frame":
				limited.maxFrameBytes--
			case "total encoded bytes":
				limited.maxBytes--
			case "frame count":
				limited.maxFrames--
			case "origins":
				limited.maxOrigins--
			case "identity metadata":
				limited.maxIdentityBytes--
			}
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			installer := newGraphOnlySnapshotInstaller(apply, graph).(*graphOnlySnapshotInstaller)
			installer.limits = limited
			if _, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: frames}); connect.CodeOf(err) != connect.CodeResourceExhausted {
				t.Fatalf("%s bound did not reject staged input: %v", dimension, err)
			}
			if len(events) != 0 || len(apply.finishes) != 0 || len(apply.cutoffs) != 0 {
				t.Fatalf("%s rejection touched live state: events=%v finishes=%v cutoffs=%v", dimension, events, apply.finishes, apply.cutoffs)
			}
			// The exact boundary is accepted and the failed attempt released
			// its staging flight, so the same installer can safely retry.
			installer.limits = exact
			result, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: frames})
			if err != nil || len(apply.finishes) != 1 || !apply.finishes[0] || result.Graph.Vertices != 1 {
				t.Fatalf("exact %s boundary or retry failed: result=%+v finishes=%v err=%v", dimension, result.Graph, apply.finishes, err)
			}
		})
	}
}

func TestGraphSnapshotStagingRejectsInvalidImageBeforeLiveAdmission(t *testing.T) {
	finalStatus := connect.NewError(connect.CodeFailedPrecondition, errors.New("source finish rejected Snapshot"))
	for _, invalid := range []string{"final server status after footer", "missing footer", "footer counts", "body after footer", "late invalid timestamp", "late nonfinite edge"} {
		t.Run(invalid, func(t *testing.T) {
			frames := graphInstallerLifecycleFrames()
			var streamErr error
			switch invalid {
			case "final server status after footer":
				streamErr = finalStatus
			case "missing footer":
				frames = frames[:len(frames)-1]
			case "footer counts":
				frames[len(frames)-1].GetFooter().VertexCount++
			case "body after footer":
				frames = append(frames, proto.Clone(frames[1]).(*pb.SnapshotResponse))
			case "late invalid timestamp":
				bad := proto.Clone(frames[1]).(*pb.SnapshotResponse)
				bad.GetVertex().Vertex.Value = &pb.Vertex_Timestamp{}
				frames[len(frames)-1].GetFooter().VertexCount++
				frames = append(frames[:len(frames)-1], bad, frames[len(frames)-1])
			case "late nonfinite edge":
				frames = snapshotStageTestFramesWithEdge()
				frames[len(frames)-2].GetEdge().Contributions[0].Weight = float32(math.NaN())
			}
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			installer := newGraphOnlySnapshotInstaller(apply, graph)
			result, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: frames, err: streamErr})
			if err == nil || result.Header != nil || result.Graph != (SnapshotGraphCounts{}) {
				t.Fatalf("invalid image returned an installed result: result=%+v err=%v", result, err)
			}
			if streamErr != nil && !errors.Is(err, streamErr) {
				t.Fatalf("source final status was replaced by local success: %v", err)
			}
			if len(events) != 0 || len(apply.finishes) != 0 || len(apply.cutoffs) != 0 {
				t.Fatalf("invalid staged image reached live state: events=%v finishes=%v cutoffs=%v", events, apply.finishes, apply.cutoffs)
			}
		})
	}
}

type snapshotStageObservedStream struct {
	SnapshotStream
	receives int
	after    func(bool)
}

func (s *snapshotStageObservedStream) Receive() bool {
	s.receives++
	open := s.SnapshotStream.Receive()
	if s.after != nil {
		s.after(open)
	}
	return open
}

type snapshotStageCancelApplier struct {
	*snapshotLifecycleMutationApplier
	cancel context.CancelFunc
}

func (a *snapshotStageCancelApplier) BeginSnapshotInstallChecked(ctx context.Context, check func() error) (func(bool) error, error) {
	finish, err := a.snapshotLifecycleMutationApplier.BeginSnapshotInstallChecked(ctx, check)
	if err == nil && a.cancel != nil {
		a.cancel()
	}
	return finish, err
}

type snapshotStageCancelGraph struct {
	*snapshotLifecycleGraph
	cancel context.CancelFunc
}

func (g *snapshotStageCancelGraph) PutVertexWithExpirationHLC(key string, vertex *pb.Vertex, expiration time.Time, stamp hlc.Timestamp) bool {
	applied := g.snapshotLifecycleGraph.PutVertexWithExpirationHLC(key, vertex, expiration, stamp)
	if g.cancel != nil {
		g.cancel()
	}
	return applied
}

func TestGraphSnapshotStagingCancellationKeepsLiveAdmissionBoundary(t *testing.T) {
	for _, phase := range []string{"before first Receive", "after remote EOF", "after live Begin", "after first live write"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var events []string
			apply := &snapshotStageCancelApplier{snapshotLifecycleMutationApplier: &snapshotLifecycleMutationApplier{events: &events}}
			graph := &snapshotStageCancelGraph{snapshotLifecycleGraph: &snapshotLifecycleGraph{events: &events}}
			stream := &snapshotStageObservedStream{SnapshotStream: &snapshotSliceStream{frames: graphInstallerLifecycleFrames()}}
			switch phase {
			case "before first Receive":
				cancel()
			case "after remote EOF":
				stream.after = func(open bool) {
					if !open {
						cancel()
					}
				}
			case "after live Begin":
				apply.cancel = cancel
			case "after first live write":
				graph.cancel = cancel
			}
			_, err := installSnapshot(ctx, newGraphOnlySnapshotInstaller(apply, graph), stream)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s cancellation was not preserved: events=%v err=%v", phase, events, err)
			}
			if phase == "before first Receive" || phase == "after remote EOF" {
				if len(events) != 0 || len(apply.finishes) != 0 {
					t.Fatalf("pre-install cancellation touched live state: events=%v finishes=%v", events, apply.finishes)
				}
				if phase == "before first Receive" && stream.receives != 0 {
					t.Fatal("canceled staging consumed its source stream")
				}
				return
			}
			if len(apply.finishes) != 1 || apply.finishes[0] || len(apply.cutoffs) != 0 || strings.Contains(strings.Join(events, ","), "finish:true") {
				t.Fatalf("interrupted live replay was verified or left unfinished: events=%v finishes=%v cutoffs=%v", events, apply.finishes, apply.cutoffs)
			}
			if phase == "after first live write" && !strings.Contains(strings.Join(events, ","), "vertex") {
				t.Fatal("live interruption control did not apply its first vertex")
			}
		})
	}
}

type snapshotStageBlockedStream struct {
	SnapshotStream
	ctx     context.Context
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
	failure error
}

func (s *snapshotStageBlockedStream) Receive() bool {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.ctx.Done():
		s.failure = s.ctx.Err()
		return false
	case <-s.release:
		return s.SnapshotStream.Receive()
	}
}

func (s *snapshotStageBlockedStream) Err() error {
	if s.failure != nil {
		return s.failure
	}
	return s.SnapshotStream.Err()
}

func TestGraphSnapshotStagingSingleFlightRejectsBeforeReceiveAndReleases(t *testing.T) {
	for _, completion := range []string{"success", "remote final error", "canceled collector"} {
		t.Run(completion, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			firstCtx, cancelFirst := context.WithCancel(ctx)
			defer cancelFirst()
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			installer := newGraphOnlySnapshotInstaller(apply, graph)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			first := &snapshotStageBlockedStream{
				SnapshotStream: &snapshotSliceStream{frames: graphInstallerLifecycleFrames()},
				ctx:            firstCtx, entered: make(chan struct{}), release: release,
			}
			finalErr := errors.New("controlled Snapshot final status failure")
			if completion == "remote final error" {
				first.SnapshotStream.(*snapshotSliceStream).err = finalErr
			}
			done := make(chan error, 1)
			go func() {
				_, err := installSnapshot(firstCtx, installer, first)
				done <- err
			}()
			joined := false
			defer func() {
				cancelFirst()
				unblock()
				if !joined {
					select {
					case <-done:
					case <-ctx.Done():
						t.Error("staging collector did not join", ctx.Err())
					}
				}
			}()
			select {
			case <-first.entered:
			case <-ctx.Done():
				t.Fatal("first staging flight did not enter its source", ctx.Err())
			}
			second := &snapshotStageObservedStream{SnapshotStream: &snapshotSliceStream{frames: graphInstallerLifecycleFrames()}}
			if _, err := installSnapshot(ctx, installer, second); connect.CodeOf(err) != connect.CodeUnavailable || second.receives != 0 || len(events) != 0 {
				t.Fatalf("competing staging consumed input or touched live state: receives=%d events=%v err=%v", second.receives, events, err)
			}
			if completion == "canceled collector" {
				cancelFirst()
			}
			unblock()
			select {
			case err := <-done:
				joined = true
				switch completion {
				case "success":
					if err != nil || len(apply.finishes) != 1 || !apply.finishes[0] {
						t.Fatalf("first flight did not install: finishes=%v err=%v", apply.finishes, err)
					}
				case "remote final error":
					if !errors.Is(err, finalErr) || len(events) != 0 {
						t.Fatalf("failed source reached live state: events=%v err=%v", events, err)
					}
				case "canceled collector":
					if !errors.Is(err, context.Canceled) || len(events) != 0 {
						t.Fatalf("canceled collection reached live state: events=%v err=%v", events, err)
					}
				}
			case <-ctx.Done():
				t.Fatal("first staging flight did not finish", ctx.Err())
			}
			before := len(apply.finishes)
			if _, err := installSnapshot(ctx, installer, &snapshotSliceStream{frames: graphInstallerLifecycleFrames()}); err != nil || len(apply.finishes) != before+1 || !apply.finishes[before] {
				t.Fatalf("completed flight retained its slot: finishes=%v err=%v", apply.finishes, err)
			}
		})
	}
}

type snapshotStageOwnershipGraph struct {
	*snapshotLifecycleGraph
	vertices []*pb.Vertex
}

func (g *snapshotStageOwnershipGraph) PutVertexWithExpirationHLC(key string, vertex *pb.Vertex, expiration time.Time, stamp hlc.Timestamp) bool {
	g.vertices = append(g.vertices, proto.Clone(vertex).(*pb.Vertex))
	return g.snapshotLifecycleGraph.PutVertexWithExpirationHLC(key, vertex, expiration, stamp)
}

func TestGraphSnapshotStagingReplaysOwnedBytesAfterSourceMutationAtEOF(t *testing.T) {
	for _, terminal := range []struct {
		name string
		err  error
	}{{name: "nil status"}, {name: "explicit io.EOF", err: io.EOF}} {
		t.Run(terminal.name, func(t *testing.T) {
			frames := graphInstallerLifecycleFrames()
			frames[1].GetVertex().Vertex.Value = &pb.Vertex_String_{String_: "original body"}
			originalHeader := proto.Clone(frames[0].GetHeader()).(*pb.SnapshotHeader)
			originalVertex := proto.Clone(frames[1].GetVertex().GetVertex()).(*pb.Vertex)
			mutated := false
			stream := &snapshotStageObservedStream{
				SnapshotStream: &snapshotSliceStream{frames: frames, err: terminal.err},
				after: func(open bool) {
					if open {
						return
					}
					// The remote producer may release or reuse all decoded
					// messages after its final status has been observed.
					frames[0].GetHeader().CutoffLocalSeq = 99
					frames[0].GetHeader().CutoffSeqPerOrigin["origin-a"] = 99
					frames[1].GetVertex().Vertex.Key = "source-reused-key"
					frames[1].GetVertex().Vertex.Value.(*pb.Vertex_String_).String_ = "source-reused-body"
					mutated = true
				},
			}
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotStageOwnershipGraph{snapshotLifecycleGraph: &snapshotLifecycleGraph{events: &events}}
			result, err := installSnapshot(t.Context(), newGraphOnlySnapshotInstaller(apply, graph), stream)
			if err != nil || !mutated || !proto.Equal(result.Header, originalHeader) || apply.cutoffs["origin-a"] != 7 {
				t.Fatalf("staged header aliased the released source or rejected clean EOF: mutated=%t header=%v cutoffs=%v err=%v", mutated, result.Header, apply.cutoffs, err)
			}
			if len(graph.vertices) != 1 || !proto.Equal(graph.vertices[0], originalVertex) || len(apply.finishes) != 1 || !apply.finishes[0] {
				t.Fatalf("live replay used mutable source bytes or did not verify once: vertices=%v finishes=%v", graph.vertices, apply.finishes)
			}
		})
	}
}

func TestGraphSnapshotStageReleasesPartialAndFailedRetainedPayloads(t *testing.T) {
	for _, phase := range []string{"partial collection", "source error", "partial replay", "canceled replay"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			frames := graphInstallerLifecycleFrames()
			source := &snapshotSliceStream{frames: frames}
			failure := errors.New("controlled collection failure after retained frames")
			if phase == "source error" {
				source.err = failure
			}
			stage := &graphSnapshotStage{ctx: ctx, source: source, limits: snapshotStageTestLimits(frames)}
			defer stage.release()
			if phase == "partial collection" {
				if !stage.Receive() {
					t.Fatal("partial collection retained no header", stage.Err())
				}
			} else {
				for stage.Receive() {
				}
				if phase == "source error" && stage.Err() != failure {
					t.Fatal("collection lost its original failure", stage.Err())
				}
			}
			if stage.bytes == 0 || len(stage.frames) == 0 || stage.source == nil {
				t.Fatal("ownership control retained no payload before cleanup")
			}
			if phase == "partial replay" || phase == "canceled replay" {
				if stage.Err() != nil {
					t.Fatal(stage.Err())
				}
				stage.source, stage.current = nil, nil
				stage.replaying = true
				before, consumed := stage.bytes, uint64(len(stage.frames[0]))
				if !stage.Receive() || stage.Msg().GetHeader() == nil || stage.frames[0] != nil || stage.bytes != before-consumed || stage.bytes == 0 {
					t.Fatalf("replay pinned its consumed encoding or refunded unread bodies: index=%d bytes=%d before=%d consumed=%d err=%v", stage.index, stage.bytes, before, consumed, stage.Err())
				}
				if phase == "canceled replay" {
					cancel()
					if stage.Receive() || !errors.Is(stage.Err(), context.Canceled) {
						t.Fatal("canceled replay continued with unread retained bodies", stage.Err())
					}
				}
			}
			stage.release()
			if stage.source != nil || stage.current != nil || stage.frames != nil || stage.bytes != 0 {
				t.Fatalf("%s cleanup retained source/current/encoded payload: source=%t current=%t frames=%d bytes=%d", phase, stage.source != nil, stage.current != nil, len(stage.frames), stage.bytes)
			}
		})
	}
}

func TestGraphSnapshotStagingPreservesTransportErrorBeforeNativeCapture(t *testing.T) {
	transportErr := errors.New("controlled native Snapshot transport failure")
	for _, status := range []struct {
		name   string
		source error
		want   error
	}{
		{name: "transport error", source: transportErr, want: transportErr},
		{name: "clean EOF without proof", want: peerauth.ErrMembership},
		{name: "explicit EOF without proof", source: io.EOF, want: peerauth.ErrMembership},
	} {
		t.Run(status.name, func(t *testing.T) {
			ctx, _ := snapshotRequestContext(t.Context(), snapshotAdmissionHTTPClientFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("unit stream unexpectedly issued an HTTP request")
				return nil, transportErr
			}), true)
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			stream := &snapshotStageObservedStream{SnapshotStream: &snapshotSliceStream{err: status.source}}
			_, err := installSnapshot(ctx, newGraphOnlySnapshotInstaller(apply, graph), stream)
			if err != status.want || stream.receives != 1 || len(events) != 0 || len(apply.finishes) != 0 {
				t.Fatalf("native proof handling masked source failure or began replay: receives=%d events=%v finishes=%v err=%v want=%v", stream.receives, events, apply.finishes, err, status.want)
			}
		})
	}
}
