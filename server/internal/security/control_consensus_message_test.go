package security

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestS2CMessageQuorums(t *testing.T) {
	for _, n := range []int{3, 5, 31} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := s2cTestCluster(t, n)
			prepare, err := s2cSignPrepare(f.trust, f.keys[1], 1, 1, f.genesis.state.prefix, s2cBallot{1, 1})
			if err != nil {
				t.Fatal(err)
			}
			var promises []*s2cMessage
			for i := 1; i <= n; i++ {
				p, err := s2cSignPromise(f.trust, f.keys[uint32(i)], uint32(i), prepare, s2cBallot{}, nil, [32]byte{})
				if err != nil {
					t.Fatal(err)
				}
				promises = append(promises, p)
			}
			admission := [32]byte{0xad}
			accept, err := s2cSignAccept(f.trust, f.keys[1], 1, prepare, promises, nil, admission)
			if err != nil {
				t.Fatal(err)
			}
			if len(accept.proof) > s2cHardProofBytes || accept.value != f.trust.noopValue() || s2cVerifyP1(f.trust, accept) != nil {
				t.Fatal("canonical NOOP/P1", len(accept.proof))
			}
			var votes []*s2cMessage
			for i := 1; i <= n; i++ {
				v, err := s2cSignAccepted(f.trust, f.keys[uint32(i)], uint32(i), accept, admission)
				if err != nil {
					t.Fatal(err)
				}
				votes = append(votes, v)
			}
			chosen, err := s2cMakeChosen(f.trust, accept, votes)
			if err != nil {
				t.Fatal(err)
			}
			if uint64(len(chosen.raw)) != s2cChosenBytesBound(f.trust, nil) {
				t.Fatal("largest encoded witness bound", len(chosen.raw), s2cChosenBytesBound(f.trust, nil))
			}
			next, err := s2cVerifyChosen(f.trust, chosen)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := ApplyS1(f.genesis.state, next)
			if err != nil || applied.Status != S1Noop || applied.State.slot != 1 {
				t.Fatal("genuine QC apply", err)
			}
			if _, err = s2cMakeChosen(f.trust, accept, votes[:f.trust.majority()-1]); err == nil {
				t.Fatal("minority QC")
			}
			if _, _, err = s2cSelectP1(f.trust, promises[:f.trust.majority()-1], nil); err == nil {
				t.Fatal("minority P1")
			}
			if _, _, err = s2cSelectP1(f.trust, append(promises[:f.trust.majority()-1:f.trust.majority()-1], promises[0]), nil); err == nil {
				t.Fatal("duplicate promise signer")
			}
			if _, err = s2cBuildQC(f.trust, append(votes[:f.trust.majority()-1:f.trust.majority()-1], votes[0])); err == nil {
				t.Fatal("duplicate QC signer")
			}
			minimal, err := s2cMakeChosen(f.trust, accept, votes[:f.trust.majority()])
			if err != nil {
				t.Fatal(err)
			}
			other, err := s2cVerifyChosen(f.trust, minimal)
			if err != nil || next.certificate.commit != other.certificate.commit || next.certificate.witness == other.certificate.witness {
				t.Fatal("replaceable witnesses changed logical commit", err)
			}
			if _, err = s2cVerifyChosen(f.trust, accept); err == nil {
				t.Fatal("accept became choice")
			}
			if _, err = s2cSignAccepted(f.trust, f.keys[2], 2, accept, [32]byte{0xae}); err == nil {
				t.Fatal("local admission mismatch signed")
			}
			// ACCEPT durably retaining this snapshot is sufficient to reproduce
			// a Promise on an equal Prepare without appending another record.
			retained, err := s2cSignPromise(f.trust, f.keys[2], 2, accept, accept.ballot, nil, accept.admission)
			if err != nil || retained.acceptedValue != f.trust.noopValue() {
				t.Fatal("retained ACCEPT snapshot", err)
			}
		})
	}
}

