package security

import "testing"

func TestAuthorityOriginApprovalNeedsExactRetainedPurposeOperation(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := owners[1]
	ctx := s3aTestContext(t)
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := o.prepare(ctx, authorityFakeCredentialProducer{}, o.network.kernel.replayState.projection.cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.beginPurpose(ctx, r, authorityFakeCredentialProducer{}); err == nil {
		t.Fatal("ordinary edit minted high-impact purpose")
	}
	if _, _, err := o.startPurpose(ctx, [32]byte{}); err == nil {
		t.Fatal("unknown ticket")
	}
	if _, err := o.completePurpose(ctx, [32]byte{}, authorityFakeCredentialProducer{}); err == nil {
		t.Fatal("access facts became Code approval")
	}
	if len(o.purposes.pending) != 0 {
		t.Fatal("refusal retained proof")
	}
}
