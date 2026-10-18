package provider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/readiness"
)

type pausedReadinessPublication struct {
	entered, release chan struct{}
	once             sync.Once
}

func (p *pausedReadinessPublication) SetServingStatus(_ string, status grpchealth.Status) {
	if status == grpchealth.StatusNotServing {
		p.once.Do(func() { close(p.entered); <-p.release })
	}
}

func TestSecurityWorkerObservationSeparatesCompletedAndPublished(t *testing.T) {
	r := &SecurityRuntime{mode: "oidc"}
	publication := &pausedReadinessPublication{entered: make(chan struct{}), release: make(chan struct{})}
	gate := readiness.NewGate(100, false, publication)
	worker := NewSecurityWorkers(r, nil, gate, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var output bytes.Buffer
	worker.ObserveReadiness(&output)
	if s := r.readinessObservation.Snapshot(); s.WorkerState != "never_started" {
		t.Fatal(s)
	}
	done := make(chan struct{})
	go func() { worker.updateReadiness(t.Context()); close(done) }()
	select {
	case <-publication.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach publication")
	}
	s := r.readinessObservation.Snapshot()
	close(publication.release)
	<-done
	if s.WorkerStarted != 1 || s.WorkerDone != 1 || s.WorkerPublish != 0 || s.WorkerState != "completed" || s.WorkerReason != "unavailable" {
		t.Fatal(s)
	}
	if s := r.readinessObservation.Snapshot(); s.WorkerPublish != 1 || s.WorkerState != "published" {
		t.Fatal(s)
	}
	worker.runtime = &SecurityRuntime{mode: "off", readinessObservation: r.readinessObservation}
	worker.updateReadiness(t.Context())
	if s := r.readinessObservation.Snapshot(); s.WorkerPublish != 2 || s.WorkerReason != "ready" || !gate.Ready() {
		t.Fatal(s)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// Stopping is a separate observation. It must retain the last completed
	// OFF evaluation rather than inventing a new authority failure reason.
	if s := r.readinessObservation.Snapshot(); s.WorkerState != "stopped_canceled" || s.WorkerReason != "ready" || s.WorkerDone < 3 || s.WorkerStarted != s.WorkerDone || s.WorkerPublish != s.WorkerDone {
		t.Fatal(s)
	}
}

func TestSecurityWorkerObservationShowsBlockedExistingEvaluation(t *testing.T) {
	clock := &securityTestClock{now: time.Now()}
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var enter, unblock sync.Once
	peer := securityWorkloadFixture(t, func() time.Time {
		if block.Load() {
			enter.Do(func() { close(entered) })
			<-release
		}
		return clock.Now()
	})
	r := &SecurityRuntime{mode: "off", peer: peer}
	defer unblock.Do(func() { close(release) })
	worker := NewSecurityWorkers(r, nil, readiness.NewGate(100, false, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.ObserveReadiness(io.Discard)
	block.Store(true)
	done := make(chan struct{})
	go func() { worker.updateReadiness(t.Context()); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("existing evaluation did not start")
	}
	hc := NewHealthChecker()
	hc.SetServingStatus("", grpchealth.StatusNotServing)
	healthDone := make(chan struct{})
	go func() {
		_, _ = (publicHealthChecker{hc.Inner(), r}).Check(t.Context(), &grpchealth.CheckRequest{})
		close(healthDone)
	}()
	select {
	case <-healthDone:
	case <-time.After(time.Second):
		t.Fatal("diagnostics reevaluated blocked authority")
	}
	s := r.readinessObservation.Snapshot()
	if s.WorkerState != "in_progress" || s.WorkerStarted != 1 || s.WorkerDone != 0 || s.WorkerPublish != 0 || s.WorkerReason != "not_evaluated" || s.HealthBranch != "static_rejection" || s.HealthReason != "not_evaluated" {
		t.Fatal(s)
	}
	block.Store(false)
	unblock.Do(func() { close(release) })
	<-done
}

func TestSecurityWorkersDrivePermissionAndJoinOnShutdown(t *testing.T) {
	clock := &securityTestClock{now: time.Now()}
	peer := securityWorkloadFixture(t, clock.Now)
	runtime := &SecurityRuntime{mode: "off", peer: peer}
	gate := readiness.NewGate(100, false, nil)
	worker := NewSecurityWorkers(runtime, peer, gate, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	wait(true)
	clock.advance(2 * time.Minute)
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

func TestSecurityWorkersReloadDataDespiteControlFailureAndExpiredReadiness(t *testing.T) {
	clock := &securityTestClock{now: time.Now()}
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	m := peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: [32]byte{2}}, IssuedAt: clock.Now(), ExpiresAt: clock.Now().Add(time.Minute), Members: []peerauth.Member{{ID: [16]byte{3}, Identity: "spiffe://worker.test/self", SPKI: [32]byte{4}, Origin: "https://127.0.0.1:17443"}}}
	raw, err := peerauth.SignManifest(m, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := peerauth.CreateStore(peerauth.StoreOptions{Path: filepath.Join(dir, "checkpoint"), Key: public, Domain: m.Domain, Now: clock.Now}, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	input := filepath.Join(dir, "input")
	peer := &PeerIdentityRuntime{store: store, config: PeerIdentityConfig{Now: clock.Now, SelfIdentity: m.Members[0].Identity, ManifestFile: input}, selfExpiry: clock.Now().Add(time.Hour)}
	clock.advance(2 * time.Minute)
	if peer.CheckWorkload(t.Context()) == nil {
		t.Fatal("old membership still live")
	}
	m.Version++
	m.IssuedAt = clock.Now()
	m.ExpiresAt = clock.Now().Add(time.Minute)
	raw, err = peerauth.SignManifest(m, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	gate := readiness.NewGate(100, false, nil)
	worker := NewSecurityWorkers(&SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}, peer: peer}, peer, gate, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for peer.CheckWorkload(t.Context()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("control failure blocked expired data reload")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gate.Ready() {
		t.Fatal("reload success manufactured readiness")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker failed to join")
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
	worker := NewSecurityWorkers(&SecurityRuntime{mode: "off", peer: peer}, peer, readiness.NewGate(100, false, nil), slog.New(slog.NewJSONHandler(writer, nil)))
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