func TestS2CMessageHighestAccepted(t *testing.T) {
	f := s2cTestCluster(t, 3)
	projection := f.genesis.state.projection
	op := s1Operation(t, projection, testIdentity(), s1Changes(s1ReaderRole()))
	makeH := func(nonce byte) *s2cHistoricalH {
		original := s1Seal(projection, op, nonce, false)
		original.serial = uint64(nonce)
		h, err := verifyHistoricalH(f.trust, s2cTestSealH(t, f.trust, 1, f.keys[1], original))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	h1, h2 := makeH(1), makeH(2)
	prepare, err := s2cSignPrepare(f.trust, f.keys[3], 3, 1, f.genesis.state.prefix, s2cBallot{9, 3})
	if err != nil {
		t.Fatal(err)
	}
	promise := func(member uint32, b s2cBallot, h *s2cHistoricalH) *s2cMessage {
		admission := [32]byte{}
		if b.valid() {
			admission = [32]byte{0xad}
		}
		p, err := s2cSignPromise(f.trust, f.keys[member], member, prepare, b, h, admission)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	low := promise(1, s2cBallot{2, 1}, h2)
	high := promise(2, s2cBallot{3, 2}, h1)
	accept, err := s2cSignAccept(f.trust, f.keys[3], 3, prepare, []*s2cMessage{low, high}, h2, [32]byte{0xad})
	if err != nil || accept.h == nil || accept.h.raw != h1.raw {
		t.Fatal("highest accepted exact H not carried", err)
	}
	if len(accept.raw) != s2cMessageOverhead+len(accept.proof)+len(h1.raw) {
		t.Fatal("historical body duplicated in proof")
	}
	// A summary authenticates the accepted digest, but absent winning bytes
	// cannot be filled with a pending different value or canonical NOOP.
	missing := []byte(high.raw[:len(high.raw)-len(h1.raw)])
	binary.BigEndian.PutUint32(missing[len(s2cMessageMagic)+s2cSummaryBytes+4:], 0)
	if _, err = s2cDecodeMessage(f.trust, missing); err == nil {
		t.Fatal("missing winning bytes decoded")
	}
	badPromise := &s2cMessage{raw: string(missing)}
	if _, _, err = s2cSelectP1(f.trust, []*s2cMessage{low, badPromise}, h2); err == nil {
		t.Fatal("missing winner replaced")
	}
	conflict := promise(1, high.acceptedBallot, h2)
	if _, _, err = s2cSelectP1(f.trust, []*s2cMessage{conflict, high}, h2); err == nil {
		t.Fatal("equal accepted ballot conflict selected")
	}
	noop := promise(2, s2cBallot{4, 2}, nil)
	_, selected, err := s2cSelectP1(f.trust, []*s2cMessage{low, noop}, h2)
	if err != nil || selected != nil {
		t.Fatal("accepted NOOP overridden", err)
	}
	fresh1, fresh2 := promise(1, s2cBallot{}, nil), promise(2, s2cBallot{}, nil)
	_, selected, err = s2cSelectP1(f.trust, []*s2cMessage{fresh1, fresh2}, h2)
	if err != nil || selected.raw != h2.raw {
		t.Fatal("fresh candidate selection", err)
	}
	// Metadata mutation cannot alter an already serialized authenticated reply.
	changed := *high
	changed.h, changed.acceptedValue = h2, h2.digest()
	_, selected, err = s2cSelectP1(f.trust, []*s2cMessage{low, &changed}, h2)
	if err != nil || selected.raw != h1.raw {
		t.Fatal("mutable view replaced authenticated bytes", err)
	}
	if _, err = s2cSignPromise(f.trust, f.keys[1], 1, prepare, s2cBallot{10, 1}, h1, [32]byte{1}); err == nil {
		t.Fatal("accepted ballot exceeds promised ballot")
	}
}

func TestS2CMessageMixedEvidence(t *testing.T) {
	f := s2cTestCluster(t, 5)
	prepare, err := s2cSignPrepare(f.trust, f.keys[1], 1, 1, f.genesis.state.prefix, s2cBallot{3, 1})
	if err != nil {
		t.Fatal(err)
	}
	var promises []*s2cMessage
	for i := uint32(1); i <= 3; i++ {
		p, err := s2cSignPromise(f.trust, f.keys[i], i, prepare, s2cBallot{}, nil, [32]byte{})
		if err != nil {
			t.Fatal(err)
		}
		promises = append(promises, p)
	}
	accept, err := s2cSignAccept(f.trust, f.keys[1], 1, prepare, promises, nil, [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	var votes []*s2cMessage
	for i := uint32(1); i <= 3; i++ {
		v, err := s2cSignAccepted(f.trust, f.keys[i], i, accept, accept.admission)
		if err != nil {
			t.Fatal(err)
		}
		votes = append(votes, v)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*s2cMessage)
	}{
		{"slot", func(m *s2cMessage) { m.slot++ }},
		{"predecessor", func(m *s2cMessage) { m.predecessor[1]++ }},
		{"counter", func(m *s2cMessage) { m.ballot.Counter++ }},
		{"proposer", func(m *s2cMessage) { m.ballot.Member = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := *promises[2]
			tc.mutate(&p)
			mixed, err := s2cSignMessage(f.trust, f.keys[3], p)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s2cSelectP1(f.trust, []*s2cMessage{promises[0], promises[1], mixed}, nil); err == nil {
				t.Fatal("mixed Promise attempt")
			}
			v := *votes[2]
			tc.mutate(&v)
			mixed, err = s2cSignMessage(f.trust, f.keys[3], v)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s2cBuildQC(f.trust, []*s2cMessage{votes[0], votes[1], mixed}); err == nil {
				t.Fatal("mixed Accepted attempt")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*s2cMessage)
	}{
		{"value", func(m *s2cMessage) { m.value[0]++ }},
		{"admission", func(m *s2cMessage) { m.admission[0]++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := *votes[2]
			tc.mutate(&v)
			mixed, err := s2cSignMessage(f.trust, f.keys[3], v)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s2cBuildQC(f.trust, []*s2cMessage{votes[0], votes[1], mixed}); err == nil {
				t.Fatal("mixed vote fields")
			}
		})
	}
	if _, err = s2cSignPrepare(f.trust, f.keys[2], 1, 1, f.genesis.state.prefix, s2cBallot{1, 1}); err == nil {
		t.Fatal("wrong key")
	}
	if _, err = s2cSignPrepare(f.trust, f.keys[1], 99, 1, f.genesis.state.prefix, s2cBallot{1, 1}); err == nil {
		t.Fatal("unknown signer")
	}
	if _, err = s2cSignPrepare(f.trust, f.keys[2], 2, 1, f.genesis.state.prefix, s2cBallot{1, 1}); err == nil {
		t.Fatal("nonowner Prepare")
	}
	other := *f.trust
	other.scope[0]++
	if _, err = s2cDecodeMessage(&other, []byte(prepare.raw)); err == nil {
		t.Fatal("cross-scope replay")
	}
	other = *f.trust
	other.members = append([]s2cMember(nil), f.members...)
	other.members[0].Proposer = false
	if _, err = s2cDecodeMessage(&other, []byte(prepare.raw)); err == nil {
		t.Fatal("unauthorized proposer")
	}
}

func TestS2CMessageCanonicalAndBounds(t *testing.T) {
	f := s2cTestCluster(t, 3)
	prepare, err := s2cSignPrepare(f.trust, f.keys[1], 1, 1, f.genesis.state.prefix, s2cBallot{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	var promises, votes []*s2cMessage
	for i := uint32(1); i <= 2; i++ {
		p, err := s2cSignPromise(f.trust, f.keys[i], i, prepare, s2cBallot{}, nil, [32]byte{})
		if err != nil {
			t.Fatal(err)
		}
		promises = append(promises, p)
	}
	accept, err := s2cSignAccept(f.trust, f.keys[1], 1, prepare, promises, nil, [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(1); i <= 2; i++ {
		v, err := s2cSignAccepted(f.trust, f.keys[i], i, accept, accept.admission)
		if err != nil {
			t.Fatal(err)
		}
		votes = append(votes, v)
	}
	chosen, err := s2cMakeChosen(f.trust, accept, votes)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*s2cMessage{prepare, promises[0], accept, votes[0], chosen} {
		t.Run(fmt.Sprint(m.kind), func(t *testing.T) {
			// Every header/signature/length byte participates in verification.
			for i := 0; i < s2cMessageOverhead; i++ {
				bad := []byte(m.raw)
				bad[i] ^= 1
				if _, err := s2cDecodeMessage(f.trust, bad); err == nil {
					t.Fatalf("accepted altered metadata byte %d", i)
				}
			}
			for i := 0; i < len(m.raw); i++ {
				if _, err := s2cDecodeMessage(f.trust, []byte(m.raw[:i])); err == nil {
					t.Fatalf("accepted truncated length %d", i)
				}
			}
			if _, err := s2cDecodeMessage(f.trust, append([]byte(m.raw), 0)); err == nil {
				t.Fatal("trailing bytes")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		limit func(*s2cTrust)
	}{
		{"header", func(t *s2cTrust) { t.bounds.HeaderBytes = uint64(s2cMessageOverhead - 1) }},
		{"proof", func(t *s2cTrust) { t.bounds.ProofBytes = uint64(len(chosen.proof) - 1) }},
		{"payload", func(t *s2cTrust) { t.bounds.PayloadBytes = uint64(len(chosen.raw) - 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limited := *f.trust
			tc.limit(&limited)
			if _, err := s2cDecodeMessage(&limited, []byte(chosen.raw)); err == nil {
				t.Fatal("one-byte-short bound")
			}
		})
	}
	exact := *f.trust
	exact.bounds.HeaderBytes = uint64(s2cMessageOverhead)
	exact.bounds.ProofBytes = uint64(len(chosen.proof))
	exact.bounds.PayloadBytes = uint64(len(chosen.raw))
	if _, err = s2cDecodeMessage(&exact, []byte(chosen.raw)); err != nil {
		t.Fatal("exact bounds", err)
	}
	if got := s2cChosenBytesBound(&exact, nil); got != uint64(len(chosen.raw)) {
		t.Fatal("largest permitted witness ignored common byte limits", got)
	}
	exact.bounds.ProofBytes--
	if s2cChosenBytesBound(&exact, nil) != 0 {
		t.Fatal("unrepresentable majority reserved as possible choice")
	}
	for _, b := range []s2cBallot{{}, {1, 0}, {0, 1}, {1, 99}} {
		if _, err = s2cSignPrepare(f.trust, f.keys[1], 1, 1, f.genesis.state.prefix, b); err == nil {
			t.Fatal("invalid ballot", b)
		}
	}
	for _, slot := range []uint64{0, math.MaxUint64 - 1, math.MaxUint64} {
		if _, err = s2cSignPrepare(f.trust, f.keys[1], 1, slot, f.genesis.state.prefix, s2cBallot{1, 1}); err == nil {
			t.Fatal("unrepresentable slot", slot)
		}
	}
	if (s2cBallot{2, 1}).compare(s2cBallot{1, 3}) <= 0 || (s2cBallot{2, 1}).compare(s2cBallot{2, 2}) >= 0 || (s2cBallot{2, 1}).compare(s2cBallot{2, 1}) != 0 {
		t.Fatal("ballot order")
	}
	// An attacker can recompute an unsigned chosen envelope hash, but cannot
	// turn a duplicated/reordered signer summary into a canonical majority.
	proof := []byte(chosen.proof)
	start := len(s2cProofMagic) + 1
	copy(proof[start+s2cSummaryBytes:], proof[start:start+s2cSummaryBytes])
	bad := *chosen
	bad.proof = string(proof)
	if _, err = s2cSignMessage(f.trust, nil, bad); err == nil {
		t.Fatal("duplicate signed summaries in QC")
	}
	proof = []byte(chosen.proof)
	proof[len(s2cProofMagic)] = 32
	bad.proof = string(proof)
	if _, err = s2cSignMessage(f.trust, nil, bad); err == nil {
		t.Fatal("overflowed proof count")
	}
	if _, err = s2cSignMessage(f.trust, ed25519.PrivateKey{}, *prepare); err == nil {
		t.Fatal("short private key")
	}
}
