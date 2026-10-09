package security

import "testing"

func TestAuthorityOriginForwardPreservesOriginalAcrossOriginLoss(t *testing.T) {
	n, owners, _ := authorityTestComposite(t)
	ctx := s3aTestContext(t)
	origin := owners[1]
	if err := origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := origin.prepare(ctx, authorityFakeCredentialProducer{}, n.f.genesis.state.projection.cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	original, err := origin.consume(ctx, r, authorityFakeCredentialProducer{}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if err := origin.network.Close(); err != nil {
		t.Fatal(err)
	}
	relay := owners[2]
	carried, err := relay.carryOriginal(ctx, []byte(original.raw))
	if err != nil || carried.raw != original.raw || carried.digest != original.digest || relay.network.kernel.originSerial != 0 || len(relay.network.kernel.serials) != 0 {
		t.Fatal("cross-origin input changed or allocated local origin", err)
	}
	before := relay.network.kernel.p.floor
	if _, err := relay.carryOriginal(ctx, []byte(original.raw)); err != nil || relay.network.kernel.p.floor != before {
		t.Fatal("repeat forwarded original appended again", err)
	}
	if _, err := relay.network.Drive(ctx, carried.digest); err != nil {
		t.Fatal("remaining native majority could not choose original", err)
	}
	status, outcome, _, err := relay.network.kernel.LookupOriginal(r.id)
	if err != nil || status != S1Known || outcome.operation != r.operation || outcome.disposition != S1Applied {
		t.Fatal("changed original identity/outcome", err)
	}
	result, err := relay.carryOriginal(ctx, []byte(original.raw))
	if err != nil || result.outcome == nil || result.outcome.id.Namespace != origin.descriptor.Namespace {
		t.Fatal("retry substituted receiver namespace", err)
	}
	floors, err := relay.network.Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.network.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := resumeS3AOwner(n.configs[2], floors)
	if err != nil {
		t.Fatal("foreign original native P replay", err)
	}
	n.nodes[2] = recovered
	if recovered.kernel.origins[original.digest].raw != original.raw || recovered.kernel.originSerial != 0 {
		t.Fatal("restamped foreign H at recovery")
	}
}
