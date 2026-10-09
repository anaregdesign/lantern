package security

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorityPurposeExclusiveConsume(t *testing.T) {
	n, _ := authorityTestNativeCluster(t, 3)
	r, ticks := authorityTestReceiver(t, n)
	h, op := authorityTestHeader(t, n.fixture)
	binding := authorityPurposeBinding{h.ID, op.digest, op.reviewed, op.actor, 1, 1}
	m := NewManagementAuthorizations(time.Hour, 5000)
	begin, err := m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time())
	if err != nil || begin.ExpiresAt.Sub(m.pending[begin.ID].createdAt) != 10*time.Minute {
		t.Fatal("bounded purpose begin", err)
	}
	if _, _, err := m.startCurrent(r, begin.Ticket); err == nil {
		t.Fatal("challenge before conservative review end")
	}
	ticks.Add(uint64(3 * time.Second))
	id, captured, err := m.startCurrent(r, begin.Ticket)
	if err != nil || id != begin.ID || captured != binding {
		t.Fatal("ticket burned by early navigation", err)
	}
	if _, _, err := m.startCurrent(r, begin.Ticket); err == nil {
		t.Fatal("ticket reused")
	}
	event := *h.Credential.Token
	event.Mode, event.Profile, event.Nonce = "code", "oidc-id", [32]byte{10}
	event.IssuedAt, event.AuthTime = authorityNumericTime(begin.NotBefore), authorityNumericTime(begin.NotBefore)
	event.Code = CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: id, Transaction: [32]byte{4}, Exchange: [32]byte{5}, Nonce: event.Nonce, PKCE: [32]byte{6}, CreatedAt: begin.NotBefore, ConsumedAt: begin.NotBefore.Add(time.Millisecond), ExpiresAt: begin.ExpiresAt}
	approved, err := m.completeCurrent(r, id, event)
	if err != nil {
		t.Fatal(err)
	}
	if m.Verify(approved.Proof[:], ManagementBinding{}, m.pending[id].approvedAt) == nil {
		t.Fatal("legacy reusable proof accepted full-S1 purpose")
	}
	if _, err := m.completeCurrent(r, id, event); err == nil {
		t.Fatal("completed twice")
	}
	ticks.Add(uint64(100 * time.Millisecond))
	var successes atomic.Int32
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			r.kernel.gate.Lock()
			defer r.kernel.gate.Unlock()
			m.mu.Lock()
			defer m.mu.Unlock()
			p, err := m.prepareCurrentConsumeLocked(approved.Proof, binding, h.OriginDigest, h.Serial)
			if err != nil {
				return
			}
			now, _, err := r.currentLocked()
			if err != nil {
				t.Error(err)
				return
			}
			check := h
			check.Time.UTCLow, check.Time.UTCHigh = now.utc.low, now.utc.high
			check.Purpose, check.PurposeDeadline = &p, p.ExpiresAt
			check.PurposeEvidence, check.PurposeBinding = s2cHash("current-purpose-evidence-v2", p), s1PurposeBinding(h.ID, op)
			if !verifyAuthorityPurpose(check, op) {
				t.Error("invalid frozen purpose")
				return
			}
			m.markCurrentConsumedLocked(p)
			successes.Add(1)
		})
	}
	workers.Wait()
	if successes.Load() != 1 || m.pending[id].state != authorizationConsumed || len(m.proofs) != 0 {
		t.Fatal("purpose consumed more than once", successes.Load())
	}
	if h.Credential.Token.AuthTime.Present {
		t.Fatal("normal authentication was refreshed")
	}
	m.Close()
	if len(m.pending) != 0 || len(m.tickets) != 0 {
		t.Fatal("purpose close retained event")
	}
}

func TestAuthorityPurposeRefusesStaleCutAndSharedPoolExhaustion(t *testing.T) {
	n, _ := authorityTestNativeCluster(t, 3)
	r, _ := authorityTestReceiver(t, n)
	h, op := authorityTestHeader(t, n.fixture)
	binding := authorityPurposeBinding{h.ID, op.digest, op.reviewed, op.actor, 1, 1}
	for name, edit := range map[string]func(*authorityPurposeBinding){
		"semantic cut":         func(b *authorityPurposeBinding) { b.Reviewed.Sequence++ },
		"lineage":              func(b *authorityPurposeBinding) { b.Lineage++ },
		"issuer configuration": func(b *authorityPurposeBinding) { b.IssuerConfigRevision++ },
		"machine actor":        func(b *authorityPurposeBinding) { b.Actor.Kind = MachinePrincipal },
		"different cohort":     func(b *authorityPurposeBinding) { b.ID.Cohort[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := binding
			edit(&bad)
			m := NewManagementAuthorizations(time.Minute, 1)
			if _, err := m.beginCurrent(r, bad, h.Credential.Token.ExpiresAt.Time()); err == nil {
				t.Fatal("invalid purpose review accepted")
			}
		})
	}
	m := NewManagementAuthorizations(time.Minute, 1)
	if _, err := m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time()); err == nil {
		t.Fatal("purpose pool exceeded")
	}
	m.Close()
	if _, err := m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time()); err == nil {
		t.Fatal("closed purpose pool used")
	}
}
