package security

import (
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

type AuthorizationState uint8

const (
	AuthorizationPending AuthorizationState = iota + 1
	AuthorizationApproved
	AuthorizationDenied
)

type AuthorizationStart struct {
	ID        [32]byte
	Ticket    [32]byte
	ExpiresAt time.Time
	NotBefore time.Time
}
type AuthorizationStatus struct {
	ID        [32]byte
	State     AuthorizationState
	Proof     [32]byte
	ExpiresAt time.Time
}
type managementAuthorization struct {
	binding                                     ManagementBinding
	createdAt, notBefore, expiresAt, approvedAt time.Time
	state                                       AuthorizationState
	proof                                       [32]byte
	evidence                                    *PurposeAuthenticationEvidence
	evidenceBytes                               int
	current                                     *authorityPendingPurpose
}

// ManagementAuthorizations is process-bound to the pinned fixed writer. A
// restart invalidates outstanding approvals. A proof permits only its exact
// immutable ID/intent/CAS: at most one durable effect, with retained same-ID
// retries resolved before proof validation. There is no session mutation.
type ManagementAuthorizations struct {
	mu       sync.Mutex
	pending  map[[32]byte]managementAuthorization
	tickets  map[[32]byte][32]byte
	proofs   map[[32]byte][32]byte
	lifetime time.Duration
	limit    int
	closed   bool
}

func NewManagementAuthorizations(lifetime time.Duration, limit int) *ManagementAuthorizations {
	return &ManagementAuthorizations{pending: make(map[[32]byte]managementAuthorization), tickets: make(map[[32]byte][32]byte), proofs: make(map[[32]byte][32]byte), lifetime: lifetime, limit: limit}
}
func (m *ManagementAuthorizations) Begin(binding ManagementBinding, now time.Time) (AuthorizationStart, error) {
	if m == nil || m.lifetime <= 0 || m.limit <= 0 || now.IsZero() || binding.ChangeID == [16]byte{} || binding.IntentDigest == [32]byte{} || binding.ExpectedDigest == [32]byte{} || binding.Generation == [16]byte{} || binding.IssuerConfigRevision == 0 {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	var id, ticket [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return AuthorizationStart{}, err
	}
	if _, err := rand.Read(ticket[:]); err != nil {
		return AuthorizationStart{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	for key, operation := range m.pending {
		if !now.Before(operation.expiresAt) {
			delete(m.pending, key)
			delete(m.proofs, sha256.Sum256(operation.proof[:]))
			for ticket, target := range m.tickets {
				if target == key {
					delete(m.tickets, ticket)
				}
			}
		}
	}
	if len(m.pending) >= m.limit {
		return AuthorizationStart{}, ErrControlReserve
	}
	// Verified JWT NumericDate evidence has whole-second precision. Do not
	// accept an event in the review's second: it could precede final review.
	notBefore := now.Truncate(time.Second).Add(time.Second)
	m.pending[id] = managementAuthorization{binding: binding, createdAt: now, notBefore: notBefore, expiresAt: now.Add(m.lifetime), state: AuthorizationPending}
	m.tickets[sha256.Sum256(ticket[:])] = id
	return AuthorizationStart{ID: id, Ticket: ticket, NotBefore: notBefore, ExpiresAt: now.Add(m.lifetime)}, nil
}

// NavigationNotBefore reveals only when this unpredictable ticket may start
// its challenge. Early navigation leaves the single-use ticket unconsumed.
func (m *ManagementAuthorizations) NavigationNotBefore(ticket [32]byte, current *Revision, now time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, known := m.tickets[sha256.Sum256(ticket[:])]
	operation, pending := m.pending[id]
	if !known || !pending || operation.state != AuthorizationPending || !validAuthorizationCut(operation, current, now) {
		return time.Time{}, ErrOperationAuthorization
	}
	return operation.notBefore, nil
}

// Start consumes only the unpredictable navigation ticket. It never approves
// the operation; Code/PKCE/state/nonce and signed authentication remain required.
func (m *ManagementAuthorizations) Start(ticket [32]byte, current *Revision, now time.Time) ([32]byte, ManagementBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := sha256.Sum256(ticket[:])
	id, known := m.tickets[key]
	operation, pending := m.pending[id]
	if !known || !pending || !validAuthorizationCut(operation, current, now) || operation.state != AuthorizationPending || now.Before(operation.notBefore) {
		return [32]byte{}, ManagementBinding{}, ErrOperationAuthorization
	}
	delete(m.tickets, key)
	return id, operation.binding, nil
}

func validAuthorizationCut(operation managementAuthorization, current *Revision, now time.Time) bool {
	if operation.current != nil {
		return false
	}
	binding := operation.binding
	if current == nil || now.Before(operation.createdAt) || !now.Before(operation.expiresAt) || current.generation != binding.Generation || current.sequence != binding.ExpectedRevision || current.digest != binding.ExpectedDigest || current.writer != binding.Writer {
		return false
	}
	issuer, known := current.snapshot.Issuer(binding.Actor.Issuer)
	access, active := current.snapshot.AccessFor(binding.Actor)
	return known && issuer.Enabled && !issuer.Deleted && issuer.ConfigRevision == binding.IssuerConfigRevision && active && access.AllowsGlobal(SecurityManage)
}

// Complete accepts detached facts exclusively from the provider's trusted Code
// adapter. The struct is not independently cryptographic proof of that adapter.
func (m *ManagementAuthorizations) Complete(id [32]byte, event TokenAuthenticationEvidence, current *Revision, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	operation, known := m.pending[id]
	if !known || operation.state != AuthorizationPending {
		return ErrOperationAuthorization
	}
	commitment, evidenceErr := event.Commitment()
	authTime, credentialExpiry := event.AuthTime.Time(), event.ExpiresAt.Time()
	if evidenceErr != nil || event.Mode != "code" || event.Code.Flow != "operation" || event.Code.AuthorizationID != id ||
		event.Generation != operation.binding.Generation || !validAuthorizationCut(operation, current, now) ||
		event.Identity != operation.binding.Actor || event.ConfigRevision != operation.binding.IssuerConfigRevision ||
		event.Code.CreatedAt.Before(operation.notBefore) || event.Code.ConsumedAt.Before(event.Code.CreatedAt) ||
		event.Code.ConsumedAt.After(now) || !event.Code.ConsumedAt.Before(event.Code.ExpiresAt) ||
		authTime.IsZero() || authTime.Before(operation.notBefore) || authTime.After(now) || !now.Before(credentialExpiry) {
		operation.state = AuthorizationDenied
		m.pending[id] = operation
		return ErrOperationAuthorization
	}
	expiry := operation.expiresAt
	if credentialExpiry.Before(expiry) {
		expiry = credentialExpiry
	}
	evidence := PurposeAuthenticationEvidence{Version: 1, Kind: "operation-approval", Binding: operation.binding, AuthorizationID: id,
		ReviewAt: operation.createdAt, NotBefore: operation.notBefore, ApprovedAt: now, ExpiresAt: expiry, Event: event, EventCommitment: commitment}
	encoded, err := authenticationEvidenceBytes(evidence)
	if err != nil || len(encoded) > MaxAuthenticationEvidenceBytes {
		operation.state = AuthorizationDenied
		m.pending[id] = operation
		return ErrOperationAuthorization
	}
	var proof [32]byte
	if _, err := rand.Read(proof[:]); err != nil {
		return err
	}
	operation.state, operation.proof, operation.approvedAt, operation.expiresAt = AuthorizationApproved, proof, now, expiry
	operation.evidence, operation.evidenceBytes = &evidence, len(encoded)
	m.pending[id] = operation
	m.proofs[sha256.Sum256(proof[:])] = id
	return nil
}

// Close discards process-owned approvals and their transcripts. It is terminal;
// native Sessions and retained business results have independent owners.
func (m *ManagementAuthorizations) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.pending)
	clear(m.tickets)
	clear(m.proofs)
	m.closed = true
}
func (m *ManagementAuthorizations) Reject(id [32]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	operation, known := m.pending[id]
	if known && operation.state == AuthorizationPending {
		operation.state = AuthorizationDenied
		m.pending[id] = operation
	}
}
func (m *ManagementAuthorizations) Status(id [32]byte, actor Identity, current *Revision, now time.Time) (AuthorizationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	operation, known := m.pending[id]
	if !known || operation.binding.Actor != actor || !validAuthorizationCut(operation, current, now) {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	return AuthorizationStatus{ID: id, State: operation.state, Proof: operation.proof, ExpiresAt: operation.expiresAt}, nil
}

// Verify runs inside the Store's serialized commit boundary. It performs no
// I/O or Store calls. Proof bytes are deliberately absent from business intent.
func (m *ManagementAuthorizations) Verify(proof []byte, binding ManagementBinding, now time.Time) error {
	if m == nil || len(proof) != 32 {
		return ErrOperationAuthorization
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id, known := m.proofs[sha256.Sum256(proof)]
	operation, pending := m.pending[id]
	if !known || !pending || operation.current != nil || operation.state != AuthorizationApproved || operation.binding != binding || now.Before(operation.approvedAt) || !now.Before(operation.expiresAt) {
		return ErrOperationAuthorization
	}
	return nil
}
