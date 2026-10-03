package security

import (
	"context"
	"time"
)

// CheckpointProof is minted only after matching a fresh owner-signed renewal
// response to this receiver's outstanding process-local challenge. A signed
// historical image alone cannot mint one. The proof cannot cross Stores.
type CheckpointProof struct {
	receiver     *LeaseReceiver
	serial       uint64
	lease        authorityLease
	deadline     time.Time
	wallDeadline int64
}

func (r *LeaseReceiver) ProveCheckpoint(encodedLease []byte) (*CheckpointProof, error) {
	lease, err := decodeAuthorityLease(encodedLease, r.key)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now, valid := r.clockNowLocked()
	if !valid || r.pending == nil || r.pending.request != lease.request {
		return nil, ErrInvalidLease
	}
	pending := r.pending
	r.pending = nil
	if lease.generation != r.store.generation || lease.issuedAt.UnixNano() > now.Add(r.margin).UnixNano() {
		return nil, ErrInvalidLease
	}
	deadline := pending.started.Add(lease.lifetime - r.margin)
	wallDeadline := min(deadline.UnixNano(), lease.issuedAt.Add(lease.lifetime-r.margin).UnixNano())
	if !now.Before(deadline) || now.UnixNano() >= wallDeadline {
		return nil, ErrAuthorityUnavailable
	}
	return &CheckpointProof{receiver: r, serial: r.serial, lease: lease, deadline: deadline, wallDeadline: wallDeadline}, nil
}
func (p *CheckpointProof) check(ctx context.Context, store *Store, revision *Revision) error {
	if p == nil || p.receiver == nil || p.receiver.store != store || revision == nil || p.lease.generation != revision.generation || p.lease.digest != revision.digest || p.lease.revision != revision.sequence {
		return ErrAuthorityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r := p.receiver
	r.mu.Lock()
	defer r.mu.Unlock()
	now, valid := r.clockNowLocked()
	if !valid || r.serial != p.serial || !now.Before(p.deadline) || now.UnixNano() >= p.wallDeadline {
		return ErrAuthorityUnavailable
	}
	return nil
}

// ActivateCheckpoint runs after atomic durable installation of the exact cut.
// Expiry during persistence leaves the installed image recovery-only until a
// new challenge succeeds; it never falls back to an older granting lease.
func (r *LeaseReceiver) ActivateCheckpoint(proof *CheckpointProof) error {
	current, ok := r.store.Current()
	if !ok {
		return ErrAuthorityUnavailable
	}
	if err := proof.check(context.Background(), r.store, current); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now, valid := r.clockNowLocked()
	if !valid || r.serial != proof.serial || !now.Before(proof.deadline) || now.UnixNano() >= proof.wallDeadline {
		return ErrAuthorityUnavailable
	}
	lease := proof.lease
	r.lease = &lease
	r.deadline = proof.deadline
	r.wallDeadline = proof.wallDeadline
	return nil
}
