package replication

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"google.golang.org/protobuf/proto"
)

func snapshotStageTestFramesWithEdge() []*pb.SnapshotResponse {
	frames := graphInstallerLifecycleFrames()
	edge := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{
		Tail: "installed", Head: "other",
		Contributions: []*pb.SnapshotEdgeContribution{{Weight: 1}},
	}}}
	frames[len(frames)-1].GetFooter().EdgeCount = 1
	return append(frames[:len(frames)-1], edge, frames[len(frames)-1])
}

func TestGraphSnapshotStagingExplicitBudgetsPrecedeLiveAdmission(t *testing.T) {
	for _, dimension := range []string{"per frame", "aggregate scratch disk"} {
		t.Run(dimension, func(t *testing.T) {
			frames := graphInstallerLifecycleFrames()
			config := testGraphSnapshotSpoolConfig(t)
			largestFrame := 0
			for _, frame := range frames {
				largestFrame = max(largestFrame, proto.Size(frame))
			}
			switch dimension {
			case "per frame":
				config.MaxFrameBytes = largestFrame - 1
			case "aggregate scratch disk":
				// The owner, frame spool and empty identity arena already
				// charge three metadata blocks. No frame extent fits.
				config.MaxDiskBytes, config.MaxFrameBytes = 12<<10, 1<<10
			}
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			installer := newGraphOnlySnapshotInstaller(apply, graph, config).(*graphOnlySnapshotInstaller)
			var owner *snapshotScratch
			installer.newScratch = func(ctx context.Context, config GraphSnapshotSpoolConfig) (*snapshotScratch, error) {
				var err error
				owner, err = newSnapshotScratch(ctx, config)
				return owner, err
			}
			if _, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: frames}); connect.CodeOf(err) != connect.CodeResourceExhausted {
				t.Fatalf("%s bound did not reject staged input: %v", dimension, err)
			}
			if len(events) != 0 || len(apply.finishes) != 0 || len(apply.cutoffs) != 0 {
				t.Fatalf("%s rejection touched live state: events=%v finishes=%v cutoffs=%v", dimension, events, apply.finishes, apply.cutoffs)
			}
			assertSnapshotStageScratchReleased(t, owner, config)
			// Only the explicitly configured allowance changes for retry.
			// The exact per-frame boundary accepts the same valid image.
			installer.spool.MaxFrameBytes = largestFrame
			installer.spool.MaxDiskBytes = 24 << 10
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
			installer := testGraphOnlySnapshotInstaller(t, apply, graph)
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
			_, err := installSnapshot(ctx, testGraphOnlySnapshotInstaller(t, apply, graph), stream)
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
			installer := testGraphOnlySnapshotInstaller(t, apply, graph)
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
			result, err := installSnapshot(t.Context(), testGraphOnlySnapshotInstaller(t, apply, graph), stream)
			if err != nil || !mutated || !proto.Equal(result.Header, originalHeader) || apply.cutoffs["origin-a"] != 7 {
				t.Fatalf("staged header aliased the released source or rejected clean EOF: mutated=%t header=%v cutoffs=%v err=%v", mutated, result.Header, apply.cutoffs, err)
			}
			if len(graph.vertices) != 1 || !proto.Equal(graph.vertices[0], originalVertex) || len(apply.finishes) != 1 || !apply.finishes[0] {
				t.Fatalf("live replay used mutable source bytes or did not verify once: vertices=%v finishes=%v", graph.vertices, apply.finishes)
			}
		})
	}
}

