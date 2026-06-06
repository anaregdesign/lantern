package service

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/replication"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReplicationSnapshotter is the narrow surface GetReplicationStatus
// reads from. *replication.Pump satisfies it directly; tests inject
// fakes that return a controlled []PeerSnapshot.
//
// The interface is defined in the consumer package (service) per the
// usual Go consumer-defined-interface convention, so replication/
// has zero dependency on service/.
type ReplicationSnapshotter interface {
	Snapshot() []replication.PeerSnapshot
}

// ReplicationStatusInfo is the wire-layer-supplied identity slice of
// GetReplicationStatus. node_id is the local HLC NodeID (rendered to
// lowercase hex by the handler so admin clients don't have to know
// the binary layout). enabled reflects "this server is participating
// in multi-peer replication" — false on a single-instance deployment
// even when a clock is wired (a stable random NodeID still gets a
// clock).
type ReplicationStatusInfo struct {
	NodeID  hlc.NodeID
	Enabled bool
}

// WithReplicationStatus attaches the pump snapshotter and identity
// slice GetReplicationStatus needs. Both are optional: leaving them
// unwired (the default) results in an enabled=false response with no
// peers — the explicit "single instance" representation, never
// Unimplemented.
func (s *LanternService) WithReplicationStatus(snap ReplicationSnapshotter, info ReplicationStatusInfo) *LanternService {
	s.replicationSnapshotter = snap
	s.replicationStatusInfo = info
	return s
}

// GetReplicationStatus returns a flat snapshot of the local node's
// outbound peer-replication state. Read-only — does not touch the
// graph cache or the mutation log. Cheap enough to call at any
// dashboard cadence (a single RLock on the pump's per-peer tracker).
func (s *LanternService) GetReplicationStatus(ctx context.Context, _ *pb.GetReplicationStatusRequest) (*pb.GetReplicationStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp := &pb.GetReplicationStatusResponse{
		NodeId:   formatNodeID(s.replicationStatusInfo.NodeID),
		LocalNow: timestamppb.New(time.Now()),
		Enabled:  s.replicationStatusInfo.Enabled,
	}
	if s.replicationSnapshotter == nil {
		return resp, nil
	}
	rows := s.replicationSnapshotter.Snapshot()
	resp.Peers = make([]*pb.ReplicationPeer, 0, len(rows))
	for _, row := range rows {
		peer := &pb.ReplicationPeer{
			Address:    row.Address,
			State:      peerStateToProto(row.State),
			AppliedSeq: row.AppliedSeq,
			Error:      row.LastError,
		}
		if !row.LastEventAt.IsZero() {
			peer.LastEventAt = timestamppb.New(row.LastEventAt)
		}
		resp.Peers = append(resp.Peers, peer)
	}
	return resp, nil
}

// formatNodeID renders an HLC NodeID as lowercase hex. The zero
// NodeID maps to a 32-character all-zero string so admin clients can
// always render *something* — the empty string would force them to
// special-case "field not set".
func formatNodeID(id hlc.NodeID) string {
	return hex.EncodeToString(id[:])
}

// peerStateToProto translates the package-local replication.PeerState
// enum to the wire enum. Kept here (in the consumer service package)
// so the replication package has no dependency on the generated pb
// types.
func peerStateToProto(s replication.PeerState) pb.ReplicationPeer_State {
	switch s {
	case replication.PeerStateConnecting:
		return pb.ReplicationPeer_STATE_CONNECTING
	case replication.PeerStateStreaming:
		return pb.ReplicationPeer_STATE_STREAMING
	case replication.PeerStateBackoff:
		return pb.ReplicationPeer_STATE_BACKOFF
	case replication.PeerStateClosed:
		return pb.ReplicationPeer_STATE_CLOSED
	default:
		return pb.ReplicationPeer_STATE_UNSPECIFIED
	}
}
