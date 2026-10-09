package security

import (
	"testing"
	"time"
)

func TestCurrentPublicObservationUsesVolatileLowerBound(t *testing.T) {
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
	review, _, err := o.Prepare(ctx, a, producer, o.Profile(), cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	unresolved, _ := o.Inspect(ctx, review.Profile, review.ID, review.Operation.Digest())
	if o.ObserveResult(ctx, unresolved).StopObservation != CurrentStopNotObserved {
		t.Fatal("unresolved original started stop observation")
	}
	result, err := o.Apply(ctx, review, producer, [32]byte{})
	if err != nil || result.Original == nil {
		t.Fatal(err)
	}
	if o.ObserveResult(ctx, result).StopObservation != CurrentStopWaiting {
		t.Fatal("Apply did not start independent observation")
	}
	ticks[1].Add(authorityRenewalLifetime)
	if o.ObserveResult(ctx, result).StopObservation != CurrentStopWaiting {
		t.Fatal("upper elapsed endpoint completed observation")
	}
	ticks[1].Add(uint64(20 * time.Millisecond))
	completed := o.ObserveResult(ctx, result)
	if completed.StopObservation != CurrentOldCutAuthorizationsStopped || completed.Original != result.Original {
		t.Fatal("lower-bound completion changed original outcome")
	}
	// Observer state is process-local: a fresh facade cannot infer completion
	// from the original result's dates or another observer's derived status.
	lost := &CurrentAuthority{origin: owners[1]}
	if lost.ObserveResult(ctx, completed).StopObservation != CurrentStopWaiting {
		t.Fatal("observer loss restored elapsed proof from public result")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if o.ObserveResult(ctx, completed).StopObservation != CurrentStopNotObserved || len(o.observations.items) != 0 {
		t.Fatal("closed observer retained completion")
	}
}
