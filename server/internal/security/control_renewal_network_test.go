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
	for _, occupied := range []bool{false, true} {
		name := "ordinary_delivery"
		if occupied {
			name = "occupied_source_ingress"
		}
		t.Run(name, func(t *testing.T) {
			n, owners, _ := authorityTestComposite(t)
			n.nodes[1].hooks.beforeSend = func(_ context.Context, to uint32, raw []byte) error {
				if to == 3 && raw[len(s2cMessageMagic)] == s2cChosen {
					return errS3AWire
				}
				return nil
			}
			ctx := s3aTestContext(t)
			if _, err := n.nodes[1].Drive(ctx, [32]byte{}); err != nil {
				t.Fatal("source installation", err)
			}
			_, source, err := n.nodes[1].kernel.ReadLocalCut()
			if err != nil || source.ControlSlot != 1 {
				t.Fatal("source cut", source.ControlSlot, err)
			}
			_, before, err := n.nodes[3].kernel.ReadLocalCut()
			if err != nil || before.ControlSlot != 0 {
				t.Fatal("fixture did not lose chosen notification", before.ControlSlot, err)
			}
			if err := n.nodes[3].renewAuthority(ctx); err == nil {
				t.Fatal("old installed head renewed")
			}
			if occupied {
				// Model a real authenticated handler paused after source apply
				// and before releasing its existing per-sender ingress credit.
				// Drive guarantees local installation, not remote handler exit.
				credit := n.nodes[1].inbound[3]
				select {
				case credit <- struct{}{}:
				case <-ctx.Done():
					t.Fatal("acquire source ingress", ctx.Err())
				}
				func() {
					defer func() { <-credit }()
					if err := n.nodes[3].renewAndCatchUp(ctx, 1); err == nil {
						t.Fatal("recovery succeeded with source ingress occupied")
					}
					k := owners[3].network.kernel
					k.gate.Lock()
					slot := k.replayState.slot
					_, _, currentErr := owners[3].network.receiver.currentLocked()
					k.gate.Unlock()
					if slot != 0 || currentErr == nil {
						t.Fatalf("occupied ingress admitted stale authority: slot=%d current_error=%v", slot, currentErr)
					}
				}()
			}
			// Progress the same real recovery operation within the existing
			// deadline. Transient ingress occupancy is not an authority grant.
			for cycles := 1; ; cycles++ {
				err := n.nodes[3].renewAndCatchUp(ctx, 1)
				if err == nil {
					break
				}
				if !s3aPause(ctx, 20*time.Millisecond) {
					_, recovered, readErr := n.nodes[3].kernel.ReadLocalCut()
					t.Fatalf("range recovery then fresh renewal exhausted: cycles=%d source_slot=%d recovered_slot=%d read_error=%v recovery_error=%v context=%v", cycles, source.ControlSlot, recovered.ControlSlot, readErr, err, ctx.Err())
				}
			}
			_, recovered, err := n.nodes[3].kernel.ReadLocalCut()
			if err != nil || recovered.ControlSlot != source.ControlSlot || recovered.ControlPrefix != source.ControlPrefix || recovered.CapsuleDigest != source.CapsuleDigest {
				t.Fatal("recovery did not install the exact source cut", err)
			}
			k := owners[3].network.kernel
			k.gate.Lock()
			defer k.gate.Unlock()
			_, active, err := owners[3].network.receiver.currentLocked()
			if err != nil || active == nil {
				t.Fatal("recovered head has no current authority", err)
			}
			statement := active.certificate.request.statement
			if statement.Slot != source.ControlSlot || statement.Prefix != source.ControlPrefix || statement.Capsule != source.CapsuleDigest {
				t.Fatal("fresh certificate does not match the exact recovered source cut")
			}
		})
	}
}
