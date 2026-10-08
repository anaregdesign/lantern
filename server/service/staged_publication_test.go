package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestStagedPublicationRecoveryClassification(t *testing.T) {
	for _, uncertainty := range []error{mutationlog.ErrWALIndeterminate, mutationlog.ErrPublicationInterrupted} {
		for _, err := range []error{&mutationlog.DefiniteWALAbort{Cause: uncertainty},
			fmt.Errorf("wrapped abort: %w", &mutationlog.DefiniteWALAbort{Cause: uncertainty})} {
			if !stagedPublicationRequiresRecovery(err) {
				t.Errorf("typed abort containing uncertainty classified as safe: %v", err)
			}
		}
		for _, abort := range []error{mutationlog.ErrClosed, mutationlog.ErrSeqExhausted,
			&mutationlog.DefiniteWALAbort{Cause: errors.New("claimed abort")}} {
			for _, joined := range []error{errors.Join(uncertainty, abort), errors.Join(abort, uncertainty)} {
				for _, err := range []error{joined, fmt.Errorf("wrapped publication: %w", joined)} {
					if !stagedPublicationRequiresRecovery(err) {
						t.Errorf("uncertain publication classified as safe: %v", err)
					}
				}
			}
		}
	}
	for _, err := range []error{nil, mutationlog.ErrClosed, mutationlog.ErrSeqExhausted,
		fmt.Errorf("closed: %w", mutationlog.ErrClosed), fmt.Errorf("exhausted: %w", mutationlog.ErrSeqExhausted),
		&mutationlog.DefiniteWALAbort{Cause: context.Canceled},
		fmt.Errorf("wrapped abort: %w", &mutationlog.DefiniteWALAbort{Cause: mutationlog.ErrClosed})} {
		if stagedPublicationRequiresRecovery(err) {
			t.Errorf("proven abort/readiness rejection requires recovery: %v", err)
		}
	}
	for _, err := range []error{errors.New("lost acknowledgement"), context.Canceled,
		context.DeadlineExceeded, mutationlog.ErrLegacyWALUncertain} {
		if !stagedPublicationRequiresRecovery(err) {
			t.Errorf("unproven outcome classified as safe: %v", err)
		}
	}
}

