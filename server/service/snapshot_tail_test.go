package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSnapshotTailRejectsUnadmittedOrAmbiguousMetadata(t *testing.T) {
	f := newSnapshotTailServiceFixture(t, 16)
	auth := newSnapshotTailAdmissionFixture(t, 2)
	admitted, _ := auth.context(t, 0, time.Second)
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		values []string
		header http.Header
	}{
		{"anonymous", t.Context(), []string{"reserve"}, make(http.Header)},
		{"no finite deadline", peerauth.WithAdmission(t.Context(), func() *peerauth.Admission {
			a, err := auth.store.Admit(auth.states[0], "")
			if err != nil {
				t.Fatal(err)
			}
			return a
		}()), []string{"reserve"}, make(http.Header)},
		{"duplicate", admitted, []string{"reserve", "reserve"}, make(http.Header)},
		{"empty", admitted, []string{""}, make(http.Header)},
		{"unknown", admitted, []string{"other"}, make(http.Header)},
		{"no response metadata owner", admitted, []string{"reserve"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &replicationSnapshotRecorder{}
			err := f.replica.Snapshot(WithReplicationTailMetadata(tc.ctx, tc.values, tc.header), &pb.SnapshotRequest{}, recorder)
			if err == nil || len(recorder.frames) != 0 {
				t.Fatalf("invalid reservation = %v, sent %d frames", err, len(recorder.frames))
			}
			assertSnapshotTailEmpty(t, f.replica)
		})
	}
}

func TestSnapshotTailClaimBindsPeerCutAndSingleUse(t *testing.T) {
	for _, name := range []string{"other member", "other store", "local cutoff", "origin cutoff", "namespace", "fault generation", "expired original", "single use"} {
		t.Run(name, func(t *testing.T) {
			f := newSnapshotTailServiceFixture(t, 16)
			auth := newSnapshotTailAdmissionFixture(t, 2)
			original, cancelOriginal := auth.context(t, 0, 5*time.Second)
			token, header, tail := reserveSnapshotTail(t, f.replica, original)
			// ProtectHandler cancels its successful RPC context on return.
			cancelOriginal()
			current, cancelCurrent := auth.context(t, 0, 5*time.Second)
			req := snapshotTailSubscribeRequest(header)
			switch name {
			case "other member":
				current, cancelCurrent = auth.context(t, 1, 5*time.Second)
			case "other store":
				other := newSnapshotTailAdmissionFixture(t, 2)
				current, cancelCurrent = other.context(t, 0, 5*time.Second)
			case "local cutoff":
				req.FromLocalSeq++
			case "origin cutoff":
				for origin := range req.FromSeqPerOrigin {
					delete(req.FromSeqPerOrigin, origin)
				}
			case "namespace":
				req.NamespaceFormat = "foreign-domain"
			case "fault generation":
				finish, err := f.primary.BeginSnapshotInstall()
				if err != nil {
					t.Fatal(err)
				}
				finish(true)
			case "expired original":
				auth.mu.Lock()
				auth.now = auth.now.Add(31 * time.Second)
				auth.mu.Unlock()
				current, cancelCurrent = auth.context(t, 0, 5*time.Second)
			case "single use":
				sender := &snapshotTailClaimSender{ctx: current, ready: make(chan struct{}), frames: make(chan *pb.SubscribeResponse, 1)}
				done := make(chan error, 1)
				go func() {
					done <- f.replica.Subscribe(WithReplicationTailMetadata(current, []string{token}, nil), req, sender)
				}()
				select {
				case <-sender.ready:
				case err := <-done:
					t.Fatalf("first claim failed: %v", err)
				case <-time.After(time.Second):
					t.Fatal("first claim did not flush idle handshake")
				}
				cancelCurrent()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("claimed Subscribe owner did not join")
				}
				current, cancelCurrent = auth.context(t, 0, 5*time.Second)
				waitSnapshotTailStopped(t, tail)
			}
			defer cancelCurrent()
			recorder := &replicationSubscribeRecorder{frames: make(chan *pb.SubscribeResponse, 1)}
			if err := f.replica.Subscribe(WithReplicationTailMetadata(current, []string{token}, nil), req, recorder); err == nil || len(recorder.frames) != 0 {
				t.Fatalf("invalid claim = %v, sent %d frames", err, len(recorder.frames))
			}
			f.replica.CloseSnapshotTails()
			waitSnapshotTailStopped(t, tail)
			assertSnapshotTailEmpty(t, f.replica)
		})
	}
}

