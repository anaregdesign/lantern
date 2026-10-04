package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/readiness"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestSecurityWorkersDrivePermissionAndJoinOnShutdown(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	gate := readiness.NewGate(100, false, nil)
	worker := NewSecurityWorkers(runtime, nil, nil, gate, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	wait := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if gate.Ready() == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("readiness not synchronized", want)
	}
	wait(false)
	clock.advance(35 * time.Second)
	wait(true)
	clock.advance(-time.Second)
	wait(false)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("policy workers did not join")
	}
}

func TestSecurityWorkersLogOnlyFixedPrivateFaultCategory(t *testing.T) {
	clock := &securityTestClock{now: time.Now()}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: [32]byte{2}}, IssuedAt: clock.Now(), ExpiresAt: clock.Now().Add(time.Minute), Members: []peerauth.Member{{ID: [16]byte{3}, Identity: "spiffe://private.test/member", SPKI: [32]byte{4}, Origin: "https://private.example:6391"}}}
	raw, err := peerauth.SignManifest(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := peerauth.CreateStore(peerauth.StoreOptions{Path: filepath.Join(t.TempDir(), "private-state"), Key: public, Domain: manifest.Domain, Now: clock.Now}, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	peer := &PeerIdentityRuntime{store: store, config: PeerIdentityConfig{Now: clock.Now, SelfIdentity: manifest.Members[0].Identity, ManifestFile: filepath.Join(t.TempDir(), "missing-input")}, selfExpiry: clock.Now().Add(time.Hour)}
	clock.advance(-time.Second)
	if peer.CheckWorkload(t.Context()) == nil {
		t.Fatal("rollback did not fence workload")
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	worker := NewSecurityWorkers(&SecurityRuntime{mode: "off", peer: peer}, peer, nil, readiness.NewGate(100, false, nil), slog.New(slog.NewJSONHandler(writer, nil)))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	message := make(chan map[string]any, 1)
	go func() { var value map[string]any; _ = json.NewDecoder(reader).Decode(&value); message <- value }()
	select {
	case value := <-message:
		if value["reason"] != "clock_rollback" || value["msg"] != "peer membership reload rejected" || len(value) != 4 {
			t.Fatal("private diagnostic exposed context or lost fixed category", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("membership fault was not diagnosed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("diagnostic worker did not stop")
	}
}
