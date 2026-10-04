package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"time"
)

// LeaseReceiver keeps only one outstanding challenge and one process-local
// lease. Neither leases nor clock deadlines are recovered from persistence.
type LeaseReceiver struct {
	store          *Store
	clock          AuthorityClock
	key            ed25519.PublicKey
	receiver, boot [16]byte
	margin         time.Duration
	mu             sync.Mutex
	pending        *renewalChallenge
	serial         uint64
	lease          *authorityLease
	deadline       time.Time
	wallDeadline   int64
	lastWall       time.Time
	clockFault     bool
}
type renewalChallenge struct {
	request LeaseRequest
	started time.Time
}

func NewLeaseReceiver(store *Store, receiver [16]byte, key ed25519.PublicKey, clock AuthorityClock, margin time.Duration) (*LeaseReceiver, error) {
	if store == nil || receiver == [16]byte{} || len(key) != ed25519.PublicKeySize || margin <= 0 || margin > maxLeaseClockMargin {
		return nil, ErrInvalidLease
	}
	if clock == nil {
		clock = systemAuthorityClock{}
	}
	result := &LeaseReceiver{store: store, clock: clock, key: append(ed25519.PublicKey(nil), key...), receiver: receiver, margin: margin, lastWall: clock.Now()}
	if _, err := rand.Read(result.boot[:]); err != nil {
		return nil, err
	}
	return result, nil
}
func (r *LeaseReceiver) clockNowLocked() (time.Time, bool) {
	now := r.clock.Now()
	if now.UnixNano() < r.lastWall.UnixNano() {
		r.clockFault = true
	}
	r.lastWall = now
	return now, !r.clockFault
}
func (r *LeaseReceiver) Challenge() (LeaseRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now, valid := r.clockNowLocked()
	if !valid {
		return LeaseRequest{}, ErrAuthorityUnavailable
	}
	request := LeaseRequest{Receiver: r.receiver, BootNonce: r.boot}
	if _, err := rand.Read(request.Challenge[:]); err != nil {
		return LeaseRequest{}, err
	}
	r.serial++
	r.pending = &renewalChallenge{request: request, started: now}
	return request, nil
}
func (r *LeaseReceiver) Accept(encoded []byte) error {
	lease, err := decodeAuthorityLease(encoded, r.key)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now, valid := r.clockNowLocked()
	if !valid || r.pending == nil || lease.request != r.pending.request {
		return ErrInvalidLease
	}
	// Consume every matched response, including one that arrives before apply.
	// Retrying requires a new challenge and a new latency budget.
	pending := r.pending
	r.pending = nil
	current, ok := r.store.Current()
	if !ok || current.generation != lease.generation || current.sequence != lease.revision || current.digest != lease.digest {
		return ErrAuthorityUnavailable
	}
	if lease.issuedAt.UnixNano() > now.Add(r.margin).UnixNano() || lease.issuedAt.Add(lease.lifetime).UnixNano() <= now.Add(r.margin).UnixNano() {
		return ErrInvalidLease
	}
	deadline := pending.started.Add(lease.lifetime - r.margin)
	if !now.Before(deadline) || now.UnixNano() >= deadline.UnixNano() {
		return ErrAuthorityUnavailable
	}
	// A writer wall-clock expiry can only shorten the request-start bound.
	wallDeadline := min(lease.issuedAt.Add(lease.lifetime-r.margin).UnixNano(), deadline.UnixNano())
	r.lease = &lease
	r.deadline = deadline
	r.wallDeadline = wallDeadline
	return nil
}
func (r *LeaseReceiver) Check(ctx context.Context, revision *Revision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now, valid := r.clockNowLocked()
	current, ok := r.store.Current()
	if !valid || !ok || revision == nil || r.lease == nil || current.digest != revision.digest || r.lease.digest != revision.digest || r.lease.generation != revision.generation || r.lease.revision != revision.sequence || !now.Before(r.deadline) || now.UnixNano() >= r.wallDeadline {
		return ErrAuthorityUnavailable
	}
	return nil
}
func (r *LeaseReceiver) Expiry() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deadline
}
