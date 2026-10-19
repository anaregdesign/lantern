package security

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// This deterministic owner fixture uses the existing private factual time and
// credential ports. It does not qualify the normal configured-source factory.
func TestAuthoritySingletonDurableAdmissionAndRenewal(t *testing.T) {
	n, owners, ticks := authorityTestCompositeMembers(t, 1, 1, nil)
	o := owners[1]
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if len(n.f.members) != 1 || len(o.network.peers) != 1 || n.f.trust.majority() != 1 || o.descriptor.PublicKey == n.f.members[0].PublicKey {
		t.Fatal("singleton fixture did not preserve independent identity/origin ownership")
	}
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal("self renewal", err)
	}
	public := &CurrentAuthority{origin: o}
	if !public.Ready(ctx) {
		t.Fatal("single enrolled voter did not become ready")
	}
	oldRenewal := o.network.receiver.active
	review, err := o.prepare(ctx, authorityFakeCredentialProducer{}, o.network.kernel.replayState.projection.cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := o.consume(ctx, review, authorityFakeCredentialProducer{}, [32]byte{})
	if err != nil || sealed.raw == "" || o.network.kernel.originSerial != 1 {
		t.Fatal("durable original H", err)
	}
	h, err := verifyHistoricalH(n.f.trust, []byte(sealed.raw))
	if err != nil || h.handoff.id != review.id {
		t.Fatal("original H lost its identity", err)
	}
	receipt, err := o.network.Drive(ctx, sealed.digest)
	if err != nil || receipt.ControlSlot != 1 {
		t.Fatal("self-driven quorum did not drain", receipt, err)
	}
	if public.Ready(ctx) || o.network.receiver.active != oldRenewal {
		t.Fatal("old-cut renewal survived a new materialized cut")
	}
	if err := o.network.renewAuthority(ctx); err != nil || !public.Ready(ctx) {
		t.Fatal("fresh self renewal after Apply", err)
	}
	if o.network.receiver.active == oldRenewal || len(o.network.receiver.active.certificate.votes) != 1 {
		t.Fatal("self renewal reused old capability or fabricated extra votes")
	}
	again, err := o.consume(ctx, review, nil, [32]byte{})
	if err != nil || again.outcome == nil || again.outcome.disposition != S1Applied || o.network.kernel.originSerial != 1 {
		t.Fatal("retry replaced original outcome or consumed another serial", err)
	}
	if _, err := o.network.Drive(ctx, [32]byte{}); err != nil {
		t.Fatal("self-driven NOOP", err)
	}
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	ticks[1].Add(authorityRenewalLifetime)
	if public.Ready(ctx) {
		t.Fatal("singleton bypassed renewal expiry")
	}
}

func TestSingletonProtocolRetainsPromisesAcceptsAndOriginals(t *testing.T) {
	n := s2cTestNativeCluster(t, 1, nil)
	n.begin(1, [32]byte{})
	n.send(s2cPrepare, 1, 1)
	promise := n.take(s2cPromise, 1, 1)
	n.restart(1)
	if err := n.deliver(promise); err != nil {
		t.Fatal("retained self promise", err)
	}
	accept := n.take(s2cAccept, 1, 1)
	n.restart(1)
	retry, err := n.nodes[1].Retry()
	if err != nil || len(retry) != 1 || !bytes.Equal(retry[0].Bytes, accept.raw) {
		t.Fatal("singleton SELECT retry changed", err)
	}
	if err := n.deliver(accept); err != nil {
		t.Fatal(err)
	}
	vote := n.take(s2cAccepted, 1, 1)
	n.restart(1)
	if err := n.deliver(vote); err != nil {
		t.Fatal("retained self ACCEPT", err)
	}
	_, before, err := n.nodes[1].ReadLocalCut()
	if err != nil || before.ControlSlot != 1 {
		t.Fatal("singleton did not complete CHOSEN/B/DRAINED", err)
	}
	n.restart(1)
	_, after, err := n.nodes[1].ReadLocalCut()
	if err != nil || before != after {
		t.Fatal("singleton replay changed drained result", err)
	}
}