func TestGraphSnapshotStageReleasesPartialAndFailedFileOwnership(t *testing.T) {
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
			config := testGraphSnapshotSpoolConfig(t)
			owner, err := newSnapshotScratch(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			file, err := owner.create()
			if err != nil {
				t.Fatal(err)
			}
			stage := &graphSnapshotStage{ctx: ctx, source: source, maxFrameBytes: config.MaxFrameBytes, file: file, digest: sha256.New()}
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
			if stage.bytes == 0 || stage.file == nil || stage.source == nil {
				t.Fatal("ownership control retained no payload before cleanup")
			}
			if phase == "partial replay" || phase == "canceled replay" {
				if err := stage.seal(); err != nil {
					t.Fatal(err)
				}
				if !stage.Receive() || stage.Msg().GetHeader() == nil {
					t.Fatal("partial replay did not decode its owned header", stage.Err())
				}
				if phase == "canceled replay" {
					cancel()
					if stage.Receive() || !errors.Is(stage.Err(), context.Canceled) {
						t.Fatal("canceled replay continued with unread retained bodies", stage.Err())
					}
				}
			}
			stage.release()
			if stage.source != nil || stage.current != nil || stage.file != nil || stage.digest != nil || stage.bytes != 0 || stage.frameCount != 0 {
				t.Fatalf("%s cleanup retained source/current/file/payload accounting: source=%t current=%t file=%t bytes=%d frames=%d", phase, stage.source != nil, stage.current != nil, stage.file != nil, stage.bytes, stage.frameCount)
			}
			if used, files := owner.stats(); files != 0 || used != snapshotScratchMetadataBytes && used != 0 {
				t.Fatalf("%s release retained a scratch extent: used=%d files=%d", phase, used, files)
			}
			if err := owner.Close(); err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			assertSnapshotStageScratchReleased(t, owner, config)
		})
	}
}

