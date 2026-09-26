package service

import (
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
// publication-generation samples also reject reads overlapping a WAL write
// without holding the publication lock across a slow traversal or scan.
func (s *LanternService) withPublicGraphRead(capture func() error) error {
	s.snapshotReadCutMu.RLock()
	defer s.snapshotReadCutMu.RUnlock()
	before, err := s.graphReadGeneration()
	if err != nil {
		return err
	}
	captureErr := capture()
	after, err := s.graphReadGeneration()
	if err != nil {
		return err
	}
	if before != after {
		return publicationChangedDuringReadError()
	}
	return captureErr
}

func (s *LanternService) graphReadGeneration() (uint64, error) {
	s.replicationCutMu.RLock()
	defer s.replicationCutMu.RUnlock()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
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
// Store reads bypass the service fault check. A healthy private receipt commit
// releases their staged locks after the matching log entry is installed, but
// these error-free Core reads cannot report an indeterminate WAL outcome.
func (s *LanternService) withCommittedView(capture func() error) error {
	s.replicationCutMu.RLock()
	defer s.replicationCutMu.RUnlock()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return publicationGapError()
	}
	return capture()
}

// withExclusiveCommittedView also excludes server-owned receipt lookups,
// which advance Store high-water while holding a shared publication cut.
// Whole-state capture is infrequent and must copy Store and graph under one
// exclusive cut. Direct Core Store access is outside this service boundary.
func (s *LanternService) withExclusiveCommittedView(capture func() error) error {
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return publicationGapError()
	}
	return capture()
}
