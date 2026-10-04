package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

// AuthorityClock must remain bounded across suspend/VM pauses. Both wall and
// monotonic elapsed checks are used; simultaneous rollback/freeze of both clocks
// needs external fencing. Production must explicitly qualify this assumption.
type AuthorityClock interface{ Now() time.Time }
type systemAuthorityClock struct{}

func (systemAuthorityClock) Now() time.Time { return time.Now() }

type LeaseAuthorityOptions struct {
	Clock        AuthorityClock
	Lifetime     time.Duration
	ClockMargin  time.Duration
	MaxReceivers int
}

type holderIdentity struct{ receiver, boot [16]byte }
type leaseHolder struct {
	revision uint64
	deadline time.Time
}

// LeaseAuthority serializes lease issuance with Store commits. A new incarnation
// always waits the previous maximum lifetime plus margin before serving or
// renewing: restarting a writer never forgets uncertain outstanding holders.
// No automatic promotion or fencing proof is inferred from a local file lock.
type LeaseAuthority struct {
	store            *Store
	clock            AuthorityClock
	lifetime, margin time.Duration
	incarnation      [16]byte
	readyAt          time.Time
	lastWall         time.Time
	clockFault       bool
	maxReceivers     int
	holders          map[holderIdentity][]leaseHolder
	mu               sync.Mutex
}

func NewLeaseAuthority(store *Store, options LeaseAuthorityOptions) (*LeaseAuthority, error) {
	if store == nil || len(store.privateKey) == 0 {
		return nil, ErrReadOnlyWriter
	}
	if options.Clock == nil {
		options.Clock = systemAuthorityClock{}
	}
	if options.Lifetime == 0 {
		options.Lifetime = maxLeaseLifetime
	}
	if options.ClockMargin == 0 {
		options.ClockMargin = 2 * time.Second
	}
	if options.MaxReceivers == 0 {
		options.MaxReceivers = 4096
	}
	if options.Lifetime <= options.ClockMargin || options.Lifetime > maxLeaseLifetime || options.ClockMargin <= 0 || options.ClockMargin > maxLeaseClockMargin || options.MaxReceivers < 1 || options.MaxReceivers > 4096 {
		return nil, ErrInvalidLease
	}
	now := options.Clock.Now()
	authority := &LeaseAuthority{store: store, clock: options.Clock, lifetime: options.Lifetime, margin: options.ClockMargin,
		readyAt: now.Add(maxLeaseLifetime + maxLeaseClockMargin), lastWall: now, maxReceivers: options.MaxReceivers, holders: make(map[holderIdentity][]leaseHolder)}
	if _, err := rand.Read(authority.incarnation[:]); err != nil {
		return nil, err
	}
	return authority, nil
}
func (a *LeaseAuthority) validClockLocked(now time.Time) bool {
	// An observed backward wall jump permanently poisons this incarnation.
	if now.UnixNano() < a.lastWall.UnixNano() {
		a.clockFault = true
	}
	a.lastWall = now
	return !a.clockFault && !now.Before(a.readyAt) && now.UnixNano() >= a.readyAt.UnixNano()
}
func (a *LeaseAuthority) Issue(ctx context.Context, request LeaseRequest) ([]byte, error) {
	lease, _, err := a.issue(ctx, request)
	return lease, err
}

// Checkpoint captures the original complete image and signed lease at one
// serialized cut. Relayers may carry the image but cannot mint this proof.
func (a *LeaseAuthority) Checkpoint(ctx context.Context, request LeaseRequest) ([]byte, []byte, error) {
	lease, revision, err := a.issue(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	return lease, revision.Encode(), nil
}

// CheckpointSince captures the same atomic cut as Checkpoint, but avoids
// encoding/transferring an unchanged complete policy on routine renewals.
// known is merely a size optimization; it never substitutes for signed proof.
func (a *LeaseAuthority) CheckpointSince(ctx context.Context, request LeaseRequest, known [32]byte) ([]byte, []byte, error) {
	lease, revision, err := a.issue(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	if known == revision.digest {
		return lease, nil, nil
	}
	return lease, revision.Encode(), nil
}
func (a *LeaseAuthority) issue(ctx context.Context, request LeaseRequest) ([]byte, *Revision, error) {
	if a == nil || !request.valid() {
		return nil, nil, ErrInvalidLease
	}
	// Keep the lock order Store -> authority everywhere. Holding Store.mu makes
	// capture/sign/holder registration one cut with respect to control commits.
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !a.validClockLocked(now) || a.store.faulted.Load() {
		return nil, nil, ErrAuthorityUnavailable
	}
	current := a.store.current.Load()
	if current == nil {
		return nil, nil, ErrAuthorityUnavailable
	}
	activeProofs := 0
	for identity, holders := range a.holders {
		live := holders[:0]
		for _, holder := range holders {
			if now.Before(holder.deadline) || now.UnixNano() < holder.deadline.UnixNano() {
				live = append(live, holder)
			}
		}
		if len(live) == 0 {
			delete(a.holders, identity)
		} else {
			a.holders[identity] = live
			activeProofs += len(live)
		}
	}
	identity := holderIdentity{request.Receiver, request.BootNonce}
	if _, known := a.holders[identity]; !known && len(a.holders) >= a.maxReceivers {
		return nil, nil, ErrControlReserve
	}
	lease := authorityLease{generation: a.store.generation, writer: sha256.Sum256(a.store.publicKey), incarnation: a.incarnation, request: request,
		revision: current.sequence, digest: current.digest, issuedAt: now, lifetime: a.lifetime}
	// Owner-side accounting includes the clock margin; no unseen holder can
	// disappear just because it is disconnected or has stopped renewing.
	holders := a.holders[identity]
	deadline := now.Add(a.lifetime + a.margin)
	extended := false
	for i := range holders {
		if holders[i].revision == current.sequence {
			holders[i].deadline = deadline
			extended = true
			break
		}
	}
	if !extended {
		if len(holders) >= 64 || activeProofs >= 16384 {
			return nil, nil, ErrControlReserve
		}
		holders = append(holders, leaseHolder{revision: current.sequence, deadline: deadline})
	}
	a.holders[identity] = holders
	return lease.encode(a.store.privateKey), current, nil
}
func (a *LeaseAuthority) Check(ctx context.Context, revision *Revision) error {
	if a == nil || revision == nil {
		return ErrAuthorityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.store.current.Load()
	if !a.validClockLocked(a.clock.Now()) || a.store.faulted.Load() || current == nil || current.digest != revision.digest {
		return ErrAuthorityUnavailable
	}
	return nil
}

// Enforced is conservative. It requires expiry of every pre-change holder;
// connected acknowledgements alone never certify disconnected processes.
func (a *LeaseAuthority) Enforced(result ChangeResult) bool {
	if a == nil || result.Revision == 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	current := a.store.current.Load()
	if !a.validClockLocked(now) || a.store.faulted.Load() || current == nil || current.sequence < result.Revision {
		return false
	}
	for _, holders := range a.holders {
		for _, holder := range holders {
			if holder.revision < result.Revision && (now.Before(holder.deadline) || now.UnixNano() < holder.deadline.UnixNano()) {
				return false
			}
		}
	}
	return true
}