func assertSnapshotStageScratchReleased(t *testing.T, owner *snapshotScratch, config GraphSnapshotSpoolConfig) {
	t.Helper()
	if owner == nil {
		t.Fatal("Snapshot did not acquire its scratch owner")
	}
	if used, files := owner.stats(); used != 0 || files != 0 {
		t.Fatalf("Snapshot retained disk credit or descriptors: used=%d files=%d", used, files)
	}
	// A fresh context must be able to claim exactly the same configured
	// directory. Counters alone do not prove that the flock lease closed.
	next, err := newSnapshotScratch(t.Context(), config)
	if err != nil {
		t.Fatal("Snapshot retained its directory lease", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGraphSnapshotStagingScratchFailuresNeverVerifyAndReleaseOwnership(t *testing.T) {
	writeFailure := errors.New("controlled late spool write failure")
	readFailure := errors.New("controlled spool read failure")
	closeFailure := errors.New("controlled scratch close failure after descriptor close")
	sourceFailure := errors.New("controlled source final status failure")
	for _, failure := range []struct {
		name       string
		want       error
		afterBegin bool
		source     error
	}{
		{name: "ENOSPC", want: syscall.ENOSPC},
		{name: "short write", want: io.ErrShortWrite},
		{name: "late footer write", want: writeFailure},
		{name: "digest read", want: readFailure},
		{name: "live replay read", want: readFailure, afterBegin: true},
		{name: "canonical close", want: closeFailure, afterBegin: true},
		{name: "owner close", want: closeFailure, afterBegin: true},
		{name: "cleanup after source failure", want: closeFailure, source: sourceFailure},
	} {
		t.Run(failure.name, func(t *testing.T) {
			config := testGraphSnapshotSpoolConfig(t)
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			installer := newGraphOnlySnapshotInstaller(apply, graph, config).(*graphOnlySnapshotInstaller)
			var owner *snapshotScratch
			attempts, writes := 0, 0
			injected := false
			installer.newScratch = func(ctx context.Context, config GraphSnapshotSpoolConfig) (*snapshotScratch, error) {
				scratch, err := newSnapshotScratch(ctx, config)
				attempts++
				if err != nil || attempts != 1 {
					return scratch, err
				}
				owner = scratch
				var canonical *os.File
				writeAt, readAt := scratch.writeAt, scratch.readAt
				scratch.writeAt = func(file *os.File, data []byte, offset int64) (int, error) {
					if canonical == nil {
						canonical = file // The first frame precedes identity writes.
					}
					if file == canonical {
						writes++
						switch {
						case failure.name == "ENOSPC" && writes == 1:
							injected = true
							return 0, syscall.ENOSPC
						case failure.name == "short write" && writes == 5:
							injected = true
							return len(data) - 1, nil
						case failure.name == "late footer write" && writes == 9:
							injected = true
							return 0, writeFailure
						}
					}
					return writeAt(file, data, offset)
				}
				scratch.readAt = func(file *os.File, data []byte, offset int64) (int, error) {
					if file == canonical && !injected && (failure.name == "digest read" || failure.name == "live replay read" && apply.checkedBegins != 0) {
						injected = true
						return 0, readFailure
					}
					return readAt(file, data, offset)
				}
				scratch.afterClose = func(file *os.File) error {
					if file == canonical && !injected && (failure.name == "canonical close" || failure.name == "cleanup after source failure") {
						injected = true
						return closeFailure
					}
					return nil
				}
				scratch.afterOwnerClose = func() error {
					if failure.name == "owner close" && apply.checkedBegins != 0 && !injected {
						injected = true
						return closeFailure
					}
					return nil
				}
				return scratch, nil
			}
			result, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: graphInstallerLifecycleFrames(), err: failure.source})
			if !injected || !errors.Is(err, failure.want) || failure.source != nil && !errors.Is(err, failure.source) {
				t.Fatalf("scratch fault did not preserve its cause: injected=%t err=%v want=%v source=%v", injected, err, failure.want, failure.source)
			}
			if result.Header != nil || result.Graph != (SnapshotGraphCounts{}) || len(apply.cutoffs) != 0 || strings.Contains(strings.Join(events, ","), "finish:true") {
				t.Fatalf("failed staging advertised a verified install: result=%+v events=%v cutoffs=%v", result, events, apply.cutoffs)
			}
			var diagnosed *snapshotInstallError
			if !errors.As(err, &diagnosed) {
				t.Fatal("failed install lacks local diagnostic envelope", err)
			}
			wantPhase := "capture_and_validate"
			if failure.name == "digest read" {
				wantPhase = "seal"
			}
			if failure.afterBegin {
				wantPhase = "live_replay"
			}
			detail := diagnosed.diagnostic
			if detail.phase != wantPhase || detail.liveStarted != failure.afterBegin || detail.elapsed < 0 || detail.phaseElapsed < 0 {
				t.Fatalf("wrong failure boundary: got=%+v wantPhase=%s live=%t", detail, wantPhase, failure.afterBegin)
			}
			if failure.afterBegin && (detail.stagedFrames != 3 || detail.stagedBytes == 0 || detail.liveFramesRead == 0) {
				t.Fatalf("cleanup discarded completed stage/read counts: %+v", detail)
			}
			if strings.Contains(failure.name, "close") || failure.name == "cleanup after source failure" {
				if !detail.cleanupFailed {
					t.Fatalf("cleanup failure was not retained: %+v", detail)
				}
			}
			if failure.afterBegin {
				if apply.checkedBegins != 1 || len(apply.finishes) != 1 || apply.finishes[0] || apply.releases != 1 {
					t.Fatalf("live replay failure did not release exactly once as unverified: events=%v finishes=%v releases=%d", events, apply.finishes, apply.releases)
				}
			} else if len(events) != 0 || len(apply.finishes) != 0 {
				t.Fatalf("collection/validation failure reached live admission: events=%v finishes=%v", events, apply.finishes)
			}
			assertSnapshotStageScratchReleased(t, owner, config)
			before := len(apply.finishes)
			if _, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: graphInstallerLifecycleFrames()}); err != nil || len(apply.finishes) != before+1 || !apply.finishes[before] {
				t.Fatalf("fresh-context retry with the same config failed: finishes=%v err=%v", apply.finishes, err)
			}
		})
	}
}

