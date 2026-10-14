package authn

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

type registrationProofTestProducer struct{}

func (registrationProofTestProducer) VerifyCurrentCredential(context.Context, security.CurrentCredentialView) (security.CurrentCredentialFacts, error) {
	return security.CurrentCredentialFacts{}, ErrUnauthenticated
}

func TestNativeRegistrationProofBoundsExpiryAndOwnership(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	removed := 0
	p := newNativeRegistrationProofs(func() (time.Time, time.Time, error) { return now, now, nil }, func(*security.CurrentRegistration) { removed++ })
	defer p.close()
	// Opaque pointers are storage fixtures only. The producer explicitly cannot
	// authenticate; no security CurrentRegistration constructor is bypassed.
	entry := nativeRegistrationEntry{proof: &security.CurrentRegistration{}, producer: registrationProofTestProducer{}, binding: [32]byte{1}, expires: now.Add(time.Minute), bytes: 128}
	first, err := p.put(entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.get(first, [32]byte{2}, true); err == nil {
		t.Fatal("proof crossed original review")
	}
	if _, _, err := p.get("invalid", entry.binding, true); err == nil {
		t.Fatal("invalid handle accepted")
	}
	if _, duplicate, err := p.get(first, entry.binding, false); err != nil || duplicate {
		t.Fatal("read claimed Apply", err)
	}
	for range nativeRegistrationProofCount - 1 {
		if _, err := p.put(entry); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.put(entry); err == nil {
		t.Fatal("count bound exceeded")
	}
	now = entry.expires
	if _, _, err := p.get(first, entry.binding, false); err == nil || removed != nativeRegistrationProofCount || p.bytes != 0 {
		t.Fatal("expired references/capacity retained", err)
	}
	entry.expires = now.Add(time.Minute)
	entry.bytes = nativeRegistrationProofBytes
	if _, err := p.put(entry); err != nil {
		t.Fatal(err)
	}
	entry.bytes = 1
	if _, err := p.put(entry); err == nil {
		t.Fatal("byte bound exceeded")
	}
}

func TestNativeRegistrationProofClaimsApplyOnce(t *testing.T) {
	now := time.Now().UTC()
	p := newNativeRegistrationProofs(func() (time.Time, time.Time, error) { return now, now, nil }, func(*security.CurrentRegistration) {})
	defer p.close()
	entry := nativeRegistrationEntry{proof: &security.CurrentRegistration{}, producer: registrationProofTestProducer{}, binding: [32]byte{1}, expires: now.Add(time.Minute), bytes: 128}
	handle, err := p.put(entry)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var first atomic.Int32
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, duplicate, err := p.get(handle, entry.binding, true)
			if err != nil {
				t.Error(err)
			} else if !duplicate {
				first.Add(1)
			}
		}()
	}
	group.Wait()
	if first.Load() != 1 {
		t.Fatal("multiple Apply owners", first.Load())
	}
}

func TestNativeRegistrationProofCloseCancelsAndJoins(t *testing.T) {
	now := time.Now().UTC()
	removed := 0
	p := newNativeRegistrationProofs(func() (time.Time, time.Time, error) { return now, now, nil }, func(*security.CurrentRegistration) { removed++ })
	_, err := p.put(nativeRegistrationEntry{proof: &security.CurrentRegistration{}, producer: registrationProofTestProducer{}, binding: [32]byte{1}, expires: now.Add(time.Minute), bytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	ctx, done, err := p.enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { p.close(); close(closed) }()
	<-ctx.Done()
	select {
	case <-closed:
		t.Fatal("Close did not join active call")
	default:
	}
	done()
	<-closed
	if len(p.entries) != 0 || p.bytes != 0 || removed != 1 {
		t.Fatal("Close retained proof references")
	}
	if _, _, err := p.enter(t.Context()); err == nil {
		t.Fatal("closed owner accepted call")
	}
	p.close()
	if removed != 1 {
		t.Fatal("close discarded twice")
	}
}
