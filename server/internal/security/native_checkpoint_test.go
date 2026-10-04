package security

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

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