func TestSnapshotTailReservationAndOwnedStorageBounds(t *testing.T) {
	t.Run("peer and global owners", func(t *testing.T) {
		f := newSnapshotTailServiceFixture(t, 16)
		auth := newSnapshotTailAdmissionFixture(t, maxSnapshotTails+1)
		for i := range maxSnapshotTails {
			ctx, _ := auth.context(t, i, 5*time.Second)
			reserveSnapshotTail(t, f.replica, ctx)
			if i == 0 {
				recorder := &replicationSnapshotRecorder{}
				err := f.replica.Snapshot(WithReplicationTailMetadata(ctx, []string{"reserve"}, make(http.Header)), &pb.SnapshotRequest{}, recorder)
				if connect.CodeOf(err) != connect.CodeResourceExhausted || len(recorder.frames) != 0 {
					t.Fatalf("second owner for one peer = %v, frames=%d", err, len(recorder.frames))
				}
			}
		}
		ctx, _ := auth.context(t, maxSnapshotTails, 5*time.Second)
		err := f.replica.Snapshot(WithReplicationTailMetadata(ctx, []string{"reserve"}, make(http.Header)), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{})
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("global reservation bound = %v", err)
		}
		f.replica.CloseSnapshotTails()
		assertSnapshotTailEmpty(t, f.replica)
	})
	t.Run("byte and entry credits before allocation", func(t *testing.T) {
		m := &snapshotTailManager{}
		tail := &snapshotTail{manager: m}
		mutation := &pb.Mutation{Seq: 1, Origin: make([]byte, 16), Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{Key: "key"}}}}
		frame, err := subscribeMutationFrame(mutation)
		if err != nil {
			t.Fatal(err)
		}
		size := proto.Size(frame)
		for _, tc := range []struct {
			name                     string
			bytes, entries           int
			totalBytes, totalEntries int
			reason                   snapshotTailReason
		}{
			{"tail bytes", maxSnapshotTailBytes - size + 1, 0, 0, 0, "tail_bytes"},
			{"global bytes", 0, 0, maxSnapshotTotalBytes - size + 1, 0, "total_bytes"},
			{"tail entries", 0, maxSnapshotTailEntries, 0, 0, "tail_entries"},
			{"global entries", 0, 0, 0, maxSnapshotTotalEntries, "total_entries"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tail.bytes, tail.entries = tc.bytes, tc.entries
				tail.terminal = "" // Each row models a separate fresh owner.
				m.totalBytes, m.totalEntries = tc.totalBytes, tc.totalEntries
				entry, err := tail.project(mutationlog.Entry{Seq: 1, Op: mutation})
				if !errors.Is(err, mutationlog.ErrSubscriptionLost) || errors.Is(err, mutationlog.ErrTailQueueFull) || entry.Op != nil {
					t.Fatalf("over-budget projection = %+v, %v", entry, err)
				}
				if tail.terminal != tc.reason {
					t.Fatalf("projection terminal category = %q, want %q", tail.terminal, tc.reason)
				}
				if tail.bytes != tc.bytes || tail.entries != tc.entries || m.totalBytes != tc.totalBytes || m.totalEntries != tc.totalEntries {
					t.Fatal("rejected projection consumed credits")
				}
			})
		}
		tail.bytes, tail.entries, m.totalBytes, m.totalEntries = 0, 0, 0, 0
		entry, err := tail.project(mutationlog.Entry{Seq: 1, Op: mutation})
		if err != nil {
			t.Fatal(err)
		}
		data, ok := entry.Op.(snapshotTailFrame)
		if !ok || len(data) != size || tail.bytes != size || tail.entries != 1 || m.totalBytes != size || m.totalEntries != 1 {
			t.Fatal("owned projection failed to charge exact encoded storage")
		}
		mutation.Op.GetDeleteVertex().Key = "changed after projection"
		var detached pb.SubscribeResponse
		if err := proto.Unmarshal(data, &detached); err != nil || detached.GetMutation().GetOp().GetDeleteVertex().GetKey() != "key" {
			t.Fatalf("projection retained original protobuf references: %v", err)
		}
		tail.release(size)
		if tail.bytes != 0 || tail.entries != 0 || m.totalBytes != 0 || m.totalEntries != 0 {
			t.Fatal("delivered projection retained credits")
		}
	})
}