// Exercise each typed adapter through its normal public or ApplyMutation route:
// five local receipt families, five relay families, and standalone Create with
// and without receipts (eleven adapters, twelve runtime/request configurations).
func TestStagedPublicationWALSentinelAcrossAdapters(t *testing.T) {
	for _, failure := range []struct {
		name      string
		cause     error
		uncertain bool
	}{
		{"WAL closed", mutationlog.ErrClosed, true},
		{"WAL exhausted", mutationlog.ErrSeqExhausted, true},
		{"definite closed", &mutationlog.DefiniteWALAbort{Cause: mutationlog.ErrClosed}, false},
		{"definite exhausted", &mutationlog.DefiniteWALAbort{Cause: mutationlog.ErrSeqExhausted}, false},
	} {
		for _, operation := range []string{"vertex put", "vertex delete", "edge add", "edge delete", "contribution delete", "create", "create without receipt"} {
			for _, relay := range []bool{false, true} {
				if relay && (operation == "create" || operation == "create without receipt") {
					continue // Create has no distributed absence/arbitration contract.
				}
				t.Run(fmt.Sprintf("%s/%s/relay=%v", failure.name, operation, relay), func(t *testing.T) {
					writes := 0
					var attempted mutationlog.MutationOp
					wal := receiptEdgeDeleteWALFunc(func(entry mutationlog.Entry) error {
						writes++
						attempted = entry.Op
						if writes == 1 {
							return failure.cause
						}
						return nil
					})
					f := newReceiptEdgeDeleteFixtureWithLimits(t, wal, hlc.NodeID{0x52}, 32, 1<<20)
					runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
					invoke, id := stagedPublicationAdapterCall(t, f, runtime, operation)
					originID := f.service.clock.NodeID()
					if relay {
						origin := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x51}, 32, 1<<20)
						originRuntime := bindPublicReceiptFixtureForConcurrencyTest(t, origin)
						publish, originReceiptID := stagedPublicationAdapterCall(t, origin, originRuntime, operation)
						if err := publish(); err != nil {
							t.Fatal(err)
						}
						entry := origin.log.RetainedEntries()[0]
						wire, err := entry.Op.(interface{ ReplicationMutation() (*pb.Mutation, error) }).ReplicationMutation()
						if err != nil {
							t.Fatal(err)
						}
						invoke = func() error { return f.service.ApplyMutation(t.Context(), wire) }
						id, originID = originReceiptID, origin.service.clock.NodeID()
					}
					err := invoke()
					wantCode := connect.CodeUnavailable
					if errors.Is(failure.cause, mutationlog.ErrSeqExhausted) {
						wantCode = connect.CodeResourceExhausted
					}
					if writes != 1 || connect.CodeOf(err) != wantCode ||
						errors.Is(err, mutationlog.ErrWALIndeterminate) != failure.uncertain {
						t.Fatalf("WAL failure = %v, writes=%d", err, writes)
					}
					if f.service.receiptCommitFaulted != failure.uncertain ||
						(f.service.publicationFaultCount != 0) != failure.uncertain {
						t.Fatalf("service fault=%v count=%d", f.service.receiptCommitFaulted, f.service.publicationFaultCount)
					}
					if f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 || f.service.LocalSeq(originID) != 0 {
						t.Fatal("failed WAL attempt published Store/log/origin state")
					}
					for _, key := range []string{"tail", "head"} {
						v, live := f.cache.GetVertex(key)
						if !live || v.GetString_() != "before" {
							t.Fatalf("Vertex rollback lost %s: %v", key, v)
						}
					}
					if _, live := f.cache.GetVertex("created"); live {
						t.Fatal("failed Put leaked a Vertex")
					}
					if weight, live := f.cache.GetWeight("tail", "head"); !live || weight != 3 {
						t.Fatalf("Edge rollback = (%v, %v)", weight, live)
					}
					if _, live := f.cache.GetWeight("head", "tail"); live {
						t.Fatal("failed Create leaked an Edge")
					}
					if relay {
						pending := f.service.pendingMutations[originID][1]
						if pending == nil || pending.receiptWAL != attempted {
							t.Fatal("relay lost exact receiver-local WAL evidence")
						}
					}
					if failure.uncertain {
						assertStagedPublicationFaultViews(t, f, id)
						if err := invoke(); connect.CodeOf(err) != connect.CodeFailedPrecondition || writes != 1 {
							t.Fatalf("uncertain retry reached WAL or cleared fail-stop: %v, writes=%d", err, writes)
						}
						if _, err := f.log.CommitWithPostRingPublication("must reject", f.service.clock.Now(), nil); !errors.Is(err, mutationlog.ErrWALIndeterminate) {
							t.Fatalf("Log did not retain poison: %v", err)
						}
					} else {
						status, _, err := f.coordinator.Lookup(id, time.Now())
						if err != nil || status != mutationreceipt.NotYetObserved {
							t.Fatalf("definite abort lookup = %v, %v", status, err)
						}
						retained := attempted
						if err := invoke(); err != nil || writes != 2 || f.log.Len() != 1 || f.service.LocalSeq(originID) != 1 {
							t.Fatalf("definite retry failed to publish exactly once: %v, writes=%d", err, writes)
						}
						if relay && attempted != retained {
							t.Fatal("definite relay retry replaced immutable evidence")
						}
						if operation != "create without receipt" {
							metadata, ok := receiptWALEnvelopeInfo(attempted)
							status, receipt, err := f.coordinator.Lookup(id, time.Now())
							if !ok || len(metadata.receipts) != 1 || status != mutationreceipt.Confirmed || err != nil ||
								!bytes.Equal(receipt.Result, metadata.receipts[0].Result) {
								t.Fatalf("retry did not retain original receipt result: %v, %v", status, err)
							}
						}
					}
				})
			}
		}
	}
}

