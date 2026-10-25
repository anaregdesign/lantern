package security

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Native journals and the actual configured worker own this transition. The
// private clock is deterministic; this is not configured-source qualification.
func TestConfiguredOwnerStartupRecoveryPrecedesReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	_, owners, _ := authorityTestCompositeConfiguredMembers(t, 1, 1, nil, configuredIntent(t), false)
	o := owners[1]
	k := o.network.kernel
	public := &CurrentAuthority{origin: o}
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	// Fix the hook before the first consensus send. Only this atomic switch
	// changes while delivery workers can observe it.
	o.network.hooks.beforeSend = func(ctx context.Context, _ uint32, raw []byte) error {
		if !armed.Load() || len(raw) <= len(s2cMessageMagic) || raw[len(s2cMessageMagic)] != s2cAccept {
			return nil
		}
		enterOnce.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	statement := configuredOwnerStatement(k)
	votes, err := o.collectConfiguredEndorsements(ctx, statement)
	if err != nil {
		t.Fatal("initial configured endorsements", err)
	}
	value, err := o.sealConfigured(ctx, statement, votes)
	if err != nil {
		t.Fatal("initial configured seal", err)
	}
	if _, err = o.network.Drive(ctx, value); err != nil {
		t.Fatal("initial matching configuration", err)
	}
	// A pending startup fence may refuse use of this lease, but it must not
	// prevent the real renewal protocol from collecting and installing it.
	renewErr := o.network.renewAuthority(ctx)
	k.gate.Lock()
	initialSlot, initialCut := k.replayState.slot, k.replayState.projection.cut
	active := o.network.receiver.active
	leaseSlot := uint64(0)
	if active != nil {
		leaseSlot = active.certificate.request.statement.Slot
	}
	genuineLease := active != nil && leaseSlot == initialSlot && len(active.certificate.votes) == 1
	k.gate.Unlock()
	if !genuineLease {
		t.Fatal("matching singleton did not install a genuine current lease", renewErr)
	}
	armed.Store(true)
	done := make(chan struct{})
	o.network.workers.Add(1)
	go func() { defer close(done); o.configuredLoop() }()
	select {
	case <-entered:
	case <-done:
		t.Fatal("configured worker exited before startup recovery")
	case <-ctx.Done():
		t.Fatal("startup recovery did not reach blocked native ACCEPT", ctx.Err())
	}
	ready := public.Ready(ctx)
	k.gate.Lock()
	t.Logf("blocked startup recovery: Ready=%v native_slot=%d lease_slot=%d", ready, k.replayState.slot, o.network.receiver.active.certificate.request.statement.Slot)
	k.gate.Unlock()
	if ready {
		t.Error("startup recovery exposed Ready before its planned initial Drive")
	}
	if _, err := public.Admit(ctx, authorityFakeCredentialProducer{}); err == nil {
		t.Error("startup recovery admitted a credential before its planned initial Drive")
	}
	unblock()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	drained := false
	for {
		k.gate.Lock()
		slot, cut := k.replayState.slot, k.replayState.projection.cut
		if !drained && slot > initialSlot && k.chosen == nil {
			drained = true
			_, _, currentErr := o.network.receiver.currentLocked()
			currentLeaseSlot := uint64(0)
			if current := o.network.receiver.active; current != nil {
				currentLeaseSlot = current.certificate.request.statement.Slot
			}
			t.Logf("drained startup recovery: same_cut=%v native_slot=%d lease_slot=%d current_error=%v", cut == initialCut, slot, currentLeaseSlot, currentErr)
			if slot != initialSlot+1 || cut != initialCut {
				t.Error("startup recovery changed semantic content or consumed multiple slots")
			}
		}
		k.gate.Unlock()
		if drained && public.Ready(ctx) {
			if _, err := public.Admit(ctx, authorityFakeCredentialProducer{}); err != nil {
				t.Fatal("startup recovery did not restore current admission", err)
			}
			return
		}
		select {
		case <-done:
			t.Fatal("configured worker exited before publishing recovered readiness")
		case <-ctx.Done():
			t.Fatal("startup recovery did not autonomously renew the recovered native prefix", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestConfiguredStartupFenceUsesFixedProposerMembership(t *testing.T) {
	for _, test := range []struct {
		name                 string
		configured           bool
		count, origins       int
		nonproposingObserver bool
	}{
		{name: "configured-origin-free-proposer", configured: true, count: 3, origins: 2},
		{name: "configured-nonproposing-observer", configured: true, count: 3, origins: 2, nonproposingObserver: true},
		{name: "independent-profile-without-config", count: 1, origins: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var desired *ConfiguredTrust
			if test.configured {
				desired = configuredIntent(t)
			}
			_, owners, _ := authorityTestCompositeConfiguredMembers(t, test.count, test.origins, nil, desired, test.nonproposingObserver)
			for id, o := range owners {
				k := o.network.kernel
				k.gate.Lock()
				member, known := k.trust.member(id)
				pending := o.network.receiver.startupRecoveryPending
				ownedOrigin := k.config.OwnedOrigin
				k.gate.Unlock()
				if !known || pending != (test.configured && member.Proposer) {
					t.Errorf("member %d startup fence=%v; configured=%v proposer=%v known=%v", id, pending, test.configured, member.Proposer, known)
				}
				if int(id) > test.origins && (ownedOrigin != 0 || o.descriptor != (s2cOriginDescriptor{}) || len(o.key) != 0) {
					t.Fatal("origin-free fixed member acquired a consume origin", id)
				}
			}
			if !test.configured {
				o := owners[1]
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				if err := o.network.renewAuthority(ctx); err != nil || !(&CurrentAuthority{origin: o}).Ready(ctx) {
					t.Fatal("independent profile without configured trust acquired a startup fence", err)
				}
			}
		})
	}
}

// Cancel a real in-flight Drive, rather than treating a delivery retry or a
// ballot increment as proof that a scheduling attempt failed.
func TestConfiguredStartupCancelledDriveKeepsFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, owners, _ := authorityTestCompositeConfiguredMembers(t, 1, 1, nil, configuredIntent(t), false)
	o := owners[1]
	k := o.network.kernel
	public := &CurrentAuthority{origin: o}
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	o.network.hooks.beforeSend = func(ctx context.Context, _ uint32, raw []byte) error {
		if !armed.Load() || len(raw) <= len(s2cMessageMagic) || raw[len(s2cMessageMagic)] != s2cAccept {
			return nil
		}
		enterOnce.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	statement := configuredOwnerStatement(k)
	votes, err := o.collectConfiguredEndorsements(ctx, statement)
	if err != nil {
		t.Fatal("initial configured endorsements", err)
	}
	value, err := o.sealConfigured(ctx, statement, votes)
	if err != nil {
		t.Fatal("initial configured seal", err)
	}
	if _, err := o.network.Drive(ctx, value); err != nil {
		t.Fatal("initial matching configuration", err)
	}
	_ = o.network.renewAuthority(ctx)
	k.gate.Lock()
	initialSlot := k.replayState.slot
	active := o.network.receiver.active
	installedLease := active != nil && active.certificate.request.statement.Slot == initialSlot
	k.gate.Unlock()
	if !installedLease {
		t.Fatal("failed-drive fixture lacks a genuine current lease")
	}
	armed.Store(true)
	driveCtx, cancelDrive := context.WithCancel(ctx)
	defer cancelDrive()
	done := make(chan error, 1)
	go func() {
		_, err := o.network.Drive(driveCtx, [32]byte{})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("Drive ended before blocked ACCEPT", err)
	case <-ctx.Done():
		t.Fatal("Drive did not reach blocked ACCEPT", ctx.Err())
	}
	cancelDrive()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled native Drive reported success")
		}
	case <-ctx.Done():
		t.Fatal("canceled native Drive did not join", ctx.Err())
	}
	k.gate.Lock()
	pending, slot := o.network.receiver.startupRecoveryPending, k.replayState.slot
	_, _, currentErr := o.network.receiver.currentLocked()
	retainedLease := o.network.receiver.active == active
	k.gate.Unlock()
	if !pending || slot != initialSlot || !retainedLease || currentErr == nil || public.Ready(ctx) {
		t.Fatalf("failed Drive released startup fence: pending=%v slot=%d initial=%d retained_lease=%v current_error=%v", pending, slot, initialSlot, retainedLease, currentErr)
	}
}