func TestSnapshotTailFailureExpiryAndShutdownReleaseOwners(t *testing.T) {
	for _, name := range []string{"send failure", "request cancellation", "deadline", "membership removal", "publication fault", "log close", "shutdown", "queue overflow"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newSnapshotTailServiceFixture(t, 2)
				auth := newSnapshotTailAdmissionFixture(t, 2)
				lifetime := 5 * time.Second
				if name == "deadline" {
					lifetime = 50 * time.Millisecond
				}
				ctx, cancel := auth.context(t, 0, lifetime)
				if name == "send failure" || name == "request cancellation" {
					recorder := &replicationSnapshotRecorder{onSend: func(*pb.SnapshotResponse) error {
						if name == "request cancellation" {
							cancel()
							return ctx.Err()
						}
						return errors.New("controlled Snapshot send failure")
					}}
					err := f.replica.Snapshot(WithReplicationTailMetadata(ctx, []string{"reserve"}, make(http.Header)), &pb.SnapshotRequest{}, recorder)
					if err == nil {
						t.Fatal("failed Snapshot returned success")
					}
					assertSnapshotTailEmpty(t, f.replica)
					return
				}
				_, _, tail := reserveSnapshotTail(t, f.replica, ctx)
				if _, err := f.primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
					Key: "queued-before-terminal", Value: &pb.Vertex_String_{String_: "owned encoded payload"},
					Expiration: timestamppb.New(time.Now().Add(time.Hour)),
				}}}); err != nil {
					t.Fatal(err)
				}
				// Wait for the real dispatcher without advancing the 50ms expiry.
				// Every following terminal path must release an actual queued frame.
				synctest.Wait()
				m := f.replica.snapshotTails
				m.mu.Lock()
				charged := len(m.active) == 1 && m.totalBytes > 0 && m.totalEntries == 1 &&
					tail.bytes == m.totalBytes && tail.entries == 1
				m.mu.Unlock()
				if !charged || len(tail.subscription.Entries()) != 1 {
					t.Fatal("terminal fixture did not own one queued encoded frame")
				}
				switch name {
				case "membership removal":
					auth.manifest.Version++
					auth.manifest.Members = auth.manifest.Members[1:]
					raw, err := peerauth.SignManifest(auth.manifest, auth.key)
					if err != nil {
						t.Fatal(err)
					}
					if err := auth.store.Apply(raw); err != nil {
						t.Fatal(err)
					}
				case "publication fault":
					finish, err := f.primary.BeginSnapshotInstall()
					if err != nil {
						t.Fatal(err)
					}
					finish(true)
				case "log close":
					if err := f.log.Close(); err != nil {
						t.Fatal(err)
					}
				case "shutdown":
					f.replica.CloseSnapshotTails()
				case "queue overflow":
					for i := range 8 {
						if _, err := f.primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
							Key: fmt.Sprintf("tail-%d", i), Expiration: timestamppb.New(time.Now().Add(time.Hour)),
						}}}); err != nil {
							t.Fatal(err)
						}
					}
				}
				waitSnapshotTailStopped(t, tail)
				assertSnapshotTailEmpty(t, f.replica)
				m.mu.Lock()
				remainingBytes, remainingEntries := tail.bytes, tail.entries
				m.mu.Unlock()
				if remainingBytes != 0 || remainingEntries != 0 || len(tail.subscription.Entries()) != 0 {
					t.Fatal("terminal owner retained credits or queued frame references")
				}
				select {
				case <-tail.released:
				default:
					t.Fatal("terminal owner did not acknowledge release")
				}
			})
		})
	}
}

type snapshotTailHeldSender struct {
	entered     chan struct{}
	release     chan struct{}
	interrupted chan struct{}
	sendOnce    sync.Once
	abortOnce   sync.Once
}

func (s *snapshotTailHeldSender) Send(*pb.SubscribeResponse) error {
	s.sendOnce.Do(func() { close(s.entered) })
	<-s.release
	return nil
}

// Interrupt records withdrawal without pretending the synchronous Send has
// relinquished its frame. Only the test's release acknowledges that ownership.
func (s *snapshotTailHeldSender) Interrupt() {
	s.abortOnce.Do(func() { close(s.interrupted) })
}

