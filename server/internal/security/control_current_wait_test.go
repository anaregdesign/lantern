package security

import (
	"testing"
	"time"
)

func TestCurrentAuthorityAppliedWaitUsesLowerBound(t *testing.T) {
	_, owners, ticks := authorityTestComposite(t)
	o := owners[1]
	ctx := s3aTestContext(t)
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := o.prepare(ctx, authorityFakeCredentialProducer{}, o.network.kernel.replayState.projection.cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	h, err := o.consume(ctx, r, authorityFakeCredentialProducer{}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.observeApplied(ctx, r.id, r.operation); err == nil {
		t.Fatal("origin ACK claimed Apply")
	}
	if _, err := o.network.Drive(ctx, h.digest); err != nil {
		t.Fatal(err)
	}
	wait, err := o.observeApplied(ctx, r.id, r.operation)
	if err != nil {
		t.Fatal(err)
	}
	ticks[1].Add(authorityRenewalLifetime)
	if done, err := wait.complete(ctx); done || err != nil {
		t.Fatal("upper bound substituted for completed wait", done, err)
	}
	ticks[1].Add(uint64(20 * time.Millisecond))
	if done, err := wait.complete(ctx); !done || err != nil {
		t.Fatal("conservative completed wait", done, err)
	}
	if err := o.network.Close(); err != nil {
		t.Fatal(err)
	}
	if done, err := wait.complete(ctx); done || err == nil {
		t.Fatal("closed wait restored")
	}
}
