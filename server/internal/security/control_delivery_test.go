package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestS3ADeliveryBoundedRenewalTurn(t *testing.T) {
	n := s3aTestCluster(t, func(_ uint32, c *s3aConfig) {
		clock, _ := fakeAuthorityTimeOwnerAt(t, c.Membership.Now())
		if err := bindAuthorityNetworkTime(c, clock); err != nil {
			t.Fatal(err)
		}
	})
	o, err := createS3AOwner(n.configs[1])
	if err != nil {
		t.Fatal(err)
	}
	n.nodes[1] = o
	ctx := s3aTestContext(t)
	request, err := o.receiver.challenge()
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var completed atomic.Int32
	first := s3aTransition{ctx: ctx, done: make(chan error, 1), apply: func() ([]s2cOutbox, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		completed.Add(1)
		return nil, nil
	}}
	o.requests <- first
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var later []chan error
	for range 12 {
		done := make(chan error, 1)
		later = append(later, done)
		o.requests <- s3aTransition{ctx: ctx, done: done, apply: func() ([]s2cOutbox, error) { completed.Add(1); return nil, nil }}
	}
	var observed atomic.Int32
	result := make(chan authorityRenewalResult, 1)
	o.renewals <- authorityRenewalTask{ctx: ctx, sender: 1, raw: request, check: func() error { observed.Store(completed.Load()); return nil }, release: func() {}, done: result}
	close(release)
	select {
	case got := <-result:
		if got.err != nil || len(got.raw) != 68 || observed.Load() != authorityConsensusBurst {
			t.Fatal("renewal starved or stole consensus share", observed.Load(), got.err)
		}
	case <-ctx.Done():
		t.Fatal("renewal starved", ctx.Err())
	}
	for _, done := range append(later, first.done) {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

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
	relay := s3aTestPrepareRelay(t, n)
	count, err := n.nodes[3].CatchUp(ctx, 2)
	t.Logf("single authenticated QC relay observation: count=%d err=%v", count, err)
	if err := s3aTestReconcileRelay(ctx, n.nodes[3], relay, count, err); err != nil {
		t.Fatal("authenticated QC relay from non-proposer", err)
	}
}

type s3aTestRelayProof struct {
	prefix           [32]byte
	chosen           []byte
	entered, drained chan struct{}
}

// Synchronize cancellation after reconciliation's first live-context check;
// otherwise an immediate cancel could test only the already-cancelled branch.
type s3aTestRelayContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *s3aTestRelayContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func s3aTestPrepareRelay(t *testing.T, n *s3aTestNetwork) s3aTestRelayProof {
	t.Helper()
	_, before, err := n.nodes[3].kernel.ReadLocalCut()
	if err != nil || before.ControlSlot != 0 {
		t.Fatal("relay requires a healthy empty laggard", before, err)
	}
	_, source, err := n.nodes[2].kernel.ReadLocalCut()
	if err != nil || source.ControlSlot != 1 {
		t.Fatal("relay source requires exactly slot 1", source, err)
	}
	chosen, err := n.nodes[2].kernel.ExportChosen(1, 1, n.nodes[2].limits.RangeBytes)
	if err != nil || len(chosen) != 1 {
		t.Fatal("relay source QC missing", len(chosen), err)
	}
	proof := s3aTestRelayProof{source.ControlPrefix, bytes.Clone(chosen[0]), make(chan struct{}), make(chan struct{})}
	var entered, drained sync.Once
	n.nodes[3].kernel.gate.Lock()
	n.nodes[3].kernel.hooks = &s2cParticipantHooks{
		afterChosen:  func() { entered.Do(func() { close(proof.entered) }) },
		afterDrained: func() { drained.Do(func() { close(proof.drained) }) },
	}
	n.nodes[3].kernel.gate.Unlock()
	return proof
}

// Count observes successful Receive returns, not every durable installation.
// Reconcile only this known one-slot relay; arbitrary errors/empty successful
// ranges cannot repair its proven empty starting state. No RPC is retried.
func s3aTestReconcileRelay(ctx context.Context, o *s3aOwner, proof s3aTestRelayProof, count uint64, callErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !(count == 1 && callErr == nil || count == 0 && callErr == context.DeadlineExceeded) {
		return fmt.Errorf("unexpected relay observation: count=%d err=%v", count, callErr)
	}
	// afterChosen runs after the P append, so an entered native operation may
	// still be pending when the RPC deadline expires. Observe both witnesses
	// within waitCut's existing five-second budget and the outer deadline.
	observation, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-proof.entered:
	case <-observation.Done():
		return fmt.Errorf("relay did not reach durable CHOSEN witness: %w", observation.Err())
	}
	select {
	case <-proof.drained:
	case <-observation.Done():
		return fmt.Errorf("relay did not finish DRAINED: %w", observation.Err())
	}
	// ReadLocalCut is not context-aware. Keep one gated observer, supervised by
	// the same budget; a timeout fails without claiming native work was aborted.
	result := make(chan error, 1)
	go func() {
		if err := o.check(observation); err != nil {
			result <- err
			return
		}
		_, cut, err := o.kernel.ReadLocalCut()
		if err != nil {
			result <- err
			return
		}
		if cut.ControlSlot != 1 || cut.ControlPrefix != proof.prefix {
			result <- errors.New("relay durable slot/prefix differs from source")
			return
		}
		chosen, err := o.kernel.ExportChosen(1, 1, o.limits.RangeBytes)
		if err != nil {
			result <- err
			return
		}
		if len(chosen) != 1 || !bytes.Equal(chosen[0], proof.chosen) {
			result <- errors.New("relay retained QC differs from source")
			return
		}
		result <- o.check(observation)
	}()
	select {
	case err := <-result:
		if expired := observation.Err(); expired != nil {
			return expired
		}
		return err
	case <-observation.Done():
		return fmt.Errorf("relay durable observation unresolved: %w", observation.Err())
	}
}

