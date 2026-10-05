package security

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManagementAuthorizationExactPurposeAndSignedEvent(t *testing.T) {
	store, _, _ := testStore(t, true)
	if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current()
	issuer := testImage().Issuers[0]
	issuer.URL = "https://another.example"
	issuer.ConfigRevision = 0
	now := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
	request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: now, Changes: []Change{{Kind: PutIssuer, Issuer: &issuer}}}
	prepared, err := store.PrepareManagement(t.Context(), request)
	if err != nil || !prepared.AuthorizationRequired {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name           string
		actor          Identity
		authTime       time.Time
		issuerRevision uint64
	}{
		{"missing", testIdentity(), time.Time{}, 1},
		{"old event", testIdentity(), now, 1},
		{"future event", testIdentity(), now.Add(3 * time.Second), 1},
		{"other subject", Identity{Kind: OIDCPrincipal, Issuer: testIdentity().Issuer, Subject: "other"}, now.Add(time.Second), 1},
		{"other issuer config", testIdentity(), now.Add(time.Second), 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManagementAuthorizations(time.Minute, 2)
			start, err := manager.Begin(prepared.Binding, now)
			if err != nil {
				t.Fatal(err)
			}
			id, binding, err := manager.Start(start.Ticket, current, now.Add(time.Second))
			if err != nil || id != start.ID || binding != prepared.Binding {
				t.Fatal("purpose ticket mismatch", err)
			}
			if _, _, err := manager.Start(start.Ticket, current, now); !errors.Is(err, ErrOperationAuthorization) {
				t.Fatal("navigation ticket reused", err)
			}
			if err := manager.Complete(id, test.actor, test.authTime, now.Add(time.Hour), test.issuerRevision, current, now.Add(2*time.Second)); !errors.Is(err, ErrOperationAuthorization) {
				t.Fatal("unqualified authentication approved", err)
			}
			status, err := manager.Status(id, testIdentity(), current, now.Add(2*time.Second))
			if err != nil || status.State != AuthorizationDenied || status.Proof != [32]byte{} {
				t.Fatal("failure minted proof", status, err)
			}
		})
	}
	manager := NewManagementAuthorizations(time.Minute, 2)
	start, err := manager.Begin(prepared.Binding, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Complete(start.ID, testIdentity(), now.Add(time.Second), now.Add(time.Hour), 1, current, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(start.ID, testIdentity(), current, now.Add(2*time.Second))
	if err != nil || status.State != AuthorizationApproved || status.Proof == [32]byte{} {
		t.Fatal(status, err)
	}
	for _, altered := range []ManagementBinding{
		func() ManagementBinding { binding := prepared.Binding; binding.ChangeID[0]++; return binding }(),
		func() ManagementBinding { binding := prepared.Binding; binding.IntentDigest[0]++; return binding }(),
		func() ManagementBinding { binding := prepared.Binding; binding.ExpectedRevision++; return binding }(),
		func() ManagementBinding { binding := prepared.Binding; binding.ExpectedDigest[0]++; return binding }(),
		func() ManagementBinding { binding := prepared.Binding; binding.Generation[0]++; return binding }(),
		func() ManagementBinding { binding := prepared.Binding; binding.Writer[0]++; return binding }(),
	} {
		if err := manager.Verify(status.Proof[:], altered, now.Add(2*time.Second)); !errors.Is(err, ErrOperationAuthorization) {
			t.Fatal("proof accepted another purpose", err)
		}
	}
	if err := manager.Verify(status.Proof[:], prepared.Binding, now.Add(time.Minute)); !errors.Is(err, ErrOperationAuthorization) {
		t.Fatal("expired approval accepted", err)
	}
	if err := NewManagementAuthorizations(time.Minute, 2).Verify(status.Proof[:], prepared.Binding, now.Add(2*time.Second)); !errors.Is(err, ErrOperationAuthorization) {
		t.Fatal("restart preserved outstanding proof", err)
	}
}

func TestManagementAuthorizationNumericDateBoundary(t *testing.T) {
	store, _, _ := testStore(t, true)
	if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current()
	issuer := testImage().Issuers[0]
	issuer.URL, issuer.ConfigRevision = "https://another.example", 0
	for _, fractional := range []time.Duration{0, 800 * time.Millisecond} {
		now := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC).Add(fractional)
		request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: now, Changes: []Change{{Kind: PutIssuer, Issuer: &issuer}}}
		prepared, _ := store.PrepareManagement(t.Context(), request)
		manager := NewManagementAuthorizations(time.Minute, 4)
		start, err := manager.Begin(prepared.Binding, now)
		if err != nil {
			t.Fatal(err)
		}
		if !start.NotBefore.After(now) || start.NotBefore.Nanosecond() != 0 {
			t.Fatal("ambiguous NumericDate boundary", start)
		}
		if _, _, err := manager.Start(start.Ticket, current, now); !errors.Is(err, ErrOperationAuthorization) {
			t.Fatal("early navigation consumed ticket", err)
		}
		if _, err := manager.NavigationNotBefore(start.Ticket, current, now); err != nil {
			t.Fatal("early ticket lost", err)
		}
		if _, _, err := manager.Start(start.Ticket, current, start.NotBefore); err != nil {
			t.Fatal("next-second ticket refused", err)
		}
		if err := manager.Complete(start.ID, testIdentity(), now.Truncate(time.Second), now.Add(time.Hour), 1, current, start.NotBefore); !errors.Is(err, ErrOperationAuthorization) {
			t.Fatal("pre-review same-second event accepted", err)
		}
		fresh, _ := manager.Begin(prepared.Binding, now)
		if err := manager.Complete(fresh.ID, testIdentity(), fresh.NotBefore, now.Add(time.Hour), 1, current, fresh.NotBefore); err != nil {
			t.Fatal("fresh whole-second event refused", err)
		}
	}
}