func TestSnapshotTailClaimedSendRetainsCreditsAndSlotUntilOwnerExit(t *testing.T) {
	f := newSnapshotTailServiceFixture(t, 16)
	auth := newSnapshotTailAdmissionFixture(t, 2)
	original, cancelOriginal := auth.context(t, 0, 5*time.Second)
	token, header, tail := reserveSnapshotTail(t, f.replica, original)
	cancelOriginal()
	if _, err := f.primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
		Key: "held-tail", Value: &pb.Vertex_String_{String_: "retained while Send is blocked"},
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	}}}); err != nil {
		t.Fatal(err)
	}
	current, cancelCurrent := auth.context(t, 0, 5*time.Second)
	sender := &snapshotTailHeldSender{entered: make(chan struct{}), release: make(chan struct{}), interrupted: make(chan struct{})}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(sender.release) }) }
	done := make(chan error, 1)
	go func() {
		done <- f.replica.Subscribe(WithReplicationTailMetadata(current, []string{token}, nil), snapshotTailSubscribeRequest(header), sender)
	}()
	defer func() {
		unblock()
		cancelCurrent()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("claimed synchronous sender did not join")
		}
	}()
	select {
	case <-sender.entered:
	case err := <-done:
		t.Fatalf("claim ended before held delivery: %v", err)
	case <-time.After(time.Second):
		t.Fatal("claimed delivery did not enter Send")
	}
	m := f.replica.snapshotTails
	m.mu.Lock()
	heldBytes, heldEntries := m.totalBytes, m.totalEntries
	claimed := tail.claimed
	m.mu.Unlock()
	if !claimed || heldBytes == 0 || heldEntries != 1 {
		t.Fatalf("held Send accounting: claimed=%t bytes=%d entries=%d", claimed, heldBytes, heldEntries)
	}
	tail.closeResources()
	waitSnapshotTailStopped(t, tail)
	select {
	case <-sender.interrupted:
	case <-time.After(time.Second):
		t.Fatal("terminal tail did not interrupt its synchronous transport owner")
	}
	// A release raced with invalidation cannot refund an in-flight holder.
	tail.release(heldBytes)
	m.mu.Lock()
	stillOwned := m.active[token] == tail && tail.closed && m.totalBytes == heldBytes && m.totalEntries == heldEntries
	m.mu.Unlock()
	if !stillOwned {
		t.Fatal("invalidating a claimed tail refunded storage or its peer slot before Send exited")
	}
	newContext, _ := auth.context(t, 0, 5*time.Second)
	recorder := &replicationSnapshotRecorder{}
	if err := f.replica.Snapshot(WithReplicationTailMetadata(newContext, []string{"reserve"}, make(http.Header)), &pb.SnapshotRequest{}, recorder); connect.CodeOf(err) != connect.CodeResourceExhausted || len(recorder.frames) != 0 {
		t.Fatalf("new reservation reused a still-owned peer slot: err=%v frames=%d", err, len(recorder.frames))
	}
	shutdownDone := make(chan struct{})
	go func() {
		f.replica.CloseSnapshotTails()
		close(shutdownDone)
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("manager shutdown did not begin")
		}
	}
	select {
	case <-shutdownDone:
		t.Fatal("manager shutdown completed while Send still owned its frame")
	default:
	}
	unblock()
	select {
	case <-tail.released:
	case <-time.After(time.Second):
		t.Fatal("handler exit did not release its claimed tail")
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("manager shutdown did not join after the owner acknowledged exit")
	}
	assertSnapshotTailEmpty(t, f.replica)
}

func TestSnapshotTailClaimRejectsTerminalSubscriptionBeforeWatcherCleanup(t *testing.T) {
	f := newSnapshotTailServiceFixture(t, 16)
	auth := newSnapshotTailAdmissionFixture(t, 2)
	original, _ := auth.context(t, 0, 5*time.Second)
	token, header, owner := reserveSnapshotTail(t, f.replica, original)
	owner.Close()
	// Recreate only the claim state at the instant core delivery is terminal
	// but its watcher has not published invalidation. Keeping this boundary
	// isolated removes a race where watcher cleanup could mask the Err check.
	m := f.replica.snapshotTails
	tail := &snapshotTail{manager: m, token: token, admission: owner.admission,
		deadline: owner.deadline, local: owner.local, origins: owner.origins, fault: owner.fault,
		subscription: owner.subscription, ready: true, opened: make(chan struct{}),
		stop: make(chan struct{}), stopped: make(chan struct{}), released: make(chan struct{})}
	close(tail.opened)
	close(tail.stopped)
	m.mu.Lock()
	m.active[token] = tail
	m.mu.Unlock()
	if tail.closed || tail.subscription.Err() == nil {
		t.Fatal("test did not retain the pre-cleanup terminal subscription boundary")
	}
	current, _ := auth.context(t, 0, 5*time.Second)
	req := snapshotTailSubscribeRequest(header)
	cursor, err := parseOriginCursor(req.GetFromSeqPerOrigin())
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.replica.claimSnapshotTail(current, token, req, cursor, tail.fault)
	if err == nil || claimed != nil {
		if claimed != nil {
			claimed.Close()
		}
		t.Fatalf("terminal subscription was claimed: tail=%v err=%v", claimed != nil, err)
	}
	f.replica.CloseSnapshotTails()
	assertSnapshotTailEmpty(t, f.replica)
}