func stagedPublicationAdapterCall(t *testing.T, f receiptEdgeDeleteFixture, runtime *ServingRuntime, operation string) (func() error, mutationreceipt.ID) {
	t.Helper()
	for _, key := range []string{"tail", "head"} {
		if err := f.cache.PutVertex(key, &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "before"}}); err != nil {
			t.Fatal(err)
		}
	}
	contribution := graphcache.ContribID{0x80}
	f.cache.AddEdgeWithExpirationContrib("tail", "head", 3, time.Now().Add(time.Hour), contribution)
	context := publicReceiptContext(t, runtime, 0x71, 1)
	id, err := mutationreceipt.DecodeID(context.OperationIds[0])
	if err != nil {
		t.Fatal(err)
	}
	return func() error {
		var err error
		switch operation {
		case "vertex put":
			_, err = f.service.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "created"}}, IfAbsent: true, ReceiptContext: context})
		case "vertex delete":
			_, err = f.service.DeleteVertices(t.Context(), &pb.DeleteVerticesRequest{Keys: []string{"tail"}, ReceiptContext: context})
		case "edge add":
			added := graphcache.ContribID{0x81}
			_, err = f.service.AddEdges(t.Context(), &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tail", Head: "head", Weight: 2}}, ContribIds: [][]byte{added[:]}, ReceiptContext: context})
		case "edge delete":
			_, err = f.service.DeleteEdges(t.Context(), &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "tail", Head: "head"}}, ReceiptContext: context})
		case "contribution delete":
			_, err = f.service.DeleteEdgeContributions(t.Context(), &pb.DeleteEdgeContributionsRequest{Contributions: []*pb.EdgeContributionKey{{Tail: "tail", Head: "head", ContribId: contribution[:]}}, ReceiptContext: context})
		case "create", "create without receipt":
			request := &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "head", Head: "tail", Weight: 2}}}
			if operation == "create" {
				request.ReceiptContext = context
			}
			_, err = f.service.CreateEdges(t.Context(), request)
		default:
			t.Fatalf("unknown operation %s", operation)
		}
		return err
	}, id
}

func assertStagedPublicationFaultViews(t *testing.T, f receiptEdgeDeleteFixture, id mutationreceipt.ID) {
	t.Helper()
	select {
	case <-f.service.publicationFaultCh:
	default:
		t.Fatal("publication fault did not invalidate the generation")
	}
	_, _, lookupErr := f.coordinator.Lookup(id, time.Now())
	_, statusErr := f.service.GetReceiptStatus(t.Context(), &pb.GetReceiptStatusRequest{OperationId: id.Bytes()})
	_, readErr := (&lanternServiceConnect{svc: f.service}).GetVertex(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: "tail"}))
	_, peerErr := f.replication.PeerStatus(t.Context(), &pb.PeerStatusRequest{})
	snapshotErr := f.replication.Snapshot(t.Context(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{})
	subscribeErr := f.replication.Subscribe(t.Context(), &pb.SubscribeRequest{AcceptReceiptEnvelopes: true}, &replicationSubscribeRecorder{})
	for name, err := range map[string]error{"lookup": lookupErr, "status": statusErr, "read": readErr, "peer": peerErr, "snapshot": snapshotErr, "subscribe": subscribeErr} {
		wantCode := connect.CodeFailedPrecondition
		if name == "status" {
			// Preserve receiptLookupError's existing public error mapping.
			wantCode = connect.CodeInternal
		}
		if connect.CodeOf(err) != wantCode {
			t.Errorf("faulted %s exposed a view: %v", name, err)
		}
	}
}

func TestStagedPublicationHeldWALSentinelRejectsWaitingViews(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		close(entered)
		<-release
		return mutationlog.ErrClosed
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	invoke, id := stagedPublicationAdapterCall(t, f, runtime, "edge delete")
	committed := make(chan error, 1)
	go func() { committed <- invoke() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("WAL not reached")
	}
	views := map[string]func() error{
		"read": func() error {
			_, err := (&lanternServiceConnect{svc: f.service}).GetVertex(t.Context(), connect.NewRequest(&pb.GetVertexRequest{Key: "tail"}))
			return err
		},
		"receipt": func() error {
			_, err := f.service.GetReceiptStatus(t.Context(), &pb.GetReceiptStatusRequest{OperationId: id.Bytes()})
			return err
		},
		"peer": func() error {
			_, err := f.replication.PeerStatus(t.Context(), &pb.PeerStatusRequest{})
			return err
		},
		"snapshot": func() error {
			return f.replication.Snapshot(t.Context(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{})
		},
	}
	completed := make(chan error, len(views))
	started := make(chan struct{}, len(views))
	for name, read := range views {
		go func() {
			started <- struct{}{}
			err := read()
			wantCode := connect.CodeFailedPrecondition
			if name == "receipt" {
				wantCode = connect.CodeInternal
			}
			if connect.CodeOf(err) != wantCode {
				completed <- fmt.Errorf("waiting %s returned %v", name, err)
				return
			}
			completed <- nil
		}()
	}
	for range views {
		<-started
	}
	select {
	case err := <-completed:
		t.Fatalf("view completed while WAL was held: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := waitReceiptTest(t, "held WAL completion", committed); !errors.Is(err, mutationlog.ErrWALIndeterminate) {
		t.Fatalf("held WAL outcome = %v", err)
	}
	for range views {
		select {
		case err := <-completed:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Fatal("view did not resolve after WAL failure")
		}
	}
}

func TestStagedPublicationUncertaintyThenPanicFaultsOnce(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error { return mutationlog.ErrClosed }))
	const interruption = "cleanup interrupted after uncertain WAL"
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		f.service.replicationCutMu.Lock()
		defer f.service.replicationCutMu.Unlock()
		f.service.receiptOriginCutMu.Lock()
		defer f.service.receiptOriginCutMu.Unlock()
		publication := stagedPublication{service: f.service}
		defer publication.failClosedOnPanic()
		if err := publication.commit("fault proof", f.service.clock.Now(), nil, func() {
			t.Error("uncertain WAL reached graph release")
		}, nil); !errors.Is(err, mutationlog.ErrWALIndeterminate) {
			t.Fatalf("WAL outcome = %v", err)
		}
		panic(interruption)
	}()
	if recovered != interruption || !f.service.receiptCommitFaulted || f.service.publicationFaultCount != 1 {
		t.Fatalf("panic=%v fault=%v count=%d", recovered, f.service.receiptCommitFaulted, f.service.publicationFaultCount)
	}
	select {
	case <-f.service.publicationFaultCh:
	default:
		t.Fatal("fault generation remained open")
	}
}

