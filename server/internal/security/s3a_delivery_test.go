package security

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestS3ADeliverySaturatedPeerSelfCreditAndEventualQuorum(t *testing.T) {
	var blocked atomic.Int64
	n := s3aTestCluster(t, func(_ uint32, c *s3aConfig) {
		c.Limits.PerPeerQueue = 1
		c.Limits.QueueBytes = c.Participant.OutboxBytes + 3*s3aQueueOverhead
		c.Limits.RPCTimeout = 80 * time.Millisecond
		c.Limits.Attempts = 1
		c.Limits.Rounds = 64
		c.Limits.Backoff = 20 * time.Millisecond
		c.hooks = &s3aHooks{beforeSend: func(ctx context.Context, to uint32, _ []byte) error {
			if to == 3 {
				blocked.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}}
	})
	n.startAll()
	ctx := s3aTestContext(t)
	if receipt, err := n.nodes[1].Drive(ctx, [32]byte{}); err != nil || receipt.ControlSlot != 1 {
		for id, o := range n.nodes {
			o.kernel.gate.Lock()
			t.Logf("voter=%d counter=%d prepare=%v selected=%v promise=%v accepted=%v promises=%d votes=%d slot=%d", id, o.kernel.counter, o.kernel.prepare != nil, o.kernel.selected != nil, o.kernel.promise != nil, o.kernel.accepted != nil, len(o.kernel.promises), len(o.kernel.votes), o.kernel.replayState.slot)
			o.kernel.gate.Unlock()
			o.queueMu.Lock()
			for peer, q := range o.queues {
				t.Logf("voter=%d queuedTo=%d count=%d queuedBytes=%d", id, peer, len(q), o.queued)
			}
			o.queueMu.Unlock()
		}
		t.Fatal("reachable quorum blocked by unavailable peer/self credit", receipt, err)
	}
	if blocked.Load() == 0 {
		t.Fatal("fault did not exercise unavailable peer")
	}
	for _, o := range n.nodes {
		o.queueMu.Lock()
		if o.queued > o.limits.QueueBytes {
			t.Fatal("byte budget exceeded")
		}
		for _, q := range o.queues {
			if len(q) > o.limits.PerPeerQueue {
				t.Fatal("peer bound exceeded")
			}
		}
		o.queueMu.Unlock()
	}
	// All three transports remain authenticated, but only the reachable quorum
	// received voting traffic; intact laggard catches up a QC from another peer.
	n.waitCut(2, 1)
	if count, err := n.nodes[3].CatchUp(ctx, 2); err != nil || count != 1 {
		t.Fatal("authenticated QC relay from non-proposer", count, err)
	}
	n.waitCut(3, 1)
}

func TestS3ADeliveryCancellationDoesNotReleaseEnteredDurability(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o := n.start(1)
	floor, err := o.Floors()
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	o.kernel.hooks = &s2cParticipantHooks{beforePAppend: func(kind byte) {
		if kind == s2cPBallot {
			once.Do(func() { close(entered) })
			<-release
		}
	}}
	ctx, cancel := context.WithCancel(t.Context())
	began := make(chan error, 1)
	go func() { began <- o.Begin(ctx, [32]byte{}) }()
	<-entered
	if err := o.Retry(t.Context()); !errors.Is(err, errS3ACredit) {
		t.Fatal("second local initiator bypassed bounded admission", err)
	}
	cancel()
	if err := <-began; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	o.queueMu.Lock()
	reserved := o.staged
	o.queueMu.Unlock()
	if reserved != o.kernel.config.OutboxBytes+3*s3aQueueOverhead {
		t.Fatal("entered transition lost credit", reserved)
	}
	closed := make(chan error, 1)
	go func() { closed <- o.Close() }()
	select {
	case <-closed:
		t.Fatal("Close escaped entered kernel transition")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if o.queued != 0 || o.staged != 0 {
		t.Fatal("credit released more/less than once", o.queued, o.staged)
	}
	resumed, err := resumeS3AOwner(n.configs[1], floor)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	actual, err := resumed.Floors()
	if err != nil || actual.P.Index <= floor.P.Index {
		t.Fatal("cancellation undid retained ballot", actual, err)
	}
}

func TestS3ADeliveryUncertainNativeIOClosesAllOutwardAPIs(t *testing.T) {
	n := s3aTestCluster(t, nil)
	o := n.start(1)
	var fired bool
	o.kernel.hooks = &s2cParticipantHooks{beforePAppend: func(kind byte) {
		if kind == s2cPBallot {
			fired = true
			_ = o.kernel.p.tip.Close()
		}
	}}
	if err := o.Begin(t.Context(), [32]byte{}); err == nil || !fired {
		t.Fatal("native tip fault not observed", fired, err)
	}
	if !o.closed.Load() {
		t.Fatal("uncertain kernel left network open")
	}
	if o.Retry(t.Context()) == nil {
		t.Fatal("retry reopened")
	}
	if _, err := o.Floors(); err == nil {
		t.Fatal("uncertain floors published")
	}
	if _, err := o.CatchUp(t.Context(), 2); err == nil {
		t.Fatal("catchup reopened")
	}
	if _, err := o.Drive(t.Context(), [32]byte{}); err == nil {
		t.Fatal("driver reopened")
	}
	if o.Refresh(n.configs[1].Manifest) == nil {
		t.Fatal("refresh reopened")
	}
	_ = o.Close()
}
