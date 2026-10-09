package security

import (
	"bytes"
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCurrentPublicAdmissionRequiresNativeRenewal(t *testing.T) {
	_, origins, counters := authorityTestComposite(t)
	o := &CurrentAuthority{origin: origins[1]}
	ctx := s3aTestContext(t)
	if _, err := o.Admit(ctx, authorityFakeCredentialProducer{}); err == nil {
		t.Fatal("installed state without renewal granted public admission")
	}
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := o.Admit(ctx, authorityFakeCredentialProducer{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision() != nil || a.Snapshot() == nil || a.ScopeBinding() == [32]byte{} {
		t.Fatal("current admission forged a legacy revision or lost immutable policy")
	}
	cut, current := a.CurrentCut()
	profile, bound := a.CurrentProfile()
	if !current || !bound || cut != o.origin.network.kernel.replayState.projection.cut || profile != o.Profile() || profile.Version != 2 {
		t.Fatal("incomplete current identity")
	}
	for _, wall := range []time.Time{{}, time.Unix(0, int64(^uint64(0)>>1))} {
		if err := a.Check(ctx, wall); err != nil {
			t.Fatal("host wall time became current authority", err)
		}
	}
	if _, err := NewAdmission(a.Identity(), a.AuthTime(), a.ExpiresAt(), a.Revision(), func(_ context.Context, _ *Revision) error { return nil }); err == nil {
		t.Fatal("current snapshot escaped through a legacy admission constructor")
	}
	counters[1].Add(uint64(20 * time.Second))
	if err := a.Check(ctx, time.Time{}); err == nil {
		t.Fatal("expired native renewal retained current authority")
	}
}

func TestCurrentPublicAdmissionDoesNotRelabelAfterApply(t *testing.T) {
	_, origins, _ := authorityTestComposite(t)
	o := &CurrentAuthority{origin: origins[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	before, err := o.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := before.CurrentCut()
	r, err := o.origin.prepare(ctx, producer, cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := o.origin.consume(ctx, r, producer, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.origin.network.Drive(ctx, result.digest); err != nil {
		t.Fatal(err)
	}
	authorityTestConverge(t, origins)
	if err = o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	if before.Check(ctx, time.Time{}) == nil {
		t.Fatal("old cut authorized disclosure after management Apply")
	}
	after, err := o.Admit(ctx, producer)
	if err != nil || after.ScopeBinding() == before.ScopeBinding() {
		t.Fatal("post-Apply fresh admission did not advance complete binding", err)
	}
	if oldCut, _ := before.CurrentCut(); oldCut != cut {
		t.Fatal("prior immutable admission was rewritten")
	}
	if after.WithAuthentication(Authentication{Class: MachineActor}).Authentication() != after.Authentication() {
		t.Fatal("trusted legacy adapter relabeled captured current facts")
	}
	if after.WithBrowserProof("unverified").Browser() {
		t.Fatal("bearer admission promoted to cookie proof")
	}
}

func TestCurrentWarmAdmissionUsesNoPeerJournalOrTimeRefresh(t *testing.T) {
	_, origins, _ := authorityTestComposite(t)
	o := &CurrentAuthority{origin: origins[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	n := o.origin.network
	output, err := o.ConfigurePublicOutput(&http.Server{}, CurrentOutputLimits{ReadBytes: 1 << 20, SendBytes: 1 << 20, ConcurrentStreams: 16})
	if err != nil {
		t.Fatal(err)
	}
	writer, _, _ := currentPublicTestWriter(t, o, output)
	before, err := o.ExportFloors()
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Uint64
	prior := n.client
	n.client = &http.Client{Transport: currentTestRoundTripper{func(r *http.Request) (*http.Response, error) { sends.Add(1); return prior.Transport.RoundTrip(r) }, prior.CloseIdleConnections}}
	n.timeOwner.producer.mu.Lock()
	sequence := n.timeOwner.producer.sequence
	n.timeOwner.producer.mu.Unlock()
	for range 1000 {
		a, err := o.Admit(ctx, authorityFakeCredentialProducer{})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Check(ctx, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("bounded warm unit")); err != nil {
			t.Fatal(err)
		}
	}
	if writer.frame != 1000 {
		t.Fatal("warm unit bypassed final events", writer.frame)
	}
	after, err := o.ExportFloors()
	if err != nil {
		t.Fatal(err)
	}
	n.timeOwner.producer.mu.Lock()
	final := n.timeOwner.producer.sequence
	n.timeOwner.producer.mu.Unlock()
	if sends.Load() != 0 || sequence != final || !bytes.Equal(before, after) {
		t.Fatal("warm request introduced peer/time I/O or authority journal mutation", sends.Load(), sequence, final)
	}
}

func TestCurrentPublicRefreshWaitsForChallengeButNotTimeUncertainty(t *testing.T) {
	_, owners, ticks := authorityTestComposite(t)
	o := &CurrentAuthority{origin: owners[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	var original CurrentCredentialFacts
	producer := authorityPublicProducerFunc(func(ctx context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
		if original.Token == nil {
			var err error
			original, err = (authorityFakeCredentialProducer{}).VerifyCurrentCredential(ctx, view)
			if err != nil {
				return original, err
			}
		}
		return original, nil
	})
	ctx, before, err := o.WithRequestCredential(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	for _, tick := range ticks {
		tick.Add(uint64(20 * time.Second))
	}
	var calls atomic.Uint64
	prior := o.origin.network.client
	o.origin.network.client = &http.Client{Transport: currentTestRoundTripper{func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) <= 2 {
			return nil, ErrAuthorityUnavailable
		}
		return prior.Transport.RoundTrip(r)
	}, prior.CloseIdleConnections}}
	freshCtx, fresh, err := o.RefreshRequest(ctx)
	if err != nil || fresh == nil || fresh.Check(freshCtx, time.Time{}) != nil || calls.Load() <= 2 {
		t.Fatal("fresh challenge did not recover after transient peer refusal", err, calls.Load())
	}
	// The old admission is immutable; the fresh request carries the same
	// original trusted producer, not a reissued token or a new operation.
	if before.ScopeBinding() != fresh.ScopeBinding() {
		t.Fatal("renewal changed credential/cut binding")
	}
	clock := o.origin.network.timeOwner
	clock.mu.Lock()
	clock.anchor = nil
	clock.mu.Unlock()
	beforeCalls := calls.Load()
	if _, _, err := o.RefreshRequest(ctx); err == nil || calls.Load() != beforeCalls {
		t.Fatal("uncertain time retried peer renewal or granted admission")
	}
}

type currentTestRoundTripper struct {
	roundTrip func(*http.Request) (*http.Response, error)
	closeIdle func()
}

func (r currentTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return r.roundTrip(request)
}
func (r currentTestRoundTripper) CloseIdleConnections() { r.closeIdle() }
