package security

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorityRenewalNetwork(t *testing.T) {
	clocks := map[uint32]*authorityTimeOwner{}
	ticks := map[uint32]*atomic.Uint64{}
	n := s3aTestCluster(t, func(id uint32, c *s3aConfig) {
		clock, counter := fakeAuthorityTimeOwnerAt(t, c.Membership.Now())
		clocks[id], ticks[id] = clock, counter
		if err := bindAuthorityNetworkTime(c, clock); err != nil {
			t.Fatal(err)
		}
		c.hooks = &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}
		if id == 3 {
			c.hooks.beforeResponse = func(ctx context.Context) { <-ctx.Done() }
		}
	})
	n.startAll()
	o := n.nodes[1]
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	if err := o.renewAuthority(ctx); err != nil {
		t.Fatal("reachable majority with delayed third peer", err)
	}
	o.kernel.gate.Lock()
	_, active, err := o.receiver.currentLocked()
	o.kernel.gate.Unlock()
	if err != nil || active == nil || len(active.certificate.votes) != 2 {
		t.Fatal("native mTLS renewal", err)
	}
	for _, v := range active.certificate.votes {
		if v.member == 3 {
			t.Fatal("delayed peer fabricated a vote")
		}
	}
	// The renewal request's receiver must match the actual mTLS sender.
	foreign, err := n.nodes[3].receiver.challenge()
	if err != nil {
		t.Fatal(err)
	}
	_, status, err := o.request(ctx, 2, authorityRenewalPath, foreign, 68)
	n.nodes[3].receiver.abandon(foreign)
	if err == nil && status == http.StatusOK {
		t.Fatal("cross-workload renewal accepted")
	}
	// Consensus retains its reserved FIFO and outbox capacity while the
	// separate renewal queue exists. A NOOP invalidates the captured prefix.
	if _, err := o.Drive(ctx, [32]byte{}); err != nil {
		t.Fatal("renewal traffic blocked consensus", err)
	}
	o.kernel.gate.Lock()
	_, _, err = o.receiver.currentLocked()
	o.kernel.gate.Unlock()
	if err == nil {
		t.Fatal("old-prefix capability survived NOOP")
	}
	if err = o.renewAuthority(ctx); err != nil {
		t.Fatal("renew new drained prefix", err)
	}
	clocks[1].mu.Lock()
	clocks[1].anchor = nil
	clocks[1].mu.Unlock()
	o.kernel.gate.Lock()
	_, _, err = o.receiver.currentLocked()
	o.kernel.gate.Unlock()
	if err == nil || o.check(ctx) == nil {
		t.Fatal("time source loss left current/mTLS authority usable")
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityRenewalNetworkConfiguration(t *testing.T) {
	n := s3aTestCluster(t, nil)
	c := n.configs[1]
	clock, _ := fakeAuthorityTimeOwnerAt(t, c.Membership.Now())
	c.Limits.MaxConnections = 3
	if bindAuthorityNetworkTime(&c, clock) == nil {
		t.Fatal("no connection headroom for control and renewal")
	}
	if bindAuthorityNetworkTime(nil, clock) == nil || bindAuthorityNetworkTime(&c, nil) == nil {
		t.Fatal("invalid native composition")
	}
	c = n.configs[1]
	if err := bindAuthorityNetworkTime(&c, clock); err != nil {
		t.Fatal(err)
	}
	clock.mu.Lock()
	clock.anchor = nil
	clock.mu.Unlock()
	if c.Membership.Now().Year() != 2262 {
		t.Fatal("source loss substituted ordinary wall time")
	}
	if _, _, err := c.Membership.TimeBounds(); err == nil {
		t.Fatal("source loss supplied bounds")
	}
}

func TestAuthorityRenewalNetworkCatchesUpBeforeNewChallenge(t *testing.T) {
	n, owners, _ := authorityTestComposite(t)
	n.nodes[1].hooks.beforeSend = func(_ context.Context, to uint32, raw []byte) error {
		if to == 3 && raw[len(s2cMessageMagic)] == s2cChosen {
			return errS3AWire
		}
		return nil
	}
	ctx := s3aTestContext(t)
	if _, err := n.nodes[1].Drive(ctx, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	n.nodes[3].kernel.gate.Lock()
	old := n.nodes[3].kernel.replayState.slot
	n.nodes[3].kernel.gate.Unlock()
	if old != 0 {
		t.Fatal("fixture did not lose chosen notification")
	}
	if err := n.nodes[3].renewAuthority(ctx); err == nil {
		t.Fatal("old installed head renewed")
	}
	if err := n.nodes[3].renewAndCatchUp(ctx, 1); err != nil {
		t.Fatal("range recovery then fresh renewal", err)
	}
	k := owners[3].network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	_, active, err := owners[3].network.receiver.currentLocked()
	if err != nil || active.certificate.request.statement.Slot != 1 {
		t.Fatal("reused old prefix challenge", err)
	}
}
