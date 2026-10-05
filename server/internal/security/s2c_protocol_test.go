package security

import (
	"bytes"
	"math"
	"testing"
)

func TestS2CProtocolDurableRetryAndEqualPromise(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	n.begin(1, [32]byte{})
	original := n.take(s2cPrepare, 1, 3)
	n.restart(1)
	retry, e := n.nodes[1].Retry()
	if e != nil {
		t.Fatal(e)
	}
	for _, out := range retry {
		if !bytes.Equal(out.Bytes, original.raw) {
			t.Fatal("BALLOT retry changed signed bytes")
		}
	}
	n.send(s2cPrepare, 1, 1)
	n.send(s2cPrepare, 1, 2)
	n.send(s2cPromise, 2, 1)
	n.send(s2cPromise, 1, 1)
	a := n.take(s2cAccept, 1, 3)
	n.restart(1)
	retry, e = n.nodes[1].Retry()
	if e != nil {
		t.Fatal(e)
	}
	for _, out := range retry {
		if !bytes.Equal(out.Bytes, a.raw) {
			t.Fatal("SELECT retry changed signed bytes")
		}
	}
	// Node 3 never received Prepare. Its durable ACCEPT establishes a promise
	// and exact snapshot without requiring a separate equal-ballot append.
	out, e := n.nodes[3].Receive(a.raw)
	if e != nil || len(out) != 1 {
		t.Fatal(e)
	}
	count := n.nodes[3].p.count
	response, e := n.nodes[3].Receive(original.raw)
	if e != nil || len(response) != 1 || n.nodes[3].p.count != count {
		t.Fatal("equal Prepare appended", e)
	}
	p, e := s2cDecodeMessage(n.fixture.trust, response[0].Bytes)
	if e != nil || p.kind != s2cPromise || p.acceptedBallot != n.nodes[3].accepted.ballot || p.acceptedValue != n.nodes[3].accepted.value {
		t.Fatal("accept snapshot", e)
	}
	n.restart(3)
	recovered, e := n.nodes[3].Receive(original.raw)
	if e != nil || len(recovered) != 1 || !bytes.Equal(recovered[0].Bytes, response[0].Bytes) || n.nodes[3].p.count != count {
		t.Fatal("recovery changed frozen Promise", e)
	}
}

func TestS2CProtocolCounterExhaustionPersists(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	p, e := s2cSignPrepare(n.fixture.trust, n.fixture.keys[2], 2, 1, n.fixture.genesis.state.prefix, s2cBallot{math.MaxUint64, 2})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = n.nodes[1].Receive([]byte(p.raw)); e != nil {
		t.Fatal(e)
	}
	before := n.nodes[1].p.floor
	if _, e = n.nodes[1].Begin([32]byte{}); e == nil || n.nodes[1].p.floor != before {
		t.Fatal("counter wrapped/appended")
	}
	n.restart(1)
	if _, e = n.nodes[1].Begin([32]byte{}); e == nil || n.nodes[1].p.floor != before {
		t.Fatal("restart refilled counter")
	}
}

func TestS2CProtocolSameBallotSecondValueAfterRestart(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	n.selectValue(1, [32]byte{}, 1, 2)
	a := n.take(s2cAccept, 1, 3)
	if _, e := n.nodes[3].Receive(a.raw); e != nil {
		t.Fatal(e)
	}
	n.restart(3)
	// Deliberately violate the proposer's one-value rule with a second validly
	// signed message. This negative control is not scheduler-supplied evidence.
	op := s1Operation(t, n.fixture.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{9}}
	raw := s2cTestSeal(t, n.fixture.trust, 1, n.fixture.keys[1], id, op, 9, false)
	h, e := verifyHistoricalH(n.fixture.trust, raw)
	if e != nil {
		t.Fatal(e)
	}
	// Retained actual Promise messages form this adversarial request's P1.
	promises := []*s2cMessage{n.nodes[1].promise, n.nodes[2].promise}
	preview, e := s2cPreviewCandidate(n.fixture.trust, n.nodes[3].b.state, n.fixture.genesis.roots, n.configs[3].BScope, n.nodes[3].b.receipt, h, s2cLogicalCommit(n.fixture.trust, 1, h.digest()))
	if e != nil {
		t.Fatal(e)
	}
	bad, e := s2cSignAccept(n.fixture.trust, n.fixture.keys[1], 1, n.nodes[1].prepare, promises, h, preview.admission)
	if e != nil {
		t.Fatal(e)
	}
	before := n.nodes[3].p.floor
	if out, e := n.nodes[3].Receive([]byte(bad.raw)); e == nil || len(out) != 0 || n.nodes[3].p.floor != before {
		t.Fatal("second value accepted after restart", e)
	}
}