func TestGraphSnapshotStagingCancellationDuringDiskPassesReleasesOwnership(t *testing.T) {
	for _, phase := range []string{"collection", "identity sort output", "canonical digest", "live replay"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			config := testGraphSnapshotSpoolConfig(t)
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotLifecycleGraph{events: &events}
			installer := newGraphOnlySnapshotInstaller(apply, graph, config).(*graphOnlySnapshotInstaller)
			frames := graphInstallerLifecycleFrames()
			if phase == "identity sort output" {
				frames = snapshotStageTestFramesWithEdge()
				second := proto.Clone(frames[2]).(*pb.SnapshotResponse)
				second.GetEdge().Head = "second-distinct-edge"
				footer := frames[len(frames)-1]
				footer.GetFooter().EdgeCount++
				frames = append(frames[:len(frames)-1], second, footer)
			}
			remoteEOF := false
			stream := &snapshotStageObservedStream{SnapshotStream: &snapshotSliceStream{frames: frames}, after: func(open bool) {
				remoteEOF = !open
			}}
			var owner *snapshotScratch
			attempts, writes := 0, 0
			interrupted := false
			installer.newScratch = func(ctx context.Context, config GraphSnapshotSpoolConfig) (*snapshotScratch, error) {
				scratch, err := newSnapshotScratch(ctx, config)
				attempts++
				if err != nil || attempts != 1 {
					return scratch, err
				}
				owner = scratch
				var canonical *os.File
				writeAt, readAt := scratch.writeAt, scratch.readAt
				scratch.writeAt = func(file *os.File, data []byte, offset int64) (int, error) {
					if canonical == nil {
						canonical = file
					}
					if file == canonical {
						writes++
					}
					n, err := writeAt(file, data, offset)
					if !interrupted && (phase == "collection" && writes == 4 || phase == "identity sort output" && remoteEOF && file != canonical) {
						interrupted = true
						cancel()
					}
					return n, err
				}
				scratch.readAt = func(file *os.File, data []byte, offset int64) (int, error) {
					n, err := readAt(file, data, offset)
					if file == canonical && !interrupted && (phase == "canonical digest" || phase == "live replay" && apply.checkedBegins != 0) {
						interrupted = true
						cancel()
					}
					return n, err
				}
				return scratch, nil
			}
			result, err := installSnapshot(ctx, installer, stream)
			if !interrupted || !errors.Is(err, context.Canceled) || result.Header != nil || len(apply.cutoffs) != 0 {
				t.Fatalf("disk-pass cancellation was lost or verified: interrupted=%t result=%+v cutoffs=%v err=%v", interrupted, result, apply.cutoffs, err)
			}
			if phase == "live replay" {
				if apply.checkedBegins != 1 || len(apply.finishes) != 1 || apply.finishes[0] || apply.releases != 1 {
					t.Fatalf("canceled live replay was not released as unverified: events=%v finishes=%v releases=%d", events, apply.finishes, apply.releases)
				}
			} else if len(events) != 0 || len(apply.finishes) != 0 {
				t.Fatalf("canceled disk validation entered live install: events=%v finishes=%v", events, apply.finishes)
			}
			assertSnapshotStageScratchReleased(t, owner, config)
			before := len(apply.finishes)
			if _, err := installSnapshot(t.Context(), installer, &snapshotSliceStream{frames: graphInstallerLifecycleFrames()}); err != nil || len(apply.finishes) != before+1 || !apply.finishes[before] {
				t.Fatalf("cancellation retained the installer flight or directory: finishes=%v err=%v", apply.finishes, err)
			}
		})
	}
}

// The source deliberately reuses one payload and one current message. Neither
// it nor the sink keeps a dataset-sized list; distinct frame bytes must survive
// until the later replay solely through the owned spool.
type snapshotStageGeneratedStream struct {
	count, next int
	payload     []byte
	current     *pb.SnapshotResponse
	digest      hash.Hash
	encoded     uint64
}