// A durable frame may exist even though the service rolled its staged graph
// back. Replay recovers the original graph/result cut into a detached candidate;
// it does not clear the fault on the old serving generation.
func TestStagedPublicationLostAcknowledgementRecoversOriginalResult(t *testing.T) {
	for _, sentinel := range []error{mutationlog.ErrClosed, mutationlog.ErrSeqExhausted} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lost-ack.wal")
			fileWAL, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fileWAL.Close() })
			var durable mutationlog.Entry
			wal := receiptEdgeDeleteWALFunc(func(entry mutationlog.Entry) error {
				if err := fileWAL.Write(entry); err != nil {
					return err
				}
				durable = entry
				return sentinel // durable success, lost acknowledgement
			})
			f := newReceiptVertexPutFixture(t, wal, hlc.NodeID{0x74}, 8, nil)
			call := receiptVertexPutTestCall(t, f.epoch, 0x79, true,
				&pb.Vertex{Key: "durable", Value: &pb.Vertex_String_{String_: "original"}})
			if _, err := f.coordinator.Commit(t.Context(), call); !errors.Is(err, mutationlog.ErrWALIndeterminate) {
				t.Fatalf("lost acknowledgement = %v", err)
			}
			if _, live := f.cache.GetVertex("durable"); live || f.log.Len() != 0 || f.store.Stats().Entries != 0 || !f.service.receiptCommitFaulted {
				t.Fatal("uncertain local generation did not roll back and fault")
			}
			if _, _, err := f.coordinator.Lookup(call.Items[0].ID, time.Now()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("old generation exposed absence: %v", err)
			}
			if err := f.log.Close(); err != nil {
				t.Fatal(err)
			}
			if err := fileWAL.Close(); err != nil {
				t.Fatal(err)
			}
			candidate, err := resumeReceiptWALCandidate(path, mutationreceipt.Config{
				Epoch: f.epoch, Retention: time.Hour, MaxEntries: 8, MaxBytes: 1 << 20,
			}, time.Now(), mutationlog.Options{Capacity: 16}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			metadata, ok := receiptWALEnvelopeInfo(durable.Op)
			if !ok || len(metadata.receipts) != 1 {
				t.Fatal("lost durable receipt evidence")
			}
			requireReceiptWALEvidence(t, candidate, metadata.receipts)
			vertex, live := candidate.graph.GetVertex("durable")
			if !live || vertex.GetString_() != "original" || candidate.origins.LocalSeq(metadata.origin) != 1 || !candidate.hlcFrontier.Equal(durable.HLC) {
				t.Fatal("recovery did not restore the durable graph/origin/HLC cut")
			}
			if seq, ok := candidate.log.LastSeq(); !ok || seq != 1 || !f.service.receiptCommitFaulted {
				t.Fatal("recovery lost Log progress or cleared the old generation fault")
			}
		})
	}
}

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