func TestS2CProtocolChosenCounterAdvancesNextBallot(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	// A higher real authenticated Prepare is retained before the proposer begins.
	high, e := s2cSignPrepare(n.fixture.trust, n.fixture.keys[2], 2, 1, n.fixture.genesis.state.prefix, s2cBallot{99, 2})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = n.nodes[1].Receive([]byte(high.raw)); e != nil {
		t.Fatal(e)
	}
	n.selectValue(1, [32]byte{}, 1, 2)
	n.choose(1, 1, 2)
	n.send(s2cChosen, 1, 3)
	out, e := n.nodes[3].Begin([32]byte{})
	if e != nil {
		t.Fatal(e)
	}
	m, e := s2cDecodeMessage(n.fixture.trust, out[0].Bytes)
	if e != nil || m.ballot.Counter != 101 {
		t.Fatal("forgot observed choice counter", m, e)
	}
	n.restart(3)
	retry, e := n.nodes[3].Retry()
	if e != nil || !bytes.Equal(retry[0].Bytes, out[0].Bytes) {
		t.Fatal("counter/ballot recovery", e)
	}
}

func TestS2CProtocolObservedRejectedPromiseRaisesDurableBallot(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	p, e := s2cSignPrepare(n.fixture.trust, n.fixture.keys[2], 2, 1, n.fixture.genesis.state.prefix, s2cBallot{100, 2})
	if e != nil {
		t.Fatal(e)
	}
	promise, e := s2cSignPromise(n.fixture.trust, n.fixture.keys[3], 3, p, s2cBallot{}, nil, [32]byte{})
	if e != nil {
		t.Fatal(e)
	}
	before := n.nodes[1].p.floor
	if out, e := n.nodes[1].Receive([]byte(promise.raw)); e == nil || len(out) != 0 || n.nodes[1].p.floor != before {
		t.Fatal("unsolicited Promise accepted/appended", e)
	}
	out, e := n.nodes[1].Begin([32]byte{})
	if e != nil {
		t.Fatal(e)
	}
	m, e := s2cDecodeMessage(n.fixture.trust, out[0].Bytes)
	if e != nil || m.ballot.Counter != 101 {
		t.Fatal("ignored authenticated rejected counter", e)
	}
	n.restart(1)
	retry, e := n.nodes[1].Retry()
	if e != nil || !bytes.Equal(retry[0].Bytes, out[0].Bytes) {
		t.Fatal("durable increase lost", e)
	}
	p, e = s2cSignPrepare(n.fixture.trust, n.fixture.keys[2], 2, 1, n.fixture.genesis.state.prefix, s2cBallot{math.MaxUint64, 2})
	if e != nil {
		t.Fatal(e)
	}
	promise, e = s2cSignPromise(n.fixture.trust, n.fixture.keys[3], 3, p, s2cBallot{}, nil, [32]byte{})
	if e != nil {
		t.Fatal(e)
	}
	tampered := []byte(promise.raw)
	tampered[len(s2cMessageMagic)+s2cSignedHeaderBytes] ^= 1
	if _, e = n.nodes[1].Receive(tampered); e == nil {
		t.Fatal("tampered maximum signature accepted")
	}
	out, e = n.nodes[1].Begin([32]byte{})
	if e != nil {
		t.Fatal("unverified counter exhausted participant", e)
	}
	m, e = s2cDecodeMessage(n.fixture.trust, out[0].Bytes)
	if e != nil || m.ballot.Counter != 102 {
		t.Fatal("unverified counter was observed", e)
	}
	if _, e = n.nodes[1].Receive([]byte(promise.raw)); e == nil {
		t.Fatal("mismatched maximum Promise")
	}
	before = n.nodes[1].p.floor
	if out, e = n.nodes[1].Begin([32]byte{}); e == nil || len(out) != 0 || n.nodes[1].p.floor != before {
		t.Fatal("observed maximum counter wrapped", e)
	}
}