func TestManagementAuthorizationFinalConsumeTimeAndFailedCommit(t *testing.T) {
	for _, boundary := range []string{"proof expiry", "credential expiry", "clock rollback", "indeterminate commit"} {
		t.Run(boundary, func(t *testing.T) {
			store, sink, _ := testStore(t, true)
			if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
			issuer := testImage().Issuers[0]
			issuer.URL, issuer.ConfigRevision = "https://another.example", 0
			request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: now, Changes: []Change{{Kind: PutIssuer, Issuer: &issuer}}}
			prepared, _ := store.PrepareManagement(t.Context(), request)
			current, _ := store.Current()
			manager := NewManagementAuthorizations(time.Minute, 4)
			start, _ := manager.Begin(prepared.Binding, now)
			if err := manager.Complete(start.ID, request.Actor, now.Add(time.Second), now.Add(time.Hour), 1, current, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			status, _ := manager.Status(start.ID, request.Actor, current, now.Add(2*time.Second))
			request.Authorize = func(binding ManagementBinding, at time.Time) error {
				return manager.Verify(status.Proof[:], binding, at)
			}
			clockCalls := 0
			request.Clock = func() time.Time {
				clockCalls++
				if clockCalls == 1 {
					return now.Add(2 * time.Second)
				}
				switch boundary {
				case "proof expiry":
					return now.Add(time.Minute)
				case "credential expiry":
					return now.Add(4 * time.Second)
				case "clock rollback":
					return now.Add(time.Second)
				default:
					return now.Add(2 * time.Second)
				}
			}
			want := ErrOperationAuthorization
			if boundary == "credential expiry" {
				admission, _ := NewAdmission(request.Actor, time.Time{}, now.Add(3*time.Second), current, func(context.Context, *Revision) error { return nil })
				request.Admission = admission.WithAuthentication(request.Authentication)
				want = ErrAuthorityUnavailable
			}
			if boundary == "clock rollback" {
				want = ErrAuthorityUnavailable
			}
			if boundary == "indeterminate commit" {
				sink.err = errors.New("indeterminate persistence")
				want = ErrStoreUnavailable
			}
			if _, err := store.Manage(t.Context(), request); !errors.Is(err, want) {
				t.Fatal("consume boundary accepted", err, want)
			}
			if boundary == "indeterminate commit" {
				if _, healthy := store.Current(); healthy {
					t.Fatal("failed commit left serving healthy")
				}
				if _, err := store.Manage(t.Context(), request); !errors.Is(err, ErrStoreUnavailable) || sink.calls != 2 {
					t.Fatal("proof retried poisoned writer", err)
				}
			} else if sink.calls != 1 {
				t.Fatal("expired/rollback admission reached persistence", sink.calls)
			}
		})
	}
}

func TestManagementAuthorizationOneDurableEffectAndRestartRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.wal")
	native, err := CreateNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Store().ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	issuer := testImage().Issuers[0]
	issuer.URL = "https://another.example"
	issuer.ConfigRevision = 0
	request := ManagementRequest{ExpectedRevision: 1, ChangeID: [16]byte{2}, Actor: testIdentity(), Authentication: Authentication{Provenance: BrowserCode, Class: EndUser, IssuerConfigRevision: 1}, Now: now, Changes: []Change{{Kind: PutIssuer, Issuer: &issuer}}}
	prepared, err := native.Store().PrepareManagement(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManagementAuthorizations(time.Minute, 2)
	start, _ := manager.Begin(prepared.Binding, now)
	current, _ := native.Store().Current()
	if err := manager.Complete(start.ID, testIdentity(), now.Add(time.Second), now.Add(time.Hour), 1, current, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, _ := manager.Status(start.ID, testIdentity(), current, now.Add(2*time.Second))
	request.Now = now.Add(2 * time.Second)
	request.Authorize = func(binding ManagementBinding, at time.Time) error {
		return manager.Verify(status.Proof[:], binding, at)
	}
	var wait sync.WaitGroup
	results := make(chan ChangeResult, 8)
	for range 8 {
		wait.Go(func() {
			result, err := native.Store().Manage(t.Context(), request)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		})
	}
	wait.Wait()
	close(results)
	commits := 0
	var original ChangeResult
	for result := range results {
		if !result.Replayed {
			commits++
			original = result
		}
	}
	if commits != 1 {
		t.Fatal("approval had multiple durable effects", commits)
	}
	current, _ = native.Store().Current()
	if current.Sequence() != 2 || len(current.Snapshot().Image().Audit) != 1 {
		t.Fatal("serialized commit or audit duplicated")
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := ResumeNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	request.Authorize = nil
	request.AuthTime = time.Time{}
	replay, err := reopened.Store().Manage(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Digest != original.Digest {
		t.Fatal("retained original required proof after restart", replay, err)
	}
	request.ChangeID[0]++
	request.ExpectedRevision = 2
	if _, err := reopened.Store().Manage(t.Context(), request); !errors.Is(err, ErrOperationAuthorization) {
		t.Fatal("old approval allowed a new effect", err)
	}
}
