package security

import (
	"crypto/rand"
	"crypto/sha256"
	"time"
)

const authorizationConsumed AuthorizationState = 4

// Shares the existing bounded purpose pool, with an explicit incompatible
// binding tag. Legacy Start/Complete/Verify cannot consume these records.
type authorityPendingPurpose struct {
	binding   authorityPurposeBinding
	challenge [32]byte
	started   bool
	event     TokenAuthenticationEvidence
	evidence  [32]byte
}

func authorityPurposeBindingLive(b authorityPurposeBinding, p *S1Projection) bool {
	if p == nil || b.Reviewed != p.cut || !b.ID.valid() || b.ID.Domain != p.cut.Domain || b.ID.Cohort != p.cut.Cohort || b.Operation == [32]byte{} || b.Lineage == 0 || b.Lineage != p.lineage[b.Actor] {
		return false
	}
	i, known := p.snapshot.Issuer(b.Actor.Issuer)
	a, active := p.snapshot.AccessFor(b.Actor)
	return b.Actor.Kind == OIDCPrincipal && known && i.Enabled && !i.Deleted && i.ConfigRevision == b.IssuerConfigRevision && active && a.AllowsGlobal(SecurityManage)
}

// Lock order: participant gate -> purpose pool -> native time owner. Callers
// additionally validate current workload eligibility at the composite boundary.
func (m *ManagementAuthorizations) beginCurrent(r *authorityRenewalReceiver, binding authorityPurposeBinding, credentialExpiry time.Time) (AuthorizationStart, error) {
	if m == nil || r == nil || m.lifetime <= 0 || m.limit <= 0 || !s2cCanonicalTime(credentialExpiry, false) {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	var id, ticket, challenge [32]byte
	for _, destination := range [][]byte{id[:], ticket[:], challenge[:]} {
		if _, err := rand.Read(destination); err != nil {
			return AuthorizationStart{}, err
		}
	}
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	defer r.kernel.poisonPanic()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !authorityPurposeBindingLive(binding, r.kernel.replayState.projection) {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	now, _, err := r.currentLocked()
	if err != nil {
		return AuthorizationStart{}, err
	}
	high := time.Unix(0, int64(now.utc.high)).UTC()
	for key, old := range m.pending {
		if !high.Before(old.expiresAt) {
			delete(m.pending, key)
			delete(m.proofs, sha256.Sum256(old.proof[:]))
			for ticket, target := range m.tickets {
				if target == key {
					delete(m.tickets, ticket)
				}
			}
		}
	}
	if len(m.pending) >= min(m.limit, 1024) {
		return AuthorizationStart{}, ErrControlReserve
	}
	expires := authorityMinTime(credentialExpiry, high.Add(min(m.lifetime, 10*time.Minute)))
	notBefore := high.Truncate(time.Second).Add(time.Second)
	if !notBefore.Before(expires) {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	m.pending[id] = managementAuthorization{createdAt: high, notBefore: notBefore, expiresAt: expires, state: AuthorizationPending,
		current: &authorityPendingPurpose{binding: binding, challenge: challenge}}
	m.tickets[sha256.Sum256(ticket[:])] = id
	return AuthorizationStart{ID: id, Ticket: ticket, NotBefore: notBefore, ExpiresAt: expires}, nil
}

func (m *ManagementAuthorizations) startCurrent(r *authorityRenewalReceiver, ticket [32]byte) ([32]byte, authorityPurposeBinding, error) {
	if m == nil || r == nil {
		return [32]byte{}, authorityPurposeBinding{}, ErrOperationAuthorization
	}
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	defer r.kernel.poisonPanic()
	m.mu.Lock()
	defer m.mu.Unlock()
	key := sha256.Sum256(ticket[:])
	id, known := m.tickets[key]
	o, present := m.pending[id]
	if m.closed || !known || !present || o.current == nil || o.state != AuthorizationPending || o.current.started || !authorityPurposeBindingLive(o.current.binding, r.kernel.replayState.projection) {
		return [32]byte{}, authorityPurposeBinding{}, ErrOperationAuthorization
	}
	now, _, err := r.currentLocked()
	if err != nil {
		return [32]byte{}, authorityPurposeBinding{}, err
	}
	low, high := time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC()
	if low.Before(o.notBefore) || !high.Before(o.expiresAt) {
		return [32]byte{}, authorityPurposeBinding{}, ErrOperationAuthorization
	}
	delete(m.tickets, key)
	o.current.started = true
	m.pending[id] = o
	return id, o.current.binding, nil
}

// Only the genuine Code producer's detached original event is supplied by the
// private composition adapter. This method never treats the struct as proof of
// producer authenticity, nor changes the normal session or review binding.
func (m *ManagementAuthorizations) completeCurrent(r *authorityRenewalReceiver, id [32]byte, event TokenAuthenticationEvidence) (AuthorizationStatus, error) {
	if m == nil || r == nil {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	commitment, err := event.Commitment()
	if err != nil {
		return AuthorizationStatus{}, err
	}
	var proof [32]byte
	if _, err = rand.Read(proof[:]); err != nil {
		return AuthorizationStatus{}, err
	}
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	defer r.kernel.poisonPanic()
	m.mu.Lock()
	defer m.mu.Unlock()
	o, known := m.pending[id]
	if m.closed || !known || o.current == nil || o.state != AuthorizationPending || !o.current.started || !authorityPurposeBindingLive(o.current.binding, r.kernel.replayState.projection) {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	b := o.current.binding
	if event.Mode != "code" || event.Code.Flow != "operation" || event.Code.AuthorizationID != id || event.Identity != b.Actor || event.Generation != b.Reviewed.Generation || event.ConfigRevision != b.IssuerConfigRevision ||
		!event.AuthTime.Present || !event.AuthTime.Numeric || event.AuthTime.Time().Before(o.notBefore) || event.Code.CreatedAt.Before(o.notBefore) || event.Code.ConsumedAt.Before(event.Code.CreatedAt) || !event.Code.ConsumedAt.Before(event.Code.ExpiresAt) {
		o.state = AuthorizationDenied
		m.pending[id] = o
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	now, _, err := r.currentLocked()
	if err != nil {
		return AuthorizationStatus{}, err
	}
	low, high := time.Unix(0, int64(now.utc.low)).UTC(), time.Unix(0, int64(now.utc.high)).UTC()
	expires := authorityMinTime(o.expiresAt, event.ExpiresAt.Time())
	start := authorityCredentialStart(authorityCredentialClaim{Token: &event}).Time()
	if low.Before(event.AuthTime.Time()) || low.Before(event.Code.ConsumedAt) || low.Before(start) || !high.Before(expires) {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	o.current.event, o.current.evidence = event, commitment
	o.state, o.proof, o.approvedAt, o.expiresAt = AuthorizationApproved, proof, high, expires
	m.pending[id] = o
	m.proofs[sha256.Sum256(proof[:])] = id
	return AuthorizationStatus{ID: id, State: AuthorizationApproved, Proof: proof, ExpiresAt: expires}, nil
}

// Caller holds both participant and purpose locks, and keeps them through the
// final native sample and markCurrentConsumedLocked. The returned claim is a
// detached immutable copy; its deadlines are checked at that final event.
func (m *ManagementAuthorizations) prepareCurrentConsumeLocked(proof [32]byte, binding authorityPurposeBinding, origin [32]byte, serial uint64) (authorityPurposeClaim, error) {
	id, known := m.proofs[sha256.Sum256(proof[:])]
	o, present := m.pending[id]
	if m.closed || !known || !present || o.current == nil || o.current.binding != binding || o.state != AuthorizationApproved || origin == [32]byte{} || serial == 0 {
		return authorityPurposeClaim{}, ErrOperationAuthorization
	}
	return authorityPurposeClaim{Kind: "full-s1-operation", Binding: binding, Authorization: id, Challenge: o.current.challenge,
		ReviewAt: o.createdAt, NotBefore: o.notBefore, ApprovedAt: o.approvedAt, ExpiresAt: o.expiresAt, Event: o.current.event, EventCommitment: o.current.evidence, Origin: origin, Serial: serial}, nil
}

func (m *ManagementAuthorizations) markCurrentConsumedLocked(p authorityPurposeClaim) {
	o := m.pending[p.Authorization]
	delete(m.proofs, sha256.Sum256(o.proof[:]))
	o.state, o.proof = authorizationConsumed, [32]byte{}
	m.pending[p.Authorization] = o
}
