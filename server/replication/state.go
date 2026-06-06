package replication

import (
	"sort"
	"sync"
	"time"
)

// PeerState mirrors graph.v1.ReplicationPeer.State without importing
// the pb package (replication/ must stay free of pb-service deps so
// the package can be unit-tested without spinning up generated code).
// The handler in server/service translates these to proto enums.
type PeerState int

const (
	PeerStateUnspecified PeerState = iota
	PeerStateConnecting
	PeerStateStreaming
	PeerStateBackoff
	PeerStateClosed
)

// PeerSnapshot is one row of Pump.Snapshot. Fields are best-effort
// point-in-time samples; the snapshot itself is taken under a single
// lock acquisition so all rows share a consistent instant.
type PeerSnapshot struct {
	Address     string
	State       PeerState
	LastEventAt time.Time
	AppliedSeq  uint64
	LastError   string
}

// peerTracker is the Pump's per-peer scoreboard. It is constructed
// once per Pump and threaded into each per-peer goroutine so the
// admin observability surface (GetReplicationStatus, #315) has a
// stable read-side view of the otherwise goroutine-local state
// (current backoff, last fromSeq, last err).
//
// The tracker only stores currently-managed peers — removePeer is
// invoked when a supervisor reconcile drops an address, so stale
// rows do not leak into the snapshot.
type peerTracker struct {
	mu    sync.RWMutex
	peers map[string]*peerRow
}

type peerRow struct {
	state       PeerState
	lastEventAt time.Time
	appliedSeq  uint64
	lastErr     string
}

func newPeerTracker() *peerTracker {
	return &peerTracker{peers: make(map[string]*peerRow)}
}

// setState records a lifecycle transition. Creates the row if absent
// so callers (runPeer's first iteration) do not have to initialise.
func (t *peerTracker) setState(addr string, st PeerState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	row := t.peers[addr]
	if row == nil {
		row = &peerRow{}
		t.peers[addr] = row
	}
	row.state = st
}

// recordEvent updates the streaming bookkeeping after a successful
// Recv. Clears lastErr so the next snapshot reflects "healed" rather
// than leaking the prior error indefinitely.
func (t *peerTracker) recordEvent(addr string, appliedSeq uint64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	row := t.peers[addr]
	if row == nil {
		row = &peerRow{}
		t.peers[addr] = row
	}
	row.state = PeerStateStreaming
	row.lastEventAt = at
	row.appliedSeq = appliedSeq
	row.lastErr = ""
}

// recordError captures the most recent session error and transitions
// the row to BACKOFF. The error string is what the admin UI will
// show; truncation/redaction is the handler's concern, not the
// tracker's.
func (t *peerTracker) recordError(addr string, err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	row := t.peers[addr]
	if row == nil {
		row = &peerRow{}
		t.peers[addr] = row
	}
	row.state = PeerStateBackoff
	row.lastErr = err.Error()
}

// removePeer drops the row entirely. Called from runPeer on goroutine
// exit so a permanently-removed peer (e.g. dropped by DNS discovery)
// does not linger in the snapshot.
func (t *peerTracker) removePeer(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, addr)
}

// snapshot returns a defensive copy of the current per-peer state.
// Rows are sorted by address so admin clients see stable order
// across calls without their own sort step.
func (t *peerTracker) snapshot() []PeerSnapshot {
	t.mu.RLock()
	out := make([]PeerSnapshot, 0, len(t.peers))
	for addr, row := range t.peers {
		out = append(out, PeerSnapshot{
			Address:     addr,
			State:       row.state,
			LastEventAt: row.lastEventAt,
			AppliedSeq:  row.appliedSeq,
			LastError:   row.lastErr,
		})
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// Snapshot returns the current per-peer state, ordered by address. It
// is the single read-side seam consumed by server/service to fulfil
// GetReplicationStatus. Safe to call from any goroutine at any time;
// returns an empty slice (never nil) when no peers are tracked.
func (p *Pump) Snapshot() []PeerSnapshot {
	if p == nil || p.tracker == nil {
		return []PeerSnapshot{}
	}
	return p.tracker.snapshot()
}
