package security

import (
	"context"
	"errors"
	"time"
)

const s3aQueueOverhead uint64 = 64

type s3aDelivery struct {
	raw []byte
}

type s3aTransition struct {
	ctx   context.Context
	check func() error
	apply func() ([]s2cOutbox, error)
	done  chan error
}

// A bounded FIFO serializes transitions. Each authenticated peer has at most
// one admitted request, so it cannot fill the FIFO ahead of all other peers.
// A separate maximum-outbox staging credit cannot be occupied by an unavailable
// peer's delivery cache. FIFO admission therefore includes self and replies.
func (o *s3aOwner) transition(ctx context.Context, check func() error, apply func() ([]s2cOutbox, error)) error {
	if !o.enterCall() {
		return errS3AClosed
	}
	defer o.calls.Done()
	if err := o.check(ctx); err != nil {
		return err
	}
	r := s3aTransition{ctx: ctx, check: check, apply: apply, done: make(chan error, 1)}
	select {
	case <-o.ctx.Done():
		return errS3AClosed
	case <-ctx.Done():
		return ctx.Err()
	case o.requests <- r:
	default:
		return errS3ACredit
	}
	select {
	case err := <-r.done:
		return err
	case <-ctx.Done():
		return ctx.Err() // Not a claim that an entered durable operation aborted.
	case <-o.ctx.Done():
		return errS3AClosed
	}
}

func (o *s3aOwner) transitions() {
	defer o.workers.Done()
	for {
		select {
		case <-o.ctx.Done():
			return
		case r := <-o.requests:
			r.done <- o.applyTransition(r)
		}
	}
}

func (o *s3aOwner) applyTransition(r s3aTransition) (err error) {
	defer func() {
		if v := recover(); v != nil {
			o.fail()
			err = errS2CUnknown
		}
	}()
	if err := o.check(r.ctx); err != nil {
		return err
	}
	if r.check != nil {
		if err := r.check(); err != nil {
			return err
		}
	}
	// Reserve the ENTIRE configured maximum outbox and one queue envelope per
	// voter before calling a mutating kernel method, regardless of message kind.
	reserve := o.kernel.config.OutboxBytes + uint64(len(o.peers))*s3aQueueOverhead
	o.queueMu.Lock()
	if o.staged != 0 {
		o.queueMu.Unlock()
		o.fail()
		return errS2CUnknown
	}
	o.staged = reserve
	o.queueMu.Unlock()
	defer func() {
		o.queueMu.Lock()
		o.staged = 0
		o.queueMu.Unlock()
	}()
	// Closing/expiry may have occurred while acquiring credit.
	if err := o.check(r.ctx); err != nil {
		return err
	}
	if r.check != nil {
		if err := r.check(); err != nil {
			return err
		}
	}
	out, err := r.apply()
	if err != nil {
		if errors.Is(err, errS2CUnknown) || errors.Is(err, errS2CClosed) {
			o.fail()
		}
		return err
	}
	var bytes uint64
	seen := map[uint32]bool{}
	for _, item := range out {
		_, known := o.peers[item.To]
		if !known || seen[item.To] || len(item.Bytes) == 0 || uint64(len(item.Bytes)) > o.kernel.trust.bounds.PayloadBytes {
			o.fail()
			return errS2CUnknown
		}
		seen[item.To] = true
		bytes += uint64(len(item.Bytes)) + s3aQueueOverhead
	}
	if bytes > reserve {
		o.fail()
		return errS2CUnknown
	}
	o.queueMu.Lock()
	// Partition pending bytes by destination, including self. valid guarantees
	// floor(Q/N) >= maximum frame + envelope, so an empty destination queue can
	// always admit its next valid frame. A slow peer cannot borrow that room by
	// retaining many copies, regardless of the kernel's broadcast order. The
	// division remainder stays unused; total pending ownership remains <= Q.
	peerBudget := o.limits.QueueBytes / uint64(len(o.peers))
	for _, item := range out {
		charge := uint64(len(item.Bytes)) + s3aQueueOverhead
		var peerQueued uint64
		for _, pending := range o.queues[item.To] { // At most PerPeerQueue <= 32.
			peerQueued += uint64(len(pending.raw)) + s3aQueueOverhead
		}
		if len(o.queues[item.To]) >= o.limits.PerPeerQueue || charge > peerBudget || peerQueued > peerBudget-charge {
			// Full staging credit already owned every produced byte before the
			// transition. This bounded, disposable delivery cache may drop a
			// datagram just like a failed network send. It acknowledges no vote,
			// choice or applied result. The same exact bytes are recoverable via
			// Retry, repeated Prepare/Accept, or retained ExportChosen history.
			o.dropped++
			continue
		}
		o.queues[item.To] = append(o.queues[item.To], s3aDelivery{raw: item.Bytes})
		o.queued += charge
		select {
		case o.queueWake[item.To] <- struct{}{}:
		default:
		}
	}
	o.queueMu.Unlock()
	// Durable work and delivery credit survive cancellation until consumed by
	// a worker; cancellation only prevents publishing this transport result.
	if err := o.check(r.ctx); err != nil {
		return err
	}
	if r.check != nil {
		return r.check()
	}
	return nil
}

