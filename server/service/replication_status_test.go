package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/replication"
)

// fakeSnapshotter is the simplest possible ReplicationSnapshotter for
// driving the handler in isolation. Tests construct it inline with
// the rows they want returned; no goroutines, no locking.
type fakeSnapshotter struct {
	rows []replication.PeerSnapshot
}

func (f *fakeSnapshotter) Snapshot() []replication.PeerSnapshot { return f.rows }

func TestLanternService_GetReplicationStatus(t *testing.T) {
	t.Run("EnabledAndProjectsRows", func(t *testing.T) {
		fb := newFakeBackend()
		nodeID := hlc.NodeID{0xa1, 0xb2, 0xc3, 0xd4}
		eventAt := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
		snap := &fakeSnapshotter{rows: []replication.PeerSnapshot{
			{
				Address:     "10.0.0.2:50051",
				State:       replication.PeerStateStreaming,
				LastEventAt: eventAt,
				AppliedSeq:  42,
			},
			{
				Address:   "10.0.0.3:50051",
				State:     replication.PeerStateBackoff,
				LastError: "transport: connection refused",
			},
		}}
		svc := NewLanternService(fb).WithReplicationStatus(snap, ReplicationStatusInfo{
			NodeID: nodeID, Enabled: true,
		})

		resp, err := svc.GetReplicationStatus(context.Background(), &pb.GetReplicationStatusRequest{})
		if err != nil {
			t.Fatalf("GetReplicationStatus: %v", err)
		}
		if !resp.GetEnabled() {
			t.Errorf("Enabled: got false want true")
		}
		if got, want := resp.GetNodeId(), "a1b2c3d4000000000000000000000000"; got != want {
			t.Errorf("NodeId: got %q want %q", got, want)
		}
		if resp.GetLocalNow() == nil || resp.GetLocalNow().AsTime().IsZero() {
			t.Errorf("LocalNow: got nil/zero want server clock")
		}
		if len(resp.GetPeers()) != 2 {
			t.Fatalf("Peers: got %d want 2", len(resp.GetPeers()))
		}
		p0 := resp.GetPeers()[0]
		if p0.GetAddress() != "10.0.0.2:50051" || p0.GetState() != pb.ReplicationPeer_STATE_STREAMING ||
			p0.GetAppliedSeq() != 42 || p0.GetError() != "" {
			t.Errorf("peer[0] = %+v, want streaming/seq=42/no-err", p0)
		}
		if p0.GetLastEventAt() == nil || !p0.GetLastEventAt().AsTime().Equal(eventAt) {
			t.Errorf("peer[0].LastEventAt: got %v want %v", p0.GetLastEventAt(), eventAt)
		}
		p1 := resp.GetPeers()[1]
		if p1.GetAddress() != "10.0.0.3:50051" || p1.GetState() != pb.ReplicationPeer_STATE_BACKOFF ||
			p1.GetError() != "transport: connection refused" {
			t.Errorf("peer[1] = %+v, want backoff/err", p1)
		}
		if p1.GetLastEventAt() != nil {
			t.Errorf("peer[1].LastEventAt: got %v, want nil when never received", p1.GetLastEventAt())
		}
	})

	t.Run("DefaultsToSingleInstanceWhenNotWired", func(t *testing.T) {
		// Construction path without WithReplicationStatus must still
		// produce a well-formed response (enabled=false, no peers, NodeId
		// = all zeros). Mirrors GetServerStatus's "additive builder" rule.
		svc := NewLanternService(newFakeBackend())

		resp, err := svc.GetReplicationStatus(context.Background(), &pb.GetReplicationStatusRequest{})
		if err != nil {
			t.Fatalf("GetReplicationStatus: %v", err)
		}
		if resp.GetEnabled() {
			t.Errorf("Enabled: got true want false")
		}
		if got, want := resp.GetNodeId(), "00000000000000000000000000000000"; got != want {
			t.Errorf("NodeId: got %q want %q", got, want)
		}
		if len(resp.GetPeers()) != 0 {
			t.Errorf("Peers: got %d want 0 (single-instance)", len(resp.GetPeers()))
		}
		if resp.GetLocalNow() == nil {
			t.Errorf("LocalNow: got nil, want server clock")
		}
	})

	t.Run("HonorsCanceledContext", func(t *testing.T) {
		svc := NewLanternService(newFakeBackend())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := svc.GetReplicationStatus(ctx, &pb.GetReplicationStatusRequest{})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got err %v, want context.Canceled", err)
		}
	})
}
