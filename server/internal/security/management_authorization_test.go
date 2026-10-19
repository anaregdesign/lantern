package security

import (
	"testing"
	"time"
)

func TestManagementAuthorizationsCloseDiscardsProcessState(t *testing.T) {
	m := NewManagementAuthorizations(time.Minute, 1)
	id := [32]byte{1}
	m.pending[id] = managementAuthorization{state: AuthorizationPending}
	m.tickets[[32]byte{2}] = id
	m.proofs[[32]byte{3}] = id
	m.Reject(id)
	if m.pending[id].state != AuthorizationDenied {
		t.Fatal("pending operation not rejected")
	}
	m.Close()
	m.Close()
	if !m.closed || len(m.pending) != 0 || len(m.tickets) != 0 || len(m.proofs) != 0 {
		t.Fatal("closed pool retained process state")
	}
}