func (o *s3aOwner) takeDelivery(id uint32) (s3aDelivery, bool) {
	o.queueMu.Lock()
	defer o.queueMu.Unlock()
	q := o.queues[id]
	if len(q) == 0 {
		return s3aDelivery{}, false
	}
	d := q[0]
	q[0] = s3aDelivery{}
	o.queues[id] = q[1:]
	o.queued -= uint64(len(d.raw)) + s3aQueueOverhead
	// Ownership transfers into one separately bounded active-RPC slot for this
	// peer, including self. The bytes are still counted in that fixed budget.
	// Never wait for self Receive while holding its outgoing queue credit.
	return d, true
}

func (o *s3aOwner) deliveries(id uint32) {
	defer o.workers.Done()
	for {
		if o.ctx.Err() != nil {
			return
		}
		d, ok := o.takeDelivery(id)
		if !ok {
			select {
			case <-o.ctx.Done():
				return
			case <-o.queueWake[id]:
				continue
			}
		}
		// Each peer owns one worker/active slot. An unavailable peer can hold it
		// only for this bounded number of timed attempts; it cannot retain every
		// transition reservation forever. Other peers keep independent workers.
		for attempt := 0; attempt < o.limits.Attempts; attempt++ {
			ctx, cancel := context.WithTimeout(o.ctx, o.limits.RPCTimeout)
			err := o.check(ctx)
			if err == nil && o.hooks != nil && o.hooks.beforeSend != nil {
				err = o.hooks.beforeSend(ctx, id, d.raw)
			}
			if err == nil {
				if id == o.kernel.config.Member {
					err = o.receive(ctx, id, d.raw, nil)
				} else {
					err = o.send(ctx, id, d.raw)
				}
			}
			cancel()
			if err == nil || o.ctx.Err() != nil {
				break
			}
			if attempt+1 < o.limits.Attempts && !s3aPause(o.ctx, o.limits.Backoff) {
				return
			}
		}
		// Delivery-cache disposal does not release any durable P/B obligation.
		// Retry/repeated requests and ExportChosen regenerate exact signed bytes.
	}
}

func s3aPause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (o *s3aOwner) receive(ctx context.Context, sender uint32, raw []byte, check func() error) error {
	m, err := s2cDecodeMessage(o.kernel.trust, raw)
	if err != nil || m.kind != s2cChosen && m.sender != sender {
		return errS3AWire
	}
	return o.transition(ctx, check, func() ([]s2cOutbox, error) { return o.kernel.Receive(raw) })
}

func (o *s3aOwner) Begin(ctx context.Context, candidate [32]byte) error {
	if err := o.enterLocal(ctx); err != nil {
		return err
	}
	defer func() { <-o.localSlot }()
	return o.transition(ctx, nil, func() ([]s2cOutbox, error) {
		_, receipt, err := o.kernel.Floors()
		if err != nil {
			return nil, err
		}
		slot := receipt.ControlSlot + 1
		if slot == o.ballotSlot && time.Now().Before(o.ballotReady) {
			return nil, errS3ACredit
		}
		out, err := o.kernel.Begin(candidate)
		if err == nil {
			o.ballotSlot = slot
			o.ballotReady = time.Now().Add(o.limits.BallotBackoff)
		}
		return out, err
	})
}

func (o *s3aOwner) Retry(ctx context.Context) error {
	if err := o.enterLocal(ctx); err != nil {
		return err
	}
	defer func() { <-o.localSlot }()
	return o.transition(ctx, nil, o.kernel.Retry)
}

// Local callers get one initiation slot, just as each remote voter gets one
// inbound slot. Together with one self worker and one range consumer, this
// prevents a local retry loop from filling all transition-admission capacity.
func (o *s3aOwner) enterLocal(ctx context.Context) error {
	if err := o.check(ctx); err != nil {
		return err
	}
	select {
	case o.localSlot <- struct{}{}:
		return nil
	default:
		return errS3ACredit
	}
}
