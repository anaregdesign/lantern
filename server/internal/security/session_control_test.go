package security

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSessionControlIssuanceRotationAndCurrentIssuer(t *testing.T) {
	store, sink, _ := testStore(t, true)
	image := testImage()
	image.Issuers[0].ConfigRevision = 1
	if _, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, image); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := SessionRequest{ChangeID: [16]byte{2}, Identity: testIdentity(), IssuerConfigRevision: 1, Digest: strings.Repeat("a", 64), CSRFDigest: strings.Repeat("b", 64), AuthTime: now, Now: now, Lifetime: time.Hour}
	result, err := store.IssueSession(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current()
	if _, _, active := current.Snapshot().SessionAccess(request.Digest, now); !active {
		t.Fatal("issued session is unusable")
	}
	replay, err := store.IssueSession(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Digest != result.Digest || sink.calls != 2 {
		t.Fatal("session retry created another session", replay, err)
	}
	next := request
	next.ChangeID = [16]byte{3}
	next.Digest = strings.Repeat("c", 64)
	next.CSRFDigest = strings.Repeat("d", 64)
	next.ReplacesDigest = request.Digest
	next.Now = now.Add(time.Second)
	if _, err := store.IssueSession(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Current()
	if _, _, active := current.Snapshot().SessionAccess(request.Digest, next.Now); active {
		t.Fatal("rotation retained old cookie")
	}
	if _, _, active := current.Snapshot().SessionAccess(next.Digest, next.Now); !active {
		t.Fatal("rotation lost new cookie")
	}
	if _, err := store.RevokeSession(t.Context(), testIdentity(), next.Digest, [16]byte{4}, next.Now); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Current()
	if _, _, active := current.Snapshot().SessionAccess(next.Digest, next.Now); active {
		t.Fatal("logout did not revoke cookie")
	}
	changed := request
	changed.ChangeID = [16]byte{5}
	changed.IssuerConfigRevision = 2
	changed.Digest = strings.Repeat("e", 64)
	if _, err := store.IssueSession(t.Context(), changed); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("callback ignored Issuer revision race", err)
	}
	unknown := request
	unknown.ChangeID = [16]byte{6}
	unknown.Identity.Subject = "unregistered"
	if _, err := store.IssueSession(t.Context(), unknown); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("login automatically enrolled unknown subject", err)
	}
}
