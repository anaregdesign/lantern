package service

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

func TestStagedPublicationReleaseAndPanicBoundaries(t *testing.T) {
	for _, phase := range []string{"before WAL", "during release", "after publication", "success"} {
		t.Run(phase, func(t *testing.T) {
			walWritten := false
			f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
				walWritten = true
				return nil
			}))
			f.cache.AddEdge("tail", "head", 3)
			call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
			keys, intents, err := prepareEdgeDeleteReceiptCall(call, f.service.namespaceFormat)
			if err != nil {
				t.Fatal(err)
			}
			panicValue := errors.New("publication boundary interrupted")
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				s := f.service
				s.replicationCutMu.Lock()
				defer s.replicationCutMu.Unlock()
				s.receiptOriginCutMu.Lock()
				defer s.receiptOriginCutMu.Unlock()
				publication := stagedPublication{service: s}
				defer publication.failClosedOnPanic()
				store, err := f.coordinator.store.Begin(time.Now())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Abort()
				if _, _, err := store.Classify(intents); err != nil {
					t.Fatal(err)
				}
				if err := store.Reserve([][]byte{{1}}); err != nil {
					t.Fatal(err)
				}
				ts := s.clock.Now()
				graph, err := f.cache.BeginEdgeDelete(keys, ts, time.Now().Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				defer graph.Abort()
				origin, ok := s.origins.stageNext(ts.NodeID, 1, ts)
				if !ok {
					t.Fatal("origin stage rejected")
				}
				defer origin.Abort()
				if err := store.Stage(); err != nil {
					t.Fatal(err)
				}
				if phase == "before WAL" {
					panic(panicValue)
				}
				err = publication.commit("staged publication proof", ts, store, func() {
					if !walWritten {
						t.Error("graph release preceded WAL success")
					}
					// A completed raw Store read proves its release precedes the
					// graph callback. The origin gate must still be held here.
					read := make(chan mutationreceipt.Status, 1)
					go func() {
						status, _, _ := f.coordinator.store.Lookup(call.Items[0].ID, time.Now())
						read <- status
					}()
					select {
					case status := <-read:
						if status != mutationreceipt.Confirmed {
							t.Errorf("post-ring Store status = %v", status)
						}
					case <-time.After(time.Second):
						panic("Store remained locked during graph release")
					}
					if s.origins.mu.TryLock() {
						s.origins.mu.Unlock()
						t.Error("origin released before graph")
					}
					if s.replicationCutMu.TryLock() {
						s.replicationCutMu.Unlock()
						t.Error("outer publication gate released before graph")
					}
					if phase == "during release" {
						panic(panicValue)
					}
					graph.Commit()
				}, origin)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "after publication" {
					panic(panicValue)
				}
			}()
			if (phase == "success" && recovered != nil) || (phase != "success" && recovered != panicValue) {
				t.Fatalf("recovered = %v", recovered)
			}
			faulted := phase == "during release" || phase == "after publication"
			if f.service.receiptCommitFaulted != faulted {
				t.Fatalf("service faulted = %v, want %v", f.service.receiptCommitFaulted, faulted)
			}
			status, _, lookupErr := f.coordinator.Lookup(call.Items[0].ID, time.Now())
			if faulted {
				if connect.CodeOf(lookupErr) != connect.CodeFailedPrecondition {
					t.Fatalf("partial publication exposed receipt status %v, %v", status, lookupErr)
				}
			} else if lookupErr != nil || (phase == "success" && status != mutationreceipt.Confirmed) ||
				(phase == "before WAL" && status != mutationreceipt.NotYetObserved) {
				t.Fatalf("receipt lookup = %v, %v", status, lookupErr)
			}
			_, live := f.cache.GetWeight("tail", "head")
			rolledBack := phase == "before WAL" || phase == "during release"
			if live != rolledBack || (f.service.origins.LocalSeq(f.service.clock.NodeID()) == 0) != rolledBack {
				t.Fatal("graph/origin resolution differs from publication boundary")
			}
			if phase == "during release" {
				if f.log.Len() != 1 {
					t.Fatal("release panic did not follow ring installation")
				}
				_, err := f.log.CommitWithPostRingPublication("must reject", f.service.clock.Now(), nil)
				if !errors.Is(err, mutationlog.ErrPublicationInterrupted) {
					t.Fatalf("interrupted log accepted later publication: %v", err)
				}
			}
		})
	}
}