// A RECEIPT Snapshot owns the same encoded handoff as GRAPH_ONLY, while the
// richer original results, accepted effects and Store guards remain mandatory.
func TestSnapshotTailReceiptHandoffPreservesEnvelopeAndDuplicateGuards(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	// Rewire the still-pristine fixture to a deliberately short ring. Receipt
	// coordination remains service-owned; only retained-history capacity changes.
	f.log = mutationlog.New(mutationlog.Options{Capacity: 4, SubscriberBuffer: 16})
	t.Cleanup(func() { _ = f.log.Close() })
	f.service.WithReplication(f.log, f.service.clock, nil)
	f.replication = NewLanternReplicationService(f.log, f.cache, f.service.clock).WithOriginStates(f.service)
	t.Cleanup(f.replication.CloseSnapshotTails)
	source := receiptCaptureSource(t, f)
	policy := receiptCapturePolicy(f.epoch)
	if err := f.replication.ConfigureReceiptSnapshot(source, policy); err != nil {
		t.Fatal(err)
	}
	liveKey := graphcache.EdgeKey[string]{Tail: "receipt-tail", Head: "live-head"}
	missingKey := graphcache.EdgeKey[string]{Tail: "receipt-tail", Head: "missing-head"}
	f.cache.AddEdgeWithExpiration(liveKey.Tail, liveKey.Head, 1, time.Now().Add(time.Hour))
	auth := newSnapshotTailAdmissionFixture(t, 1)
	original, cancelOriginal := auth.context(t, 0, 5*time.Second)
	responseHeader := make(http.Header)
	expected := make([][]byte, 0, 8)
	var firstCall receiptEdgeDeleteCall
	recorder := &replicationSnapshotRecorder{onSend: func(frame *pb.SnapshotResponse) error {
		if frame.GetHeader() == nil {
			return nil
		}
		// The header and receipt Store cut are detached already; all writes
		// below occur during Snapshot transfer and before token claim.
		for n := range 8 {
			call := receiptDeleteCall(t, f.epoch, liveKey, missingKey)
			call.Group = mutationreceipt.GroupID{0xe1, byte(n + 1)}
			for i := range call.Items {
				id, err := mutationreceipt.NewID(f.epoch, time.Now().Add(-time.Second), [24]byte{0xc1, byte(n + 1), byte(i + 1)})
				if err != nil {
					return err
				}
				call.Items[i].ID = id
			}
			result, err := f.coordinator.Commit(t.Context(), call)
			if err != nil {
				return err
			}
			if n == 0 {
				firstCall = call
				if got := result.GetExisted(); len(got) != 2 || !got[0] || got[1] {
					return fmt.Errorf("unaligned original receipt results: %v", got)
				}
			}
			retained := f.log.RetainedEntries()
			projected, err := subscribeMutationFrame(retained[len(retained)-1].Op)
			if err != nil {
				return err
			}
			raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(projected)
			if err != nil {
				return err
			}
			expected = append(expected, raw)
		}
		return nil
	}}
	if err := f.replication.Snapshot(WithReplicationTailMetadata(original, []string{"reserve"}, responseHeader),
		&pb.SnapshotRequest{RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT}, recorder); err != nil {
		t.Fatal(err)
	}
	cancelOriginal()
	if err := validateReceiptSnapshotFrames(recorder.frames, policy, mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch: policy.Epoch, MaxEntries: policy.MaxEntries, MaxBytes: policy.MaxBytes,
	}); err != nil {
		t.Fatalf("reserved receipt Snapshot was not complete/valid: %v", err)
	}
	header := recorder.frames[0].GetHeader()
	if header.GetCutoffLocalSeq() != 0 || recorder.frames[len(recorder.frames)-1].GetFooter().GetActiveReceiptCount() != 0 {
		t.Fatal("post-cut receipt writes leaked into the Snapshot cut")
	}
	if first, _ := f.log.FirstSeq(); first != 5 || len(expected) != 8 {
		t.Fatalf("receipt tail was not evicted during transfer: first=%d, writes=%d", first, len(expected))
	}
	token := responseHeader.Get(SnapshotTailHeader)
	if len(token) != 64 {
		t.Fatalf("receipt Snapshot did not return exact handoff metadata: %q", token)
	}
	current, cancelCurrent := auth.context(t, 0, 5*time.Second)
	sender := &snapshotTailClaimSender{ctx: current, ready: make(chan struct{}), frames: make(chan *pb.SubscribeResponse, 8)}
	done := make(chan error, 1)
	go func() {
		done <- f.replication.Subscribe(WithReplicationTailMetadata(current, []string{token}, nil), snapshotTailSubscribeRequest(header), sender)
	}()
	defer func() {
		cancelCurrent()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("receipt Subscribe owner did not join")
		}
	}()
	receiver := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x62}, 32, 1<<20)
	receiver.cache.AddEdgeWithExpiration(liveKey.Tail, liveKey.Head, 1, time.Now().Add(time.Hour))
	origin := f.service.clock.NodeID()
	var firstMutation *pb.Mutation
	for n, want := range expected {
		var frame *pb.SubscribeResponse
		select {
		case frame = <-sender.frames:
		case err := <-done:
			t.Fatalf("receipt handoff ended before seq %d: %v", n+1, err)
		case <-time.After(time.Second):
			t.Fatalf("receipt handoff omitted seq %d", n+1)
		}
		got, err := proto.MarshalOptions{Deterministic: true}.Marshal(frame)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("serialized handoff changed receipt/effect frame %d: %v", n+1, err)
		}
		mutation := frame.GetMutation()
		if n == 0 {
			firstMutation = proto.Clone(mutation).(*pb.Mutation)
			envelope, err := decodeReceiptEdgeDeleteMutation(mutation)
			if err != nil || len(envelope.Receipts) != 2 ||
				!bytes.Equal(envelope.Receipts[0].Result, []byte{1}) || !bytes.Equal(envelope.Receipts[1].Result, []byte{0}) || len(envelope.Accepted) != 2 {
				t.Fatalf("handoff lost typed aligned results/accepted effects: envelope=%+v err=%v", envelope, err)
			}
		}
		if err := receiver.service.ApplyMutation(t.Context(), mutation); err != nil {
			t.Fatalf("receiver rejected claimed receipt seq %d: %v", n+1, err)
		}
	}
	if receiver.service.LocalSeq(origin) != 8 || receiver.log.Len() != 8 || receiver.coordinator.store.Stats().Entries != 16 {
		t.Fatal("receipt tail did not commit the exact contiguous graph/Store/log frontier")
	}
	for i, item := range firstCall.Items {
		status, receipt, err := receiver.coordinator.Lookup(item.ID, time.Now())
		want := byte(0)
		if i == 0 {
			want = 1
		}
		if err != nil || status != mutationreceipt.Confirmed || !bytes.Equal(receipt.Result, []byte{want}) {
			t.Fatalf("receiver changed original result at request index %d: status=%v receipt=%+v err=%v", i, status, receipt, err)
		}
	}
	if err := receiver.service.ApplyMutation(t.Context(), firstMutation); err != nil {
		t.Fatalf("valid committed receipt duplicate failed: %v", err)
	}
	for _, name := range []string{"epoch", "policy"} {
		invalid := proto.Clone(firstMutation).(*pb.Mutation)
		call := invalid.GetOp().GetReplicatedReceiptEdgeDelete()
		if name == "epoch" {
			call.DeploymentEpoch[0] ^= 1
			for _, item := range call.Items {
				// Keep the foreign envelope structurally valid, so the test
				// reaches Store binding before committed-sequence suppression.
				copy(item.Receipt.OperationId[1:17], call.DeploymentEpoch)
			}
		} else {
			call.PolicyFingerprint[0] ^= 1
		}
		if err := receiver.service.ApplyMutation(t.Context(), invalid); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("%s-mismatched committed duplicate bypassed Store guard: %v", name, err)
		}
		if receiver.service.LocalSeq(origin) != 8 || receiver.log.Len() != 8 || receiver.coordinator.store.Stats().Entries != 16 {
			t.Fatalf("%s-mismatched duplicate changed committed state", name)
		}
	}
}

