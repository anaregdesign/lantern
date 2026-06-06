package integration_test

import (
	"context"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestLantern_GetReplicationStatus_EndToEnd(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := l.GetReplicationStatus(ctx)
	if err != nil {
		t.Fatalf("GetReplicationStatus: %v", err)
	}

	// In-process server was constructed via service.NewLanternService
	// without WithReplicationStatus, so this is the "single-instance"
	// representation: enabled=false, NodeId=all zeros, no peers, but
	// LocalNow is always server-supplied.
	if st.GetEnabled() {
		t.Errorf("Enabled: got true, want false (single-instance test rig)")
	}
	if got, want := st.GetNodeId(), "00000000000000000000000000000000"; got != want {
		t.Errorf("NodeId: got %q want %q", got, want)
	}
	if len(st.GetPeers()) != 0 {
		t.Errorf("Peers: got %d want 0 (single-instance test rig)", len(st.GetPeers()))
	}
	if st.GetLocalNow() == nil {
		t.Errorf("LocalNow: got nil, want server clock")
	}

	// PeerLag against a nil peer must be the explicit "no signal" zero,
	// never a panic — it is the helper's contract.
	if got := client.PeerLag(st, nil); got != 0 {
		t.Errorf("PeerLag(nil peer): got %v, want 0", got)
	}
}
