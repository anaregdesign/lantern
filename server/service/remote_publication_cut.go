package service

import (
	"bytes"
	"fmt"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type remotePublicationCut struct {
	origin    hlc.NodeID
	committed uint64
}

// Caller holds the publication read or write cut. This is the same ordered
// validation owner for a duplicate proof and the refreshed exclusive path.
// Public-read faults are intentionally broader: a legacy graph-first WAL
// fault must still allow private pending retry and older committed duplicates.
func (s *LanternService) checkRemotePublicationAtCut(m *pb.Mutation, receipt receiptMutationEnvelope) (remotePublicationCut, error) {
	var cut remotePublicationCut
	if s.receiptCommitFaulted {
		return cut, publicationGapError()
	}
	if s.snapshotInstallFaulted {
		return cut, publicationGapError()
	}
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_DeleteVerticesByPrefix, *pb.MutationOp_DeleteEdgesByPrefix:
		return cut, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: predicate-shaped delete replay is unsupported; publish exact victims"))
	}
	if len(m.GetOrigin()) != len(cut.origin) || m.GetSeq() == 0 || m.GetHlc() == nil || len(m.GetHlc().GetNodeId()) != len(cut.origin) {
		return cut, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: mutation requires a 16-byte origin/HLC NodeID and nonzero seq"))
	}
	copy(cut.origin[:], m.GetOrigin())
	if cut.origin == (hlc.NodeID{}) || zeroNodeID(m.GetHlc().GetNodeId()) {
		return cut, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: zero NodeID is forbidden"))
	}
	if !bytes.Equal(m.GetHlc().GetNodeId(), m.GetOrigin()) {
		return cut, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: HLC NodeID differs from origin"))
	}
	if s.origins == nil {
		return cut, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: origin state is unavailable"))
	}
	if receipt != nil {
		if err := s.validateReplicatedReceiptEnvelope(receipt); err != nil {
			return cut, err
		}
	} else if err := s.validateDurableRemoteHLC(hlcFromProto(m.GetHlc())); err != nil {
		return cut, err
	}
	cut.committed = s.origins.LocalSeq(cut.origin)
	return cut, nil
}

// One opportunistic read proof; a miss adds no queued reader and inspects no
// protected state. A nonduplicate must be revalidated by the writer afterward.
func (s *LanternService) probeCommittedRemoteMutation(m *pb.Mutation, receipt receiptMutationEnvelope) (duplicate bool, pending int, err error) {
	if !s.replicationCutMu.TryRLock() {
		return false, 0, nil
	}
	defer s.replicationCutMu.RUnlock()
	cut, err := s.checkRemotePublicationAtCut(m, receipt)
	if err != nil {
		return false, 0, err
	}
	if m.GetSeq() <= cut.committed {
		return true, s.pendingCount, nil
	}
	return false, 0, nil
}
