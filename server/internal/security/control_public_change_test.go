package security

import (
	"errors"
	"testing"
	"time"
)

func TestCurrentPublicChangeRetainsOriginalAndFreshDisclosure(t *testing.T) {
	_, owners, ticks := authorityTestComposite(t)
	o := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	a, err := o.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	review, purpose, err := o.Prepare(ctx, a, producer, o.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil || purpose || review.ID.Namespace != o.origin.descriptor.Namespace {
		t.Fatal("prepare", err, purpose)
	}
	decoded, err := o.DecodeReview(review.Profile, review.ID, review.Operation.Actor(), cut, s1Changes(s1ReaderRole()), review.Operation.Digest())
	if err != nil || decoded != review {
		t.Fatal("review did not survive wire fields", err)
	}
	before, err := o.Inspect(ctx, review.Profile, review.ID, review.Operation.Digest())
	if err != nil || before.Progress != CurrentUnresolved || before.Original != nil {
		t.Fatal("prepare claimed durable H", err)
	}
	if _, err := o.ObserveApplied(ctx, review); err == nil {
		t.Fatal("started observation before Apply")
	}
	r, err := o.Apply(ctx, review, producer, [32]byte{})
	if err != nil || r.Progress != CurrentApplied || r.Original == nil || r.Original.Disposition() != S1Applied {
		t.Fatal("Apply", err, r.Progress)
	}
	if a.Check(ctx, time.Time{}) == nil {
		t.Fatal("pre-Apply cut remained disclosure authority")
	}
	serial, floor := o.origin.network.kernel.originSerial, o.origin.network.kernel.p.floor
	// Recovery of a known original precedes credentials/purpose. This result is
	// not disclosure permission, which the service must obtain independently.
	replayed, err := o.Apply(ctx, review, nil, [32]byte{99})
	if err != nil || replayed.Original != r.Original || serial != o.origin.network.kernel.originSerial || floor != o.origin.network.kernel.p.floor {
		t.Fatal("known original reconsumed", err)
	}
	if _, err := o.BeginPurpose(ctx, review, nil); err == nil {
		t.Fatal("known Apply requested new purpose")
	}
	w, err := o.ObserveApplied(ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	ticks[1].Add(authorityRenewalLifetime)
	if done, err := w.Complete(ctx); done || err != nil {
		t.Fatal("upper endpoint completed stop observation", done, err)
	}
	ticks[1].Add(uint64(20 * time.Millisecond))
	if done, err := w.Complete(ctx); !done || err != nil {
		t.Fatal("lower endpoint did not complete", done, err)
	}
}

func TestCurrentPublicChangeUnknownAndConflictingReviewNeverMint(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	a, err := o.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	r, _, err := o.Prepare(ctx, a, producer, o.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	unknown := r
	unknown.ID.Nonce[0] ^= 0xff
	if result, err := o.Apply(ctx, unknown, producer, [32]byte{}); !errors.Is(err, errS2CUnknown) || result.Progress != CurrentUnresolved || o.origin.network.kernel.originSerial != 0 {
		t.Fatal("caller ID allocated or proved absence", result.Progress, err)
	}
	conflict := r
	conflict.Operation, err = NewS1Operation(r.Operation.Actor(), cut, s1Changes(Change{Kind: DeleteRole, RoleID: "reader"}), DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Apply(ctx, conflict, producer, [32]byte{}); !errors.Is(err, ErrChangeConflict) || o.origin.network.kernel.originSerial != 0 {
		t.Fatal("changed intent minted H", err)
	}
	wrong := o.Profile()
	wrong.Version++
	if _, err := o.DecodeReview(wrong, r.ID, a.Identity(), cut, s1Changes(s1ReaderRole()), r.Operation.Digest()); err == nil {
		t.Fatal("unknown public version accepted")
	}
	if _, _, err := o.Prepare(ctx, a, producer, wrong, cut, s1Changes(s1ReaderRole())); err == nil {
		t.Fatal("unknown prepare version accepted")
	}
}