func TestSnapshotTailBudgetPrecedesAllocatingProjection(t *testing.T) {
	mutation := &pb.Mutation{Seq: 1, Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
		DeleteVertex: &pb.DeleteVertexRequest{Key: "invalid-for-rich-projector"},
	}}}
	for _, row := range []struct {
		name string
		op   mutationlog.MutationOp
	}{
		{"graph Add", &graphAddEffectEnvelope{Mutation: mutation}},
		{"mixed graph mutation", &graphMutationEnvelope{Mutation: mutation}},
	} {
		t.Run(row.name, func(t *testing.T) {
			if _, err := subscribeMutationFrame(row.op); err == nil || errors.Is(err, mutationlog.ErrSubscriptionLost) {
				t.Fatalf("fixture does not distinguish semantic projection from budget rejection: %v", err)
			}
			manager := &snapshotTailManager{totalBytes: maxSnapshotTailBytes}
			tail := &snapshotTail{manager: manager, bytes: maxSnapshotTailBytes}
			entry, err := tail.project(mutationlog.Entry{Seq: 1, Op: row.op})
			if !errors.Is(err, mutationlog.ErrSubscriptionLost) || entry.Op != nil {
				t.Fatalf("exhausted budget reached allocating/semantic projection: entry=%+v err=%v", entry, err)
			}
			if tail.bytes != maxSnapshotTailBytes || manager.totalBytes != maxSnapshotTailBytes || tail.entries != 0 || manager.totalEntries != 0 {
				t.Fatal("rejected projection consumed or refunded existing credits")
			}
		})
	}
}

