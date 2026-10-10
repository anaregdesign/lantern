package security

import (
	"testing"
	"time"
)

func TestAuthorityRenewalSigningHoldsAcceptGate(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	workloads := [32]byte{7}
	raw := testAuthorityRenewalRequest(t, n, 1, workloads)
	n.selectValue(1, [32]byte{}, 1, 2)
	accept := n.take(s2cAccept, 1, 1)
	inside, release := make(chan struct{}), make(chan struct{})
	voteDone, acceptDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := n.nodes[1].signAuthorityRenewal(raw, workloads, func() error {
			close(inside)
			<-release
			return nil
		})
		voteDone <- err
	}()
	<-inside
	go func() {
		_, err := n.nodes[1].Receive(accept.raw)
		acceptDone <- err
	}()
	select {
	case err := <-acceptDone:
		close(release)
		<-voteDone
		t.Fatal("ACCEPT escaped the signing gate", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-voteDone; err != nil {
		t.Fatal("serialized pre-accept vote", err)
	}
	if err := <-acceptDone; err != nil {
		t.Fatal("durable ACCEPT after vote", err)
	}
	if _, err := n.nodes[1].signAuthorityRenewal(raw, workloads, func() error { return nil }); err == nil {
		t.Fatal("accepted suffix renewed its old prefix")
	}
}

func TestAuthorityRenewalDurableAcceptExclusion(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	workloads := [32]byte{7}
	raw := testAuthorityRenewalRequest(t, n, 1, workloads)
	request, err := parseAuthorityRenewalRequest(raw, n.fixture.trust, workloads)
	if err != nil {
		t.Fatal(err)
	}
	vote := func(id uint32, allowed bool) {
		t.Helper()
		before := n.nodes[id].p.count
		encoded, err := n.nodes[id].signAuthorityRenewal(raw, workloads, func() error { return nil })
		if (err == nil) != allowed {
			t.Fatalf("member %d allowed=%v: %v", id, allowed, err)
		}
		if n.nodes[id].p.count != before {
			t.Fatal("volatile renewal appended WAL")
		}
		if allowed {
			v, err := parseAuthorityRenewalVote(encoded, request, n.fixture.trust)
			if err != nil || v.member != id {
				t.Fatal("invalid produced vote", err)
			}
		}
	}
	for id := uint32(1); id <= 3; id++ {
		vote(id, true)
	}
	n.selectValue(1, [32]byte{}, 1, 2)
	// A durable PROMISE/SELECT without an ACCEPT is not a hidden choice.
	vote(1, true)
	vote(2, true)
	n.send(s2cAccept, 1, 1)
	n.send(s2cAccept, 1, 2)
	// A+B accepted while every B materialization is still the old prefix.
	// The otherwise matching A+C old-prefix majority cannot renew.
	vote(1, false)
	vote(2, false)
	vote(3, true)
	n.restart(1)
	vote(1, false)
	n.send(s2cAccepted, 2, 1)
	n.send(s2cAccepted, 1, 1)
	vote(1, false) // NOOP advances the control prefix even with identical S1 cut.
	newRaw := testAuthorityRenewalRequest(t, n, 1, workloads)
	if _, err := n.nodes[1].signAuthorityRenewal(newRaw, workloads, func() error { return nil }); err != nil {
		t.Fatal("new drained prefix", err)
	}
	if _, err := n.nodes[1].signAuthorityRenewal(newRaw, workloads, func() error { return errAuthorityTime }); err == nil {
		t.Fatal("unqualified workload/time signed")
	}
	if _, err := n.nodes[1].signAuthorityRenewal(newRaw, workloads, nil); err == nil {
		t.Fatal("missing eligibility signed")
	}
}

func TestAuthorityRenewalRequestAndVoteBinding(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	workloads := [32]byte{7}
	raw := testAuthorityRenewalRequest(t, n, 1, workloads)
	r, err := parseAuthorityRenewalRequest(raw, n.fixture.trust, workloads)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*authorityRenewalStatement){
		"version":          func(s *authorityRenewalStatement) { s.Version++ },
		"scope":            func(s *authorityRenewalStatement) { s.Scope[0]++ },
		"members":          func(s *authorityRenewalStatement) { s.Members[0]++ },
		"workloads":        func(s *authorityRenewalStatement) { s.Workloads[0]++ },
		"time":             func(s *authorityRenewalStatement) { s.TimeProfile[0]++ },
		"lifetime":         func(s *authorityRenewalStatement) { s.Lifetime++ },
		"unknown receiver": func(s *authorityRenewalStatement) { s.Receiver = 100 },
		"boot":             func(s *authorityRenewalStatement) { s.Boot = [16]byte{} },
		"challenge":        func(s *authorityRenewalStatement) { s.Challenge = [32]byte{} },
		"cut":              func(s *authorityRenewalStatement) { s.Cut.Sequence = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			s := r.statement
			mutate(&s)
			if _, err := signAuthorityRenewalRequest(s, n.fixture.trust, workloads, n.fixture.keys[1]); err == nil {
				t.Fatal("invalid request signed")
			}
		})
	}
	if _, err := signAuthorityRenewalRequest(r.statement, n.fixture.trust, workloads, n.fixture.keys[2]); err == nil {
		t.Fatal("other receiver key signed")
	}
	for _, change := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:len(b)-1] },
		func(b []byte) []byte { b[len(b)-1] ^= 1; return b },
		func(b []byte) []byte { return append(b, 0) },
		func(b []byte) []byte { b[len(authorityRenewalRequestDomain)] = '['; return b },
	} {
		if _, err := parseAuthorityRenewalRequest(change(append([]byte(nil), raw...)), n.fixture.trust, workloads); err == nil {
			t.Fatal("malformed request accepted")
		}
	}
	v, err := n.nodes[2].signAuthorityRenewal(raw, workloads, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	changed := r.statement
	changed.Challenge[0]++
	otherRaw, err := signAuthorityRenewalRequest(changed, n.fixture.trust, workloads, n.fixture.keys[1])
	if err != nil {
		t.Fatal(err)
	}
	other, err := parseAuthorityRenewalRequest(otherRaw, n.fixture.trust, workloads)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseAuthorityRenewalVote(v, other, n.fixture.trust); err == nil {
		t.Fatal("vote crossed challenge")
	}
	v[3] = 3
	if _, err := parseAuthorityRenewalVote(v, r, n.fixture.trust); err == nil {
		t.Fatal("signature counted as another member")
	}
}
