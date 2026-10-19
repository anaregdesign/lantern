package security

import (
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
	createdAt, notBefore, expiresAt, approvedAt time.Time
	state                                       AuthorizationState
	proof                                       [32]byte
	current                                     *authorityPendingPurpose
}

// ManagementAuthorizations owns bounded process-local reviewed-operation approvals.
// The original H consumes a proof once; retained original results own retries.
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