func TestSnapshotTailOriginMetadataBoundRejectsBeforeReservation(t *testing.T) {
	f := newSnapshotTailServiceFixture(t, 16)
	auth := newSnapshotTailAdmissionFixture(t, 1)
	ctx, _ := auth.context(t, 0, time.Second)
	origins := make(map[string]uint64, maxSnapshotTailOrigins+1)
	for n := range maxSnapshotTailOrigins + 1 {
		origins[fmt.Sprintf("%032x", n+1)] = 1
	}
	var tail *snapshotTail
	var openErr error
	if err := f.primary.withReplicationSnapshotCut(func() {
		tail, openErr = f.replica.openSnapshotTail(ctx, 8, origins, f.primary.publicationFaultCh)
	}); err != nil {
		t.Fatal(err)
	}
	if tail != nil || connect.CodeOf(openErr) != connect.CodeResourceExhausted {
		if tail != nil {
			tail.Close()
		}
		t.Fatalf("oversized origin metadata retained reservation: tail=%v err=%v", tail != nil, openErr)
	}
	assertSnapshotTailEmpty(t, f.replica)
}

func assertSnapshotTailDiagnostic(t *testing.T, err error, stage, guard string, terminal snapshotTailReason) {
	t.Helper()
	var diagnostic *snapshotTailDiagnostic
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.As(err, &diagnostic) {
		t.Fatalf("tail failure lost its FailedPrecondition diagnostic: %v", err)
	}
	if diagnostic.stage != stage || diagnostic.guard != guard || diagnostic.terminal != terminal {
		t.Fatalf("tail diagnostic = %s/%s/%s, want %s/%s/%s", diagnostic.stage, diagnostic.guard, diagnostic.terminal, stage, guard, terminal)
	}
}

func TestSnapshotTailDiagnosticDistinguishesOverflowBeforeFinishFromMissingClaim(t *testing.T) {
	for _, phase := range []string{"before footer", "after successful finish"} {
		t.Run(phase, func(t *testing.T) {
			f := newSnapshotTailServiceFixture(t, 2)
			auth := newSnapshotTailAdmissionFixture(t, 2)
			original, cancelOriginal := auth.context(t, 0, 5*time.Second)
			var tail *snapshotTail
			overflow := func() {
				t.Helper()
				for i := range 5 {
					if _, err := f.primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{
						Key: fmt.Sprintf("overflow-%d", i), Expiration: timestamppb.New(time.Now().Add(time.Hour)),
					}}); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-tail.subscription.Done():
				case <-time.After(time.Second):
					t.Fatal("controlled five-entry write did not overflow the two-entry tail")
				}
				waitSnapshotTailStopped(t, tail)
				if !errors.Is(tail.subscription.Err(), mutationlog.ErrTailQueueFull) || !errors.Is(tail.subscription.Err(), mutationlog.ErrSubscriptionLost) {
					t.Fatal("controlled queue overflow lost either terminal contract")
				}
				f.replica.snapshotTails.mu.Lock()
				first := tail.terminal
				f.replica.snapshotTails.mu.Unlock()
				if first != "log_boundary" {
					t.Fatalf("lost replay did not retain boundary category with %q", first)
				}
				assertSnapshotTailEmpty(t, f.replica)
			}
			if phase == "before footer" {
				responseHeader := make(http.Header)
				recorder := &replicationSnapshotRecorder{onSend: func(frame *pb.SnapshotResponse) error {
					if frame.GetHeader() != nil {
						m := f.replica.snapshotTails
						m.mu.Lock()
						tail = m.active[responseHeader.Get(SnapshotTailHeader)]
						m.mu.Unlock()
						if tail == nil {
							return errors.New("Snapshot header has no reservation owner")
						}
						overflow()
					}
					return nil
				}}
				err := f.replica.Snapshot(WithReplicationTailMetadata(original, []string{"reserve"}, responseHeader), &pb.SnapshotRequest{}, recorder)
				assertSnapshotTailDiagnostic(t, err, "finish", "subscription_terminal", "log_boundary")
				if len(recorder.frames) == 0 || recorder.frames[len(recorder.frames)-1].GetFooter() == nil {
					t.Fatal("fixture did not reach the finish guard after sending its footer")
				}
				if tail.ready {
					t.Fatal("overflowed Snapshot became claimable")
				}
				assertSnapshotTailEmpty(t, f.replica)
				return
			}
			token, header, owner := reserveSnapshotTail(t, f.replica, original)
			tail = owner
			cancelOriginal() // Successful native handler return cancels this RPC.
			overflow()
			if !tail.ready {
				t.Fatal("fixture did not successfully finish before losing its tail")
			}
			current, _ := auth.context(t, 0, 5*time.Second)
			recorder := &replicationSubscribeRecorder{frames: make(chan *pb.SubscribeResponse, 1)}
			err := f.replica.Subscribe(WithReplicationTailMetadata(current, []string{token}, nil), snapshotTailSubscribeRequest(header), recorder)
			assertSnapshotTailDiagnostic(t, err, "claim", "reservation_missing", "unknown")
			if len(recorder.frames) != 0 || strings.Contains(err.Error(), token) {
				t.Fatal("failed claim emitted data or included a token in its diagnostic")
			}
			assertSnapshotTailEmpty(t, f.replica)
		})
	}
}

