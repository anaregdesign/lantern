package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestPublicationGateFreshCertificatesCoverEveryExclusiveInterval(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	gate := &svc.replicationCutMu
	first := gate.readCertificate.Load()
	if !first.stable() || first.faulted {
		t.Fatal("constructor did not certify its initial healthy cut")
	}
	gate.RLock()
	if gate.TryLock() {
		gate.Unlock()
		t.Fatal("TryLock entered while a reader held the cut")
	}
	if gate.readCertificate.Load() != first || gate.generation != 0 {
		t.Fatal("failed TryLock changed publication proof")
	}
	gate.RUnlock()
	retained := []*publicationReadCertificate{first}
	for i := range 64 {
		if !gate.TryLock() {
			t.Fatal("uncontended TryLock failed")
		}
		if gate.readCertificate.Load() != activePublicationCertificate {
			t.Fatal("writer returned before publishing active")
		}
		gate.Unlock()
		certificate := gate.readCertificate.Load()
		if !certificate.stable() || certificate.faulted || gate.generation != uint64(i+1) {
			t.Fatal("empty exclusive interval was not committed")
		}
		for _, previous := range retained {
			if previous == certificate {
				t.Fatal("stable certificate identity was reused")
			}
		}
		retained = append(retained, certificate)
	}
}

func TestPublicationGateCommittedReadsCompleteBeforeMutexHandoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	metrics := &testPublicationObserver{}
	svc := NewLanternService(cache).WithHotPathMetrics(metrics)
	gate := &svc.replicationCutMu
	gate.Lock()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(gate.mu.Unlock) }
	defer release()
	if err := cache.PutVertex("committed", &pb.Vertex{Key: "committed"}); err != nil {
		t.Fatal(err)
	}
	// This is the production Unlock publisher, paused immediately before its
	// raw mutex handoff. Only this component test accesses the underlying mu.
	gate.completePublicationLocked()
	if gate.TryRLock() {
		gate.RUnlock()
		t.Fatal("test did not retain the raw exclusive mutex")
	}
	done := make(chan error, 16)
	for range 16 {
		go func() {
			_, err := svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "committed"})
			done <- err
		}()
	}
	for range 16 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("certified read queued behind completed-body handoff", ctx.Err())
		}
	}
	if len(metrics.gates) != 0 || len(metrics.reads) != 16 {
		t.Fatal("retry read registered a generation-probe/fallback acquisition")
	}
	release()
	gate.Lock()
	if gate.readCertificate.Load().stable() {
		gate.Unlock()
		t.Fatal("following writer did not invalidate completed proof")
	}
	gate.Unlock()
}

func TestPublicationGateFaultsRemainAuthoritativeAndUnboundIsUnavailable(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	gate := &svc.replicationCutMu
	pending := &pendingMutation{}
	gate.Lock()
	svc.markPublicationFault(pending)
	gate.Unlock()
	if !gate.readCertificate.Load().faulted {
		t.Fatal("graph publication fault was not certified")
	}
	gate.Lock()
	svc.markReceiptCommitFaultLocked()
	svc.clearPublicationFault(pending)
	gate.Unlock()
	if !gate.readCertificate.Load().faulted {
		t.Fatal("clearing one fault hid receipt indeterminacy")
	}
	unbound := &LanternService{}
	if unbound.replicationCutMu.readCertificate.Load() != nil {
		t.Fatal("zero-value gate invented a stable owner")
	}
	captures := 0
	if err := unbound.withPublicGraphReadRetry(t.Context(), func() error { captures++; return nil }); err != nil || captures != 1 {
		t.Fatalf("unbound protected fallback: captures=%d err=%v", captures, err)
	}
	unbound.replicationCutMu.Lock()
	unbound.publicationFaultCount = 1
	unbound.replicationCutMu.Unlock()
	if unbound.replicationCutMu.readCertificate.Load() != nil {
		t.Fatal("unbound Unlock invented healthy proof")
	}
	err := unbound.withPublicGraphReadRetry(t.Context(), func() error { t.Fatal("faulted fallback captured"); return nil })
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unbound authoritative fault = %v", err)
	}
}

func TestPublicationGateCheckedRejectionPreservesProofAndReleasesRawMutex(t *testing.T) {
	for _, faulted := range []bool{false, true} {
		name := "healthy"
		if faulted {
			name = "faulted"
		}
		t.Run(name, func(t *testing.T) {
			svc := NewLanternService(newFakeBackend())
			gate := &svc.replicationCutMu
			if faulted {
				gate.Lock()
				svc.markPublicationFault(&pendingMutation{})
				gate.Unlock()
			}
			certificate, generation := gate.readCertificate.Load(), gate.generation
			rejected := errors.New("original admission expired under publication cut")
			checks := 0
			rawOwned, proofUnchanged := false, false
			err := gate.LockChecked(func() error {
				checks++
				rawOwned = !gate.TryRLock()
				if !rawOwned {
					gate.RUnlock()
				}
				proofUnchanged = gate.readCertificate.Load() == certificate && gate.generation == generation
				return rejected
			})
			if !errors.Is(err, rejected) || checks != 1 || !rawOwned || !proofUnchanged {
				t.Fatalf("checked acquisition did not reject under the raw cut before changing proof: checks=%d rawOwned=%t proofUnchanged=%t err=%v", checks, rawOwned, proofUnchanged, err)
			}
			if gate.readCertificate.Load() != certificate || gate.generation != generation {
				t.Fatal("rejected acquisition changed certificate or numeric generation")
			}
			if !gate.TryRLock() {
				t.Fatal("rejected acquisition retained its raw mutex")
			}
			gate.RUnlock()
			if err := gate.LockChecked(func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if gate.readCertificate.Load() != activePublicationCertificate || gate.generation != generation {
				gate.Unlock()
				t.Fatal("accepted acquisition did not enter its exclusive interval")
			}
			gate.Unlock()
			if next := gate.readCertificate.Load(); next == certificate || !next.stable() || next.faulted != faulted || gate.generation != generation+1 {
				t.Fatal("accepted retry did not certify exactly one subsequent publication interval")
			}
		})
	}
}
