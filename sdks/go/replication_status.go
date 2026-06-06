package client

import (
	"context"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ReplicationStatus is the flat snapshot returned by
// Lantern.GetReplicationStatus. Aliased to the generated proto type so
// callers can freely pass it across SDK / pb boundaries without
// conversion — mirrors the ServerStatus pattern.
type ReplicationStatus = pb.GetReplicationStatusResponse

// ReplicationPeer is one row of ReplicationStatus.Peers. Aliased for
// the same reason as ReplicationStatus.
type ReplicationPeer = pb.ReplicationPeer

// PeerLag returns wall-clock staleness of peer.LastEventAt against the
// server-supplied LocalNow timestamp (preferred over the client's clock
// to avoid skew). Returns 0 when either field is missing — the
// "we have no signal" representation, never a negative value.
func PeerLag(s *ReplicationStatus, peer *ReplicationPeer) time.Duration {
	if s == nil || peer == nil || s.LocalNow == nil || peer.LastEventAt == nil {
		return 0
	}
	lag := s.LocalNow.AsTime().Sub(peer.LastEventAt.AsTime())
	if lag < 0 {
		return 0
	}
	return lag
}

// GetReplicationStatus returns the server's view of its outbound peer
// replication state — one row per configured (or DNS-discovered) peer.
// Cheap to call (a single RLock on the pump's per-peer tracker) and
// intended for the admin UI's "Ops" tab.
//
// On a single-instance server the response has Enabled=false and an
// empty Peers slice; this is the explicit "no replication" representation
// and never an Unimplemented error.
func (l *Lantern) GetReplicationStatus(ctx context.Context) (*ReplicationStatus, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := l.client.GetReplicationStatus(ctx, &pb.GetReplicationStatusRequest{})
	if err != nil {
		return nil, wrapStatus(err)
	}
	return resp, nil
}