func TestSnapshotTailDiagnosticClaimGuardsRemainFailClosed(t *testing.T) {
	for _, guard := range []string{"peer_mismatch", "local_cursor_mismatch", "origin_cursor_mismatch", "generation_mismatch", "not_ready"} {
		t.Run(guard, func(t *testing.T) {
			f := newSnapshotTailServiceFixture(t, 16)
			auth := newSnapshotTailAdmissionFixture(t, 2)
			original, _ := auth.context(t, 0, 5*time.Second)
			token, header, tail := reserveSnapshotTail(t, f.replica, original)
			current, _ := auth.context(t, 0, 5*time.Second)
			req := snapshotTailSubscribeRequest(header)
			fault := tail.fault
			switch guard {
			case "peer_mismatch":
				current, _ = auth.context(t, 1, 5*time.Second)
			case "local_cursor_mismatch":
				req.FromLocalSeq++
			case "origin_cursor_mismatch":
				req.FromSeqPerOrigin = nil
			case "generation_mismatch":
				fault = make(chan struct{})
			case "not_ready":
				f.replica.snapshotTails.mu.Lock()
				tail.ready = false
				f.replica.snapshotTails.mu.Unlock()
			}
			cursor, err := parseOriginCursor(req.GetFromSeqPerOrigin())
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := f.replica.claimSnapshotTail(current, token, req, cursor, fault)
			if claimed != nil {
				claimed.Close()
				t.Fatal("invalid claim took ownership")
			}
			assertSnapshotTailDiagnostic(t, err, "claim", guard, "none")
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "spiffe:") {
				t.Fatal("claim diagnostic included private token or workload evidence")
			}
			tail.Close()
			assertSnapshotTailEmpty(t, f.replica)
		})
	}
}

func TestSnapshotTailDiagnosticUsesOnlyKnownTerminalCategories(t *testing.T) {
	for _, row := range []struct {
		err    error
		reason snapshotTailReason
	}{
		{mutationlog.ErrTailQueueFull, "queue_full"},
		{mutationlog.ErrSubscriptionLost, "subscription_lost"},
		{mutationlog.ErrClosed, "log_closed"},
		{mutationlog.ErrGapped, "log_boundary"},
		{mutationlog.ErrWALIndeterminate, "wal_indeterminate"},
		{mutationlog.ErrPublicationInterrupted, "publication_interrupted"},
		{context.Canceled, "subscription_canceled"},
		{errors.New("private-error-detail-must-not-escape"), "subscription_other"},
	} {
		if got := snapshotTailCoreReason(row.err); got != row.reason {
			t.Fatalf("terminal category = %q, want %q", got, row.reason)
		}
		err := snapshotTailError("finish", "subscription_terminal", snapshotTailCoreReason(row.err))
		assertSnapshotTailDiagnostic(t, err, "finish", "subscription_terminal", row.reason)
		if strings.Contains(err.Error(), "private-error-detail") {
			t.Fatal("raw core error escaped fixed diagnostic categories")
		}
	}
}
