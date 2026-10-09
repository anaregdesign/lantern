package security

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCurrentPublicLookupRecoversExactOriginalOverPrivateWire(t *testing.T) {
	_, owners, _ := authorityTestCompositeOrigins(t, 2)
	origin, relay := &CurrentAuthority{origin: owners[1]}, &CurrentAuthority{origin: owners[3]}
	ctx := s3aTestContext(t)
	if err := origin.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	if origin.OriginEnabled() == false || relay.OriginEnabled() || len(relay.origin.key) != 0 {
		t.Fatal("three members/two origins lost independent origin enrollment")
	}
	if err := relay.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	observer, err := relay.Admit(ctx, producer)
	if err != nil {
		t.Fatal("origin-free member could not admit a public read", err)
	}
	observerCut, _ := observer.CurrentCut()
	if _, _, err := relay.Prepare(ctx, observer, producer, relay.Profile(), observerCut, s1Changes(s1ReaderRole())); err == nil {
		t.Fatal("origin-free participant minted new public work")
	}
	a, err := origin.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	r, _, err := origin.Prepare(ctx, a, producer, origin.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := origin.prepared(r)
	if err != nil {
		t.Fatal(err)
	}
	h, err := origin.origin.consume(ctx, pending, producer, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	// No phase 1 or delivery: only the original origin has H. The facade on a
	// different node must fetch it over authenticated TLS, not take a raw H API.
	relay.origin.network.driveSlot <- struct{}{} // bounded scheduling credit is occupied
	status, err := relay.Lookup(ctx, r.Profile, r.ID, r.Operation.Digest())
	<-relay.origin.network.driveSlot
	if !errors.Is(err, errS3ACredit) || status.Progress != CurrentOriginDurable || status.Original != nil || relay.origin.network.kernel.originSerial != 0 {
		t.Fatal("cross-node original lookup", err, status.Progress)
	}
	if relay.origin.network.kernel.origins[h.digest].raw != h.raw {
		t.Fatal("private lookup changed original bytes")
	}
	if err := origin.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := relay.Lookup(ctx, r.Profile, r.ID, r.Operation.Digest())
	if err != nil || result.Progress != CurrentApplied || result.Original.ID() != r.ID || result.Original.HandoffDigest() != h.digest || relay.origin.network.kernel.originSerial != 0 {
		t.Fatal("status-only recovery failed original completion", err, result.Progress)
	}
	if result.Original.Disposition() != S1Applied {
		t.Fatal("unexpected original disposition")
	}
}

func TestCurrentPublicLookupBoundsAndLocalAbsence(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := owners[1].network
	ctx := s3aTestContext(t)
	cut := o.kernel.trust.genesis.state.projection.cut
	id := FullChangeID{S1Version, cut.Domain, cut.Cohort, owners[1].descriptor.Namespace, [16]byte{99}}
	q, err := json.Marshal(currentOriginalQuery{id, [32]byte{7}})
	if err != nil {
		t.Fatal(err)
	}
	if response, err := o.originalResponse(ctx, q); err != nil || response != nil {
		t.Fatal("absence manufactured original", err)
	}
	if _, err := o.findOriginal(ctx, id, [32]byte{7}); !errors.Is(err, errS2CUnknown) {
		t.Fatal("missing original treated as safe retry", err)
	}
	o.originalSlot <- struct{}{}
	if _, err := o.findOriginal(ctx, id, [32]byte{7}); !errors.Is(err, errS3ACredit) {
		t.Fatal("outbound lookup credit exceeded", err)
	}
	<-o.originalSlot
	for _, raw := range [][]byte{append(q, '\n'), []byte(`{"ID":null,"Intent":null}`), make([]byte, currentOriginalRequestLimit+1)} {
		if _, err := o.originalResponse(ctx, raw); err == nil {
			t.Fatal("noncanonical/oversize lookup accepted")
		}
	}
}
