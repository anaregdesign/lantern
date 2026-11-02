package dataruntime

import (
	"context"
	"time"

	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// withPublicGraphRead orders a GraphCache read against Snapshot admission.
// The installer takes the exclusive latch for both fault transitions, so a
// read either completes before replay starts or sees the fault. Callbacks
// must detach all returned graph values before releasing this latch: Connect
// serializes responses after the service call has returned. Short
// publication-generation samples reject reads overlapping a WAL write
// without holding the publication lock across a slow traversal or scan.
func (s *Executor) withPublicGraphRead(capture func() error) error {
	s.snapshotReadCutMu.RLock()
	defer s.snapshotReadCutMu.RUnlock()
	before, err := s.graphReadGeneration()
	if err != nil {
		return err
	}
	started := s.publicationCaptureStart()
	captureErr := capture()
	finished := s.publicationCaptureStart()
	after, err := s.graphReadGeneration()
	if err != nil {
		s.observePublicationRead(1, "fault", started, finished)
		return err
	}
	if captureErr != nil {
		s.observePublicationRead(1, "capture_error", started, finished)
		return captureErr
	}
	if before == after {
		s.observePublicationRead(1, "consistent", started, finished)
		return nil
	}
	s.observePublicationRead(1, "conflict", started, finished)
	return publicationChangedDuringReadError()
}

// withPublicGraphReadRetry may repeat a read-only capture after an overlapping
// publication. Callers must replace, not append to, their per-attempt result.
// If writes overlap all optimistic attempts, the final capture holds the
// publication read cut instead of returning an error for healthy contention.
func (s *Executor) withPublicGraphReadRetry(ctx context.Context, capture func() error) error {
	s.snapshotReadCutMu.RLock()
	defer s.snapshotReadCutMu.RUnlock()
	for attempt := 1; attempt <= 3; attempt++ {
		before := s.replicationCutMu.readCertificate.Load()
		if !before.stable() {
			return s.capturePublicGraphFallback(ctx, capture)
		}
		if before.faulted {
			return publicationGapError()
		}
		started := s.publicationCaptureStart()
		captureErr := capture()
		finished := s.publicationCaptureStart()
		after := s.replicationCutMu.readCertificate.Load()
		if after.stable() && after.faulted {
			s.observePublicationRead(attempt, "fault", started, finished)
			return publicationGapError()
		}
		if captureErr != nil {
			if !after.stable() {
				// Preserve post-capture fault priority, then return the original
				// error. This callback has already run and must not be replayed.
				checked := false
				err := s.withCommittedView(func() error { checked = true; return captureErr })
				outcome := "fault"
				if checked {
					outcome = "capture_error"
				}
				s.observePublicationRead(attempt, outcome, started, finished)
				return err
			}
			s.observePublicationRead(attempt, "capture_error", started, finished)
			return captureErr
		}
		if before == after {
			s.observePublicationRead(attempt, "consistent", started, finished)
			return nil
		}
		s.observePublicationRead(attempt, "conflict", started, finished)
		if !after.stable() {
			return s.capturePublicGraphFallback(ctx, capture)
		}
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
	}
	return s.capturePublicGraphFallback(ctx, capture)
}

func (s *Executor) capturePublicGraphFallback(ctx context.Context, capture func() error) error {
	var started, finished time.Time
	captured := false
	err := s.withCommittedViewNamed("read_fallback", func() error {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		captured = true
		started = s.publicationCaptureStart()
		err := capture()
		finished = s.publicationCaptureStart()
		return err
	})
	if captured {
		outcome := "consistent"
		if err != nil {
			outcome = "capture_error"
		}
		s.observePublicationRead(4, outcome, started, finished)
	}
	return err
}

func (s *Executor) graphReadGeneration() (uint64, error) {
	gate := s.lockPublicationGate("generation_probe", true)
	defer gate.Unlock()
	if s.publicGraphFaultLocked() {
		return 0, publicationGapError()
	}
	return s.replicationCutMu.generation, nil
}

func detachedGraphVertex(vertex *pb.Vertex) *pb.Vertex {
	if vertex == nil {
		return nil
	}
	return proto.Clone(vertex).(*pb.Vertex)
}

func detachSnapshotVertices(vertices []graphcache.SnapshotVertex[string, *pb.Vertex]) {
	for i := range vertices {
		vertices[i].Value = detachedGraphVertex(vertices[i].Value)
	}
}

func detachTraversalVertices(g *coregraph.Graph[string, *pb.Vertex]) {
	if g == nil {
		return
	}
	for key, vertex := range g.Vertices {
		g.Vertices[key] = detachedGraphVertex(vertex)
	}
}

// withCommittedView holds the server publication cut while a caller copies a
// composite observation. The callback must finish its reads before returning;
// retaining a backend, Store, or log pointer is not a committed view. A failed
// local/relay publication, indeterminate receipt commit, or incomplete
// Snapshot install must not be reported as a healthy graph or receipt read.
//
// This protects server-owned observations only. Direct GraphCache and receipt
// Store reads bypass the executor fault check. A healthy private receipt commit
// releases their staged locks after the matching log entry is installed, but
// these error-free Core reads cannot report an indeterminate WAL outcome.
func (s *Executor) withCommittedView(capture func() error) error {
	return s.withCommittedViewNamed("committed_view", capture)
}

func (s *Executor) withCommittedViewNamed(category string, capture func() error) error {
	gate := s.lockPublicationGate(category, true)
	defer gate.Unlock()
	if s.publicGraphFaultLocked() {
		return publicationGapError()
	}
	return capture()
}

// withExclusiveCommittedView also excludes server-owned receipt lookups,
// which advance Store high-water while holding a shared publication cut.
// Whole-state capture is infrequent and must copy Store and graph under one
// exclusive cut. Direct Core Store access is outside this executor boundary.
func (s *Executor) withExclusiveCommittedView(capture func() error) error {
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.publicGraphFaultLocked() {
		return publicationGapError()
	}
	return capture()
}