func TestS3ADeliveryRelayDeadlineRequiresExactDrainedEvidence(t *testing.T) {
	n := s3aTestCluster(t, func(_ uint32, c *s3aConfig) {
		c.Limits.PerPeerQueue = 1
		c.Limits.QueueBytes = c.Participant.OutboxBytes + 3*s3aQueueOverhead
		c.Limits.RPCTimeout = 80 * time.Millisecond
		c.Limits.Attempts = 1
		c.Limits.Rounds = 64
		c.Limits.Backoff = 20 * time.Millisecond
		c.hooks = &s3aHooks{beforeSend: func(ctx context.Context, to uint32, _ []byte) error {
			if to == 3 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}}
	})
	n.startAll()
	ctx := s3aTestContext(t)
	if receipt, err := n.nodes[1].Drive(ctx, [32]byte{}); err != nil || receipt.ControlSlot != 1 {
		t.Fatal("produce real chosen history", receipt, err)
	}
	n.waitCut(2, 1)
	proof := s3aTestPrepareRelay(t, n)
	beforeEntry, err := n.nodes[3].Floors()
	if err != nil {
		t.Fatal(err)
	}
	expired, cancelExpired := context.WithDeadline(ctx, time.Time{})
	preCount, preErr := n.nodes[3].CatchUp(expired, 2)
	cancelExpired()
	if preCount != 0 || preErr != context.DeadlineExceeded {
		t.Fatal("pre-entry deadline observation", preCount, preErr)
	}
	if err := s3aTestReconcileRelay(ctx, n.nodes[3], proof, preCount, preErr); err == nil {
		t.Fatal("deadline before native entry was accepted")
	}
	afterEntry, err := n.nodes[3].Floors()
	if err != nil || afterEntry != beforeEntry {
		t.Fatal("pre-entry deadline changed local P/B floors", afterEntry, beforeEntry, err)
	}
	t.Log("pre-entry deadline rejected with unchanged local P/B floors")
	appendHeld, appendRelease := make(chan struct{}), make(chan struct{})
	var appendOnce sync.Once
	releaseAppend := func() { appendOnce.Do(func() { close(appendRelease) }) }
	defer releaseAppend()
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseApply := func() { once.Do(func() { close(release) }) }
	defer releaseApply()
	n.nodes[3].kernel.gate.Lock()
	n.nodes[3].kernel.hooks.beforePAppend = func(kind byte) {
		if kind == s2cPChosen {
			close(appendHeld)
			<-appendRelease
		}
	}
	n.nodes[3].kernel.hooks.beforeB = func() { close(held); <-release }
	n.nodes[3].kernel.gate.Unlock()
	type relayResult struct {
		count uint64
		err   error
	}
	result := make(chan relayResult, 1)
	go func() { count, err := n.nodes[3].CatchUp(ctx, 2); result <- relayResult{count, err} }()
	select {
	case <-appendHeld:
	case got := <-result:
		t.Fatal("relay did not reach controlled native entry", got.count, got.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var got relayResult
	select {
	case got = <-result:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got.count != 0 || got.err != context.DeadlineExceeded {
		t.Fatal("entered relay did not expose natural deadline", got.count, got.err)
	}
	select {
	case <-proof.entered:
		t.Fatal("CHOSEN witness published before native append")
	default:
	}
	// The kernel has entered native work, but afterChosen cannot fire until
	// the P append completes. Observe cancellation after the first live check
	// to reject the old immediate missing-witness assertion deterministically.
	appendCtx, cancelAppend := context.WithCancel(ctx)
	appendObserved := &s3aTestRelayContext{Context: appendCtx, checked: make(chan struct{})}
	appendPending := make(chan error, 1)
	go func() { appendPending <- s3aTestReconcileRelay(appendObserved, n.nodes[3], proof, got.count, got.err) }()
	select {
	case <-appendObserved.checked:
	case <-ctx.Done():
		cancelAppend()
		t.Fatal(ctx.Err())
	}
	cancelAppend()
	if err := <-appendPending; !errors.Is(err, context.Canceled) {
		t.Fatal("pending P append did not retain bounded witness observation", err)
	}
	releaseAppend()
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Log("single native relay returned 0/DeadlineExceeded before CHOSEN witness, then reached held B")
	// CHOSEN entry alone cannot pass while native completion remains held. Do
	// not synchronously read the gated cut behind this controlled barrier.
	pendingCtx, cancelPending := context.WithCancel(ctx)
	observed := &s3aTestRelayContext{Context: pendingCtx, checked: make(chan struct{})}
	pending := make(chan error, 1)
	go func() { pending <- s3aTestReconcileRelay(observed, n.nodes[3], proof, got.count, got.err) }()
	select {
	case <-observed.checked:
	case <-ctx.Done():
		cancelPending()
		t.Fatal(ctx.Err())
	}
	cancelPending()
	if err := <-pending; err == nil {
		t.Fatal("pending CHOSEN was mistaken for durable completion")
	}
	releaseApply()
	if err := s3aTestReconcileRelay(ctx, n.nodes[3], proof, got.count, got.err); err != nil {
		t.Fatal("entered deadline did not reconcile exact DRAINED cut/QC", err)
	}
	t.Log("deadline reconciled only after DRAINED, exact slot 1/prefix/QC and eligibility")
	before, err := n.nodes[3].Floors()
	if err != nil {
		t.Fatal(err)
	}
	count, retryErr := n.nodes[3].CatchUp(ctx, 2)
	if count != 0 || retryErr != nil && retryErr != context.DeadlineExceeded {
		t.Fatal("completed relay retry observation", count, retryErr)
	}
	// A fresh call starts at installed slot+1. Check its native empty-range
	// consequence without imposing a new 80ms success guarantee on every RPC.
	empty, err := n.nodes[2].kernel.ExportChosen(2, 1, n.nodes[2].limits.RangeBytes)
	if err != nil || len(empty) != 0 {
		t.Fatal("completed retry must request beyond retained history", len(empty), err)
	}
	parts, err := n.nodes[3].decodeRange(append([]byte(s3aRangeMagic), 0, 0, 0, 0), 2)
	if err != nil || len(parts) != 0 {
		t.Fatal("valid empty range cannot require an applied count", len(parts), err)
	}
	after, err := n.nodes[3].Floors()
	if err != nil || after != before {
		t.Fatal("completed retry changed local P/B floors", after, before, err)
	}
	if err := s3aTestReconcileRelay(ctx, n.nodes[3], proof, got.count, got.err); err != nil {
		t.Fatal("completed retry changed exact durable relay proof", err)
	}
	t.Logf("single retry after durable completion: count=%d err=%v; empty range and local floors unchanged", count, retryErr)

	wrongPrefix, wrongQC := proof, proof
	wrongPrefix.prefix[0] ^= 1
	wrongQC.chosen = bytes.Clone(proof.chosen)
	wrongQC.chosen[len(wrongQC.chosen)-1] ^= 1
	for _, tc := range []struct {
		name  string
		proof s3aTestRelayProof
		count uint64
		err   error
	}{
		{"wrong-prefix", wrongPrefix, 0, context.DeadlineExceeded},
		{"wrong-QC", wrongQC, 0, context.DeadlineExceeded},
		{"empty-success-from-cut-zero", proof, 0, nil},
		{"joined-unknown-deadline", proof, 0, errors.Join(context.DeadlineExceeded, errS2CUnknown)},
		{"wire-error", proof, 0, errS3AWire},
		{"cancelled-call", proof, 0, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s3aTestReconcileRelay(ctx, n.nodes[3], tc.proof, tc.count, tc.err); err == nil {
				t.Fatal("invalid relay evidence accepted")
			}
		})
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s3aTestReconcileRelay(cancelled, n.nodes[3], proof, 1, nil); err == nil {
		t.Fatal("outer cancellation converted to relay success")
	}
	n.clock.Store(n.manifest.ExpiresAt.UnixNano())
	if err := s3aTestReconcileRelay(ctx, n.nodes[3], proof, 1, nil); err == nil {
		t.Fatal("expired workload converted to relay success")
	}
	_ = n.nodes[3].Close()
	if err := s3aTestReconcileRelay(ctx, n.nodes[3], proof, 1, nil); err == nil {
		t.Fatal("closed owner converted to relay success")
	}
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

// Adapt the first-fit counterexample with its native SELECT/Retry and controlled
// worker turns. Only cache scheduling is controlled: the promises, acceptance,
// votes and choice all come from real native participants. This proves pending
// delivery opportunities at minimum Q, not an unbounded HTTP liveness claim.
func TestS3ADeliveryLowIDPartitionPreservesHealthyAndSelfOpportunity(t *testing.T) {
	n := s3aTestCluster(t, nil)
	for _, listener := range n.listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	bounds := n.f.trust.bounds
	bounds.HistoricalBytes = 16 << 10
	bounds.ProofBytes = 4 << 10
	bounds.PayloadBytes = 32 << 10
	var err error
	n.f.trust, err = newS2CTrust(s2cBootstrap{n.f.genesis, n.f.members, n.f.origins, bounds})
	if err != nil {
		t.Fatal(err)
	}
	n.manifest.Profile.ProtocolScope = n.f.trust.scope
	manifest := n.sign(n.manifest)
	maxFrame := min(bounds.PayloadBytes, uint64(s2cMessageOverhead)+bounds.ProofBytes+bounds.HistoricalBytes)
	for id, c := range n.configs {
		c.Participant.Trust = n.f.trust
		c.Participant.OutboxBytes = 3 * maxFrame
		c.Membership.Profile = n.manifest.Profile
		c.Manifest = manifest
		c.Limits.PerPeerQueue = 32
		c.Limits.QueueBytes = c.Participant.OutboxBytes + 3*s3aQueueOverhead
		c.Limits.RangeBytes = bounds.PayloadBytes
		if !c.Limits.valid(c.Participant) {
			t.Fatal("regression must retain constructor-admitted minimum capacity", id)
		}
		n.configs[id] = c
	}
	o, err := createS3AOwner(n.configs[3])
	if err != nil {
		t.Fatal(err)
	}
	n.nodes[3] = o
	_, value := n.origin(3, 79)
	prepare, err := o.kernel.Begin(value)
	if err != nil {
		t.Fatal(err)
	}
	voters := map[uint32]*s2cParticipant{}
	for _, id := range []uint32{1, 2} {
		c := n.configs[id].Participant
		c.Key = n.f.keys[id]
		voter, err := createS2CParticipant(c)
		if err != nil {
			t.Fatal(err)
		}
		voters[id] = voter
		t.Cleanup(func() { _ = voter.Close() })
		var incoming []byte
		for _, item := range prepare {
			if item.To == id {
				incoming = item.Bytes
			}
		}
		promise, err := voter.Receive(incoming)
		if err != nil || len(promise) != 1 {
			t.Fatal("real durable Promise", id, err)
		}
		if _, err := o.kernel.Receive(promise[0].Bytes); err != nil {
			t.Fatal("real durable SELECT", err)
		}
	}
	retained, err := o.kernel.Retry()
	if err != nil || len(retained) != 3 {
		t.Fatal("retained SELECT broadcast", err)
	}
	for i, item := range retained {
		m, err := s2cDecodeMessage(n.f.trust, item.Bytes)
		if err != nil || m.kind != s2cAccept || item.To != uint32(i+1) || m.value != value || !bytes.Equal(item.Bytes, retained[0].Bytes) {
			t.Fatal("regression must use the exact sorted native Accept broadcast", err)
		}
	}
	charge := uint64(len(retained[0].Bytes)) + s3aQueueOverhead
	peerBudget := o.limits.QueueBytes / 3
	copies := peerBudget / charge
	if copies < 2 || copies > uint64(o.limits.PerPeerQueue) || charge <= uint64(s2cMessageOverhead)+s3aQueueOverhead {
		t.Fatal("fixture must retain multiple large native Accept copies", copies, charge)
	}
	// This shell owns only scheduling state. The original owner's idle workers
	// never receive its queues, and it retains sole native-resource ownership.
	p := &s3aOwner{kernel: o.kernel, membership: o.membership, identity: o.identity,
		limits: o.limits, now: o.now, peers: o.peers, ctx: o.ctx, cancel: o.cancel,
		queues: map[uint32][]s3aDelivery{}, queueWake: map[uint32]chan struct{}{}}
	for _, id := range []uint32{1, 2, 3} {
		p.queueWake[id] = make(chan struct{}, 1)
	}
	retry := func() {
		t.Helper()
		if err := p.applyTransition(s3aTransition{ctx: t.Context(), apply: o.kernel.Retry}); err != nil {
			t.Fatal("exact Retry transition", err)
		}
	}
	checkBounds := func() {
		t.Helper()
		var total uint64
		for id, pending := range p.queues {
			var used uint64
			for _, d := range pending {
				used += uint64(len(d.raw)) + s3aQueueOverhead
			}
			if len(pending) > p.limits.PerPeerQueue || used > peerBudget {
				t.Fatal("destination exceeded its count or byte partition", id, len(pending), used, peerBudget)
			}
			total += used
		}
		if total != p.queued || total > p.limits.QueueBytes || p.staged != 0 {
			t.Fatal("pending/staging accounting", total, p.queued, p.staged)
		}
	}
	drainSelf := func() {
		t.Helper()
		for processed := 0; ; processed++ {
			d, ok := p.takeDelivery(3)
			if !ok {
				return
			}
			if processed > 4 {
				t.Fatal("unexpected self-delivery cycle")
			}
			if err := p.applyTransition(s3aTransition{ctx: t.Context(), apply: func() ([]s2cOutbox, error) { return o.kernel.Receive(d.raw) }}); err != nil {
				t.Fatal("real self-delivery", err)
			}
		}
	}
	// Peer 1 starts one slow unavailable attempt. During this finite prelude,
	// peer 2 fails quickly and self processes its genuine Accept/Accepted. Try
	// enough copies to fill the old shared Q, not merely the new peer partition.
	var active s3aDelivery
	for i := uint64(0); i <= p.limits.QueueBytes/charge+1; i++ {
		retry()
		if i == 0 {
			var ok bool
			active, ok = p.takeDelivery(1)
			if !ok {
				t.Fatal("unavailable peer did not get its active slot")
			}
		}
		for {
			if _, ok := p.takeDelivery(2); !ok {
				break
			}
		}
		drainSelf()
		checkBounds()
	}
	if len(p.queues[1]) != int(copies) || p.queued != copies*charge || !bytes.Equal(active.raw, retained[0].Bytes) || p.dropped == 0 {
		t.Fatal("prelude must saturate only the unavailable peer's partition", len(p.queues[1]), p.queued, p.dropped)
	}
	beforeP, beforeB, err := o.kernel.Floors()
	if err != nil {
		t.Fatal(err)
	}
	initialDropped := p.dropped
	var reply []byte
	for cycle := 0; cycle < 6; cycle++ {
		// Each bounded peer-1 attempt completes, its worker takes another copy,
		// then sorted Retry runs before both healthy workers, as in the review.
		var ok bool
		active, ok = p.takeDelivery(1)
		if !ok || !bytes.Equal(active.raw, retained[0].Bytes) {
			t.Fatal("unavailable worker transfer", cycle)
		}
		retry()
		for _, id := range []uint32{2, 3} {
			d, ok := p.takeDelivery(id)
			if !ok || !bytes.Equal(d.raw, retained[0].Bytes) {
				t.Fatal("healthy/self lost its exact Accept delivery opportunity", cycle, id)
			}
			if id == 2 {
				accepted, err := voters[2].Receive(d.raw)
				if err != nil || len(accepted) != 1 || accepted[0].To != 3 {
					t.Fatal("healthy voter did not durably accept", cycle, err)
				}
				if reply != nil && !bytes.Equal(reply, accepted[0].Bytes) {
					t.Fatal("retry changed healthy voter's exact durable response")
				}
				reply = accepted[0].Bytes
				// Delay this genuine response for six finite cycles so the
				// stable Accept retry/cache state can be checked repeatedly.
			} else if err := p.applyTransition(s3aTransition{ctx: t.Context(), apply: func() ([]s2cOutbox, error) { return o.kernel.Receive(d.raw) }}); err != nil {
				t.Fatal("real self Accept", cycle, err)
			}
		}
		drainSelf()
		checkBounds()
		if p.queued != copies*charge || len(p.queues[1]) != int(copies) || p.dropped != initialDropped {
			t.Fatal("retry did not preserve fair bounded cache state", cycle, p.queued, p.dropped)
		}
	}
	afterP, afterB, err := o.kernel.Floors()
	if err != nil || beforeP != afterP || beforeB != afterB {
		t.Fatal("exact retries changed proposer P/B or consumed durable capacity", err)
	}
	// Complete the now-functioning quorum with the actual healthy reply; peer 1
	// stays unavailable and retains its saturated pending and active copies.
	if err := p.applyTransition(s3aTransition{ctx: t.Context(), apply: func() ([]s2cOutbox, error) { return o.kernel.Receive(reply) }}); err != nil {
		t.Fatal("healthy quorum response", err)
	}
	chosen, ok := p.takeDelivery(2)
	if !ok {
		t.Fatal("healthy destination lost chosen delivery opportunity")
	}
	if _, err := voters[2].Receive(chosen.raw); err != nil {
		t.Fatal("healthy chosen delivery", err)
	}
	drainSelf()
	checkBounds()
	for _, voter := range []*s2cParticipant{o.kernel, voters[2]} {
		_, receipt, err := voter.Floors()
		if err != nil || receipt.ControlSlot != 1 {
			t.Fatal("reachable quorum did not materialize the genuine choice", receipt, err)
		}
	}
	t.Logf("minimum QueueBytes=%d per-destination bytes=%d PerPeerQueue=%d; native Accept bytes=%d charge=%d; low-ID pending copies=%d plus one active; 6 fair cycles delivered 12 healthy/self Accepts with unchanged proposer P/B, then native quorum materialized slot 1", p.limits.QueueBytes, peerBudget, p.limits.PerPeerQueue, len(retained[0].Bytes), charge, copies)
}
