package security

import (
	"fmt"
	"testing"
)

func TestCurrentPublicAuditUsesOriginalLedgerAndExactAdmission(t *testing.T) {
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
	empty, err := o.Audit(ctx, a, "")
	if err != nil || empty.Len() != 0 {
		t.Fatal("empty", err)
	}
	cut, _ := a.CurrentCut()
	review, _, err := o.Prepare(ctx, a, producer, o.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := o.Apply(ctx, review, producer, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Audit(ctx, a, ""); err == nil {
		t.Fatal("old cut disclosed audit")
	}
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err := o.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	view, err := o.Audit(ctx, fresh, fmt.Sprintf("%d:%x", review.ID.Namespace, review.ID.Nonce))
	if err != nil || view.Len() != 1 {
		t.Fatal("view", err)
	}
	rows, err := view.Page(0, 1)
	if err != nil || rows[0].Result.Original != result.Original || rows[0].Operation != S1Management || rows[0].Actor == [32]byte{} {
		t.Fatal("original", err)
	}
	if rows[0].Result.StopObservation != CurrentStopNotObserved {
		t.Fatal("audit invented stop observation")
	}
	if empty.Len() != 0 {
		t.Fatal("immutable view changed")
	}
	for _, bounds := range [][2]int{{-1, 0}, {1, 0}, {0, 2}, {0, 201}} {
		if _, err := view.Page(bounds[0], bounds[1]); err == nil {
			t.Fatal("accepted invalid bounds", bounds)
		}
	}
	unknown, err := o.Audit(ctx, fresh, "999:unknown")
	if err != nil || unknown.Len() != 0 {
		t.Fatal("exact filter", err)
	}
}
