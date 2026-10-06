package security

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestNativeCheckpointLegacyZeroConfigSessionsPreserveEvidence(t *testing.T) {
	// Literal pre-qualification image bytes with matching zero Issuer/Session
	// configuration revisions. Reading history must preserve unknown and old
	// signed authentication evidence without enrolling this legacy Principal.
	const encoded = `{"version":2,"bootstrap_revision":1,"issuers":[{"config_revision":0,"url":"https://idp.example/realm","enabled":true,"client_id":"admin-client","api_audience":"lantern","redirect_uri":"https://admin.example/auth/callback","algorithms":["RS256"]}],"roles":[{"id":"security_admin","name":"","rules":[{"effect":"allow","action":"security.manage","resource":"global"}]}],"principals":[{"identity":{"kind":"oidc","issuer":"https://idp.example/realm","subject":"admin"},"state":"active","assignments":[{"role_id":"security_admin","env_owned":true}]}],"sessions":[{"issuer_config_revision":0,"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","identity":{"kind":"oidc","issuer":"https://idp.example/realm","subject":"admin"},"created_at":"2026-10-03T01:00:00Z","expires_at":"2026-10-03T02:00:00Z","auth_time":"0001-01-01T00:00:00Z","revoked":false},{"issuer_config_revision":0,"digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","identity":{"kind":"oidc","issuer":"https://idp.example/realm","subject":"admin"},"created_at":"2026-10-03T01:00:00Z","expires_at":"2026-10-03T02:00:00Z","auth_time":"2026-10-03T00:00:00Z","revoked":false}]}`
	legacy, err := DecodeImage([]byte(encoded), DefaultPolicyLimits())
	if err != nil || !bytes.Equal(legacy.image, []byte(encoded)) {
		t.Fatal("legacy zero-configuration image changed", err)
	}
	check := func(snapshot *Snapshot) {
		t.Helper()
		if !bytes.Equal(snapshot.image, []byte(encoded)) || snapshot.HumanIdentity(testIdentity()) {
			t.Fatal("legacy bytes changed or historical read enrolled a human")
		}
		now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
		for _, session := range legacy.Image().Sessions {
			_, authTime, active := snapshot.SessionAccess(session.Digest, now)
			if !active || !authTime.Equal(session.AuthTime) {
				t.Fatal("matching legacy configuration lost exact authentication evidence")
			}
		}
	}
	check(legacy)
	options := nativeTestOptions(t, filepath.Join(t.TempDir(), "legacy-sessions.wal"))
	native, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := SignRevision(options.Generation, 1, [32]byte{}, [16]byte{1}, legacy, options.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Store().Apply(t.Context(), revision.Encode()); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := ResumeNativeStore(nativeTestOptions(t, options.Path))
	if err != nil {
		t.Fatal("legacy signed WAL replay", err)
	}
	defer reopened.Close()
	current, _ := reopened.Store().Current()
	check(current.Snapshot())
	_, _, replicaOptions := testStore(t, false)
	replicaOptions.Generation, replicaOptions.PublicKey = options.Generation, options.PublicKey
	replica, err := NewStore(replicaOptions)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRevision(current.Encode(), options.PublicKey, options.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.restoreCheckpoint(decoded); err != nil {
		t.Fatal("legacy signed checkpoint restore", err)
	}
	installed, _ := replica.Current()
	check(installed.Snapshot())
}

func TestNativeCheckpointFreshReplicaAndRestart(t *testing.T) {
	writer, clock := newAuthorityTestStore(t)
	for expected := uint64(1); expected < 12; expected++ {
		image := testImage()
		unknown := testSession()
		unknown.CreatedAt = clock.Now()
		unknown.ExpiresAt = clock.Now().Add(time.Hour)
		unknown.AuthTime = time.Time{}
		old := unknown
		old.Digest = strings.Repeat("c", 64)
		old.AuthTime = clock.Now().Add(-time.Hour)
		image.Sessions = []Session{unknown, old}
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "reader"}, State: Suspended})
		if _, err := writer.Commit(t.Context(), expected, [16]byte{byte(expected + 1)}, image); err != nil {
			t.Fatal(err)
		}
	}
	options := nativeTestOptions(t, filepath.Join(t.TempDir(), "system.wal"))
	options.PrivateKey = nil
	replica, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	authority, _ := NewLeaseAuthority(writer, LeaseAuthorityOptions{Clock: clock})
	receiver, _ := NewLeaseReceiver(replica.Store(), [16]byte{5}, options.PublicKey, clock, 2*time.Second)
	clock.advance(35 * time.Second)
	request, _ := receiver.Challenge()
	lease, image, err := authority.Checkpoint(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = replica.InstallCheckpoint(t.Context(), image, nil); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("historical signature granted serving", err)
	}
	proof, err := receiver.ProveCheckpoint(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = replica.InstallCheckpoint(t.Context(), image, proof); err != nil {
		t.Fatal(err)
	}
	current, _ := replica.Store().Current()
	for _, session := range current.Snapshot().Image().Sessions {
		_, authTime, active := current.Snapshot().SessionAccess(session.Digest, clock.Now())
		if !active || !authTime.Equal(session.AuthTime) {
			t.Fatal("checkpoint changed authentication evidence")
		}
	}
	if current.Sequence() != 12 || len(replica.Store().changes) != 12 {
		t.Fatal("incomplete cut or retry history")
	}
	// The checkpoint retains exact historical commit cuts, not just current
	// policy. Item outcomes are absent from the unchanged signed history.
	retained, err := writer.ChangeStatus([16]byte{2})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := replica.Store().ChangeStatus([16]byte{2})
	if err != nil || installed != retained || installed.Digest == current.Digest() {
		t.Fatal("checkpoint lost original commit proof", installed, err)
	}
	if err = receiver.Check(t.Context(), current); err == nil {
		t.Fatal("installation itself granted serving")
	}
	if err = receiver.ActivateCheckpoint(proof); err != nil {
		t.Fatal(err)
	}
	if err = receiver.Check(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if err = replica.Close(); err != nil {
		t.Fatal(err)
	}
	options.Graph = graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	recovered, err := ResumeNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recovered.Close() }()
	restored, known := recovered.Store().Current()
	if !known || restored.Digest() != current.Digest() {
		t.Fatal("checkpoint did not recover", err)
	}
	restarted, err := recovered.Store().ChangeStatus([16]byte{2})
	if err != nil || restarted != retained {
		t.Fatal("restart lost checkpoint commit proof", restarted, err)
	}
	for _, session := range current.Snapshot().Image().Sessions {
		_, authTime, active := restored.Snapshot().SessionAccess(session.Digest, clock.Now())
		if !active || !authTime.Equal(session.AuthTime) {
			t.Fatal("restart changed authentication evidence")
		}
	}
	freshReceiver, _ := NewLeaseReceiver(recovered.Store(), [16]byte{5}, options.PublicKey, clock, 2*time.Second)
	if err = freshReceiver.Check(context.Background(), restored); err == nil {
		t.Fatal("restart reused old serving lease")
	}
}
func TestNativeCheckpointRefusesExpiredSupersededAndWrongStore(t *testing.T) {
	writer, clock := newAuthorityTestStore(t)
	authority, _ := NewLeaseAuthority(writer, LeaseAuthorityOptions{Clock: clock})
	options := nativeTestOptions(t, filepath.Join(t.TempDir(), "replica.wal"))
	options.PrivateKey = nil
	replica, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	receiver, _ := NewLeaseReceiver(replica.Store(), [16]byte{1}, options.PublicKey, clock, 2*time.Second)
	clock.advance(35 * time.Second)
	request, _ := receiver.Challenge()
	lease, image, _ := authority.Checkpoint(t.Context(), request)
	proof, err := receiver.ProveCheckpoint(lease)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = receiver.Challenge()
	if err = replica.InstallCheckpoint(t.Context(), image, proof); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("superseded proof installed", err)
	}
	request, _ = receiver.Challenge()
	lease, image, _ = authority.Checkpoint(t.Context(), request)
	proof, _ = receiver.ProveCheckpoint(lease)
	clock.advance(29 * time.Second)
	if err = replica.InstallCheckpoint(t.Context(), image, proof); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("expired proof installed", err)
	}
	if replica.Store().faulted.Load() {
		t.Fatal("preflight refusal poisoned healthy empty store")
	}
}