func (s *snapshotStageGeneratedStream) Receive() bool {
	s.current = nil
	switch {
	case s.next == 0:
		s.current = graphInstallerLifecycleFrames()[0]
	case s.next <= s.count:
		binary.BigEndian.PutUint64(s.payload[:8], uint64(s.next))
		key := fmt.Sprintf("vertex-%03d", s.next)
		_, _ = s.digest.Write([]byte(key))
		_, _ = s.digest.Write(s.payload)
		s.current = &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_Bytes{Bytes: s.payload}}}}}
	case s.next == s.count+1:
		s.current = &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{VertexCount: uint64(s.count)}}}
	default:
		return false
	}
	s.next++
	s.encoded += uint64(proto.Size(s.current))
	return true
}

func (s *snapshotStageGeneratedStream) Msg() *pb.SnapshotResponse { return s.current }
func (s *snapshotStageGeneratedStream) Err() error                { return nil }

type snapshotStageDigestGraph struct {
	*snapshotLifecycleGraph
	count  uint64
	digest hash.Hash
}

func (g *snapshotStageDigestGraph) PutVertexWithExpirationHLC(key string, vertex *pb.Vertex, _ time.Time, _ hlc.Timestamp) bool {
	g.count++
	_, _ = g.digest.Write([]byte(key))
	_, _ = g.digest.Write(vertex.GetBytes())
	return true
}

func TestGraphSnapshotStagingStreamsMoreThan64MiBWithExactDataAndBoundedScratch(t *testing.T) {
	config := testGraphSnapshotSpoolConfig(t)
	config.MaxFrameBytes = (1 << 20) + 1024
	var events []string
	apply := &snapshotLifecycleMutationApplier{events: &events}
	graph := &snapshotStageDigestGraph{snapshotLifecycleGraph: &snapshotLifecycleGraph{events: &events}, digest: sha256.New()}
	installer := newGraphOnlySnapshotInstaller(apply, graph, config).(*graphOnlySnapshotInstaller)
	var owner *snapshotScratch
	installer.newScratch = func(ctx context.Context, config GraphSnapshotSpoolConfig) (*snapshotScratch, error) {
		var err error
		owner, err = newSnapshotScratch(ctx, config)
		return owner, err
	}
	generated := &snapshotStageGeneratedStream{count: 65, payload: make([]byte, 1<<20), digest: sha256.New()}
	for index := range generated.payload {
		generated.payload[index] = byte(index)
	}
	var stagedBytes uint64
	stream := &snapshotStageObservedStream{SnapshotStream: generated, after: func(open bool) {
		if !open {
			stagedBytes, _ = owner.stats()
		}
	}}
	result, err := installSnapshot(t.Context(), installer, stream)
	if err != nil || generated.encoded <= 64<<20 || stagedBytes <= 64<<20 || stagedBytes > config.MaxDiskBytes {
		t.Fatalf("explicit disk allowance did not stage the large stream: encoded=%d scratch=%d quota=%d err=%v", generated.encoded, stagedBytes, config.MaxDiskBytes, err)
	}
	if result.Graph.Vertices != 65 || graph.count != 65 || !equalSnapshotDigest(generated.digest.Sum(nil), graph.digest.Sum(nil)) || len(apply.finishes) != 1 || !apply.finishes[0] {
		t.Fatalf("large replay changed data or did not verify exactly once: result=%+v applied=%d finishes=%v", result.Graph, graph.count, apply.finishes)
	}
	assertSnapshotStageScratchReleased(t, owner, config)
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
			_, err := installSnapshot(ctx, testGraphOnlySnapshotInstaller(t, apply, graph), stream)
			if !errors.Is(err, status.want) || err.Error() != status.want.Error() || stream.receives != 1 || len(events) != 0 || len(apply.finishes) != 0 {
				t.Fatalf("native proof handling masked source failure or began replay: receives=%d events=%v finishes=%v err=%v want=%v", stream.receives, events, apply.finishes, err, status.want)
			}
		})
	}
}
