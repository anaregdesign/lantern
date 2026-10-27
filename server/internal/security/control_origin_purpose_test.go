package security

import (
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
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

func TestAuthorityPurposeDiagnosticDoesNotConsumeOrResample(t *testing.T) {
	t.Run("pending short circuit", func(t *testing.T) {
		for _, tc := range []struct {
			closed, known, present bool
			pending                managementAuthorization
			reason                 string
		}{
			{closed: true, reason: "pool_closed"},
			{reason: "ticket_unknown"},
			{known: true, reason: "pending_missing"},
			{known: true, present: true, pending: managementAuthorization{current: &authorityPendingPurpose{}}, reason: "state"},
			{known: true, present: true, pending: managementAuthorization{state: AuthorizationPending, current: &authorityPendingPurpose{started: true}}, reason: "started"},
		} {
			if got := authorityPurposePendingFailure(tc.closed, tc.known, tc.present, tc.pending, nil); got != tc.reason {
				t.Fatal("pending refusal dereferenced or changed the later binding predicate", got, tc.reason)
			}
		}
	})
	n, _ := authorityTestNativeCluster(t, 3)
	r, _ := authorityTestReceiver(t, n)
	h, op := authorityTestHeader(t, n.fixture)
	binding := authorityPurposeBinding{h.ID, op.digest, op.reviewed, op.actor, 1, 1}
	for _, reason := range []string{"before_not_before", "expired", "started", "cut", "lineage", "issuer_binding"} {
		t.Run(reason, func(t *testing.T) {
			m := NewManagementAuthorizations(time.Minute, 1)
			begin, err := m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time())
			if err != nil {
				t.Fatal(err)
			}
			pending := m.pending[begin.ID]
			if reason != "before_not_before" {
				pending.notBefore = pending.createdAt.Add(-time.Second)
			}
			switch reason {
			case "expired":
				pending.expiresAt = pending.createdAt.Add(-time.Second)
			case "started":
				pending.current.started = true
			case "cut":
				pending.current.binding.Reviewed.Sequence++
			case "lineage":
				pending.current.binding.Lineage++
			case "issuer_binding":
				pending.current.binding.IssuerConfigRevision++
			}
			m.pending[begin.ID] = pending
			calls := 0
			sample := r.clock.producer.sample
			r.clock.producer.sample = func() (authorityTimeStamp, error) { calls++; return sample() }
			defer func() { r.clock.producer.sample = sample }()
			capture := decisiontrace.Start(16)
			if capture == nil {
				t.Fatal("capture already owned")
			}
			defer capture.Stop()
			if _, _, err := m.startCurrent(r, begin.Ticket); err != ErrOperationAuthorization {
				t.Fatal("diagnostic changed purpose refusal", err)
			}
			capture.Stop()
			events, dropped := capture.Drain()
			var observed []string
			for _, event := range events {
				if event.Kind == "browser_decision" {
					observed = append(observed, event.Reason)
				}
			}
			wantCalls := 0
			if reason == "before_not_before" || reason == "expired" {
				wantCalls = 1
			}
			if dropped != 0 || len(observed) != 1 || observed[0] != reason || calls != wantCalls {
				t.Fatal("diagnostic lost first refusal or added native samples", observed, dropped, calls)
			}
			if _, exists := m.tickets[sha256.Sum256(begin.Ticket[:])]; !exists {
				t.Fatal("diagnostic consumed refused ticket")
			}
		})
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

func TestAuthorityPurposeRenewalBindingCallSites(t *testing.T) {
	for _, operation := range []string{"begin", "start", "complete"} {
		t.Run(operation, func(t *testing.T) {
			n, _ := authorityTestNativeCluster(t, 3)
			r, ticks := authorityTestReceiver(t, n)
			r.clock.traceID = 71
			h, op := authorityTestHeader(t, n.fixture)
			binding := authorityPurposeBinding{h.ID, op.digest, op.reviewed, op.actor, 1, 1}
			m := NewManagementAuthorizations(time.Minute, 1)
			var begin AuthorizationStart
			var event TokenAuthenticationEvidence
			if operation != "begin" {
				var err error
				begin, err = m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time())
				if err != nil {
					t.Fatal(err)
				}
			}
			if operation == "complete" {
				ticks.Add(uint64(3 * time.Second))
				if _, _, err := m.startCurrent(r, begin.Ticket); err != nil {
					t.Fatal(err)
				}
				event = *h.Credential.Token
				event.Mode, event.Profile, event.Nonce = "code", "oidc-id", [32]byte{10}
				event.IssuedAt, event.AuthTime = authorityNumericTime(begin.NotBefore), authorityNumericTime(begin.NotBefore)
				event.Code = CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: begin.ID, Transaction: [32]byte{4}, Exchange: [32]byte{5}, Nonce: event.Nonce, PKCE: [32]byte{6}, CreatedAt: begin.NotBefore, ConsumedAt: begin.NotBefore.Add(time.Millisecond), ExpiresAt: begin.ExpiresAt}
			}
			r.active.certificate.request.statement.Cut.Sequence++
			calls := 0
			sample := r.clock.producer.sample
			r.clock.producer.sample = func() (authorityTimeStamp, error) { calls++; return sample() }
			capture := decisiontrace.Start(16)
			if capture == nil {
				t.Fatal("capture already owned")
			}
			defer capture.Stop()
			var err error
			switch operation {
			case "begin":
				_, err = m.beginCurrent(r, binding, h.Credential.Token.ExpiresAt.Time())
			case "start":
				_, _, err = m.startCurrent(r, begin.Ticket)
			case "complete":
				_, err = m.completeCurrent(r, begin.ID, event)
			}
			capture.Stop()
			events, dropped := capture.Drain()
			if err != errAuthorityRenewal || !errors.Is(err, errAuthorityRenewal) || calls != 0 || len(m.proofs) != 0 {
				t.Fatal("binding diagnostic sampled or published proof", err, calls)
			}
			if operation == "begin" {
				if len(m.pending) != 0 || len(m.tickets) != 0 {
					t.Fatal("failed begin committed generated IDs")
				}
			} else {
				pending := m.pending[begin.ID]
				if len(m.pending) != 1 || pending.state != AuthorizationPending || pending.proof != [32]byte{} || pending.current.started != (operation == "complete") || pending.current.evidence != [32]byte{} {
					t.Fatal("binding failure changed retained purpose state")
				}
				if _, present := m.tickets[sha256.Sum256(begin.Ticket[:])]; present != (operation == "start") {
					t.Fatal("binding failure consumed or restored ticket")
				}
			}
			details := 0
			for _, observed := range events {
				if observed.Kind == "browser_decision" && observed.Stage == "renewal_binding_origin_purpose_"+operation {
					details++
					if observed.Reason != "projection_cut" || observed.Object != 71 {
						t.Fatal("wrong same-pass purpose detail", observed)
					}
				}
			}
			if dropped != 0 || details != 1 {
				t.Fatal("purpose binding call site missing", events, dropped)
			}
		})
	}
}
