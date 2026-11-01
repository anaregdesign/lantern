package dataruntime

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"google.golang.org/protobuf/proto"
)

// Replay uses the exact production owner cap. Only the retained ring varies:
// these unit fixtures neither enlarge the native queue nor acquire network time.
func newSnapshotTailReplayFixture(t *testing.T, capacity int) snapshotTailServiceFixture {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: capacity, SubscriberBuffer: maxSnapshotTailEntries})
	clock := hlc.New(hlc.NodeID{0xDB}, hlc.Options{})
	primary := NewExecutor(cache).WithReplication(log, clock, nil)
	replica := NewReplication(log, cache, clock).WithOriginStates(primary)
	t.Cleanup(func() { _ = log.Close() })
	t.Cleanup(replica.CloseSnapshotTails)
	fixture := snapshotTailServiceFixture{primary, replica, log}
	appendSnapshotTailReplayWrites(t, fixture, 8)
	return fixture
}

func appendSnapshotTailReplayWrites(t *testing.T, fixture snapshotTailServiceFixture, count int) {
	t.Helper()
	for index := range count {
		if _, err := fixture.primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
			Key: "replay-value", Value: &pb.Vertex_String_{String_: fmt.Sprintf("write-%d", index)},
		}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func requireSnapshotTailReplayStandby(t *testing.T, tail *snapshotTail) {
	t.Helper()
	if capacity := cap(tail.subscription.Entries()); capacity != maxSnapshotTailEntries {
		t.Fatalf("owned native queue capacity=%d, want unchanged cap %d", capacity, maxSnapshotTailEntries)
	}
	select {
	case <-tail.subscription.Done():
	case <-time.After(time.Second):
		t.Fatal("exact 512-entry unclaimed queue did not overflow")
	}
	if !errors.Is(tail.subscription.Err(), mutationlog.ErrTailQueueFull) {
		t.Fatalf("unclaimed terminal = %v, want authoritative queue overflow", tail.subscription.Err())
	}
	if err := tail.prepareRingReplay(); err != nil {
		t.Fatalf("unclaimed queue could not enter ring replay standby: %v", err)
	}
	m := tail.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !tail.ringReplay || tail.closed || tail.claimed || tail.deliverySubscription != nil ||
		tail.bytes != 0 || tail.entries != 0 || m.totalBytes != 0 || m.totalEntries != 0 || m.active[tail.token] != tail {
		t.Fatalf("standby retained a queue or lost ownership: replay=%t closed=%t claimed=%t replacement=%t owners=%d entries=%d bytes=%d",
			tail.ringReplay, tail.closed, tail.claimed, tail.deliverySubscription != nil, len(m.active), m.totalEntries, m.totalBytes)
	}
}

func snapshotTailReplayClaim(t *testing.T, fixture snapshotTailServiceFixture, ctx context.Context, token string, header *pb.SnapshotHeader, fault <-chan struct{}) (*snapshotTail, error) {
	t.Helper()
	request := snapshotTailSubscribeRequest(header)
	cursor, err := parseOriginCursor(request.GetFromSeqPerOrigin())
	if err != nil {
		t.Fatal(err)
	}
	return fixture.replica.claimSnapshotTail(ctx, token, request, cursor, fault)
}

func TestSnapshotTailReplayFinishesAndClaimsFromOriginalCut(t *testing.T) {
	fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
	auth := newSnapshotTailAdmissionFixture(t, 1)
	original, cancelOriginal := auth.context(t, 0, 10*time.Second)
	deadline, _ := original.Deadline()
	responseHeader := make(http.Header)
	var tail *snapshotTail
	recorder := &replicationSnapshotRecorder{onSend: func(frame *pb.SnapshotResponse) error {
		if frame.GetHeader() == nil {
			return nil
		}
		m := fixture.replica.snapshotTails
		m.mu.Lock()
		tail = m.active[responseHeader.Get(SnapshotTailHeader)]
		m.mu.Unlock()
		if tail == nil {
			return errors.New("Snapshot header has no original tail owner")
		}
		appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
		requireSnapshotTailReplayStandby(t, tail)
		return nil
	}}
	if err := fixture.replica.Snapshot(WithReplicationTailMetadata(original, []string{"reserve"}, responseHeader),
		&pb.SnapshotRequest{RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1}, recorder); err != nil {
		t.Fatalf("retained original cut did not finish after queue overflow: %v", err)
	}
	defer tail.Close()
	header := proto.Clone(recorder.frames[0].GetHeader()).(*pb.SnapshotHeader)
	if header.GetCutoffLocalSeq() != 8 || recorder.frames[len(recorder.frames)-1].GetFooter() == nil {
		t.Fatal("overflow changed the captured cut or Snapshot completion")
	}
	wantOrigins := maps.Clone(tail.origins)
	cancelOriginal() // Successful request teardown must not discard the original owner.
	appendSnapshotTailReplayWrites(t, fixture, 2)
	requireSnapshotTailReplayStandby(t, tail)
	if tail.local != 8 || tail.deadline != deadline || !maps.Equal(tail.origins, wantOrigins) {
		t.Fatal("standby rebased its cut, origins, or original deadline")
	}
	current, cancelCurrent := auth.context(t, 0, 10*time.Second)
	duplicate := &replicationSnapshotRecorder{}
	if err := fixture.replica.Snapshot(WithReplicationTailMetadata(current, []string{"reserve"}, make(http.Header)), &pb.SnapshotRequest{}, duplicate); connect.CodeOf(err) != connect.CodeResourceExhausted || len(duplicate.frames) != 0 {
		t.Fatalf("standby released its original peer slot: err=%v frames=%d", err, len(duplicate.frames))
	}
	sender := &snapshotTailClaimSender{ctx: current, ready: make(chan struct{}), frames: make(chan *pb.SubscribeResponse, 1)}
	done := make(chan error, 1)
	go func() {
		done <- fixture.replica.Subscribe(WithReplicationTailMetadata(current, []string{tail.token}, nil), snapshotTailSubscribeRequest(header), sender)
	}()
	defer func() {
		cancelCurrent()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("claimed ring replay handler did not join")
		}
	}()
	lastReplay := uint64(8 + maxSnapshotTailEntries + 3)
	for want := uint64(9); want <= lastReplay; want++ {
		select {
		case frame := <-sender.frames:
			if got := frame.GetMutation().GetSeq(); got != want {
				t.Fatalf("ring replay origin sequence=%d, want %d", got, want)
			}
		case err := <-done:
			t.Fatalf("ring replay ended before original sequence %d: %v", want, err)
		case <-time.After(time.Second):
			t.Fatalf("ring replay omitted original sequence %d", want)
		}
	}
	appendSnapshotTailReplayWrites(t, fixture, 1)
	select {
	case frame := <-sender.frames:
		if frame.GetMutation().GetSeq() != lastReplay+1 {
			t.Fatal("ring replay did not join the contiguous live tail")
		}
	case <-time.After(time.Second):
		t.Fatal("ring replay did not switch atomically to the live tail")
	}
	cancelCurrent()
	select {
	case <-tail.released:
	case <-time.After(time.Second):
		t.Fatal("ring replay handler retained credits after exit")
	}
	waitSnapshotTailStopped(t, tail)
	assertSnapshotTailEmpty(t, fixture.replica)
}

func TestSnapshotTailReplayRejectsEvictedOriginalCut(t *testing.T) {
	t.Run("before Snapshot finish", func(t *testing.T) {
		fixture := newSnapshotTailReplayFixture(t, maxSnapshotTailEntries)
		auth := newSnapshotTailAdmissionFixture(t, 1)
		original, _ := auth.context(t, 0, 10*time.Second)
		responseHeader := make(http.Header)
		var tail *snapshotTail
		recorder := &replicationSnapshotRecorder{onSend: func(frame *pb.SnapshotResponse) error {
			if frame.GetHeader() == nil {
				return nil
			}
			m := fixture.replica.snapshotTails
			m.mu.Lock()
			tail = m.active[responseHeader.Get(SnapshotTailHeader)]
			m.mu.Unlock()
			appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
			requireSnapshotTailReplayStandby(t, tail)
			return nil
		}}
		err := fixture.replica.Snapshot(WithReplicationTailMetadata(original, []string{"reserve"}, responseHeader), &pb.SnapshotRequest{}, recorder)
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "replay_unavailable") {
			t.Fatalf("evicted original cut finished Snapshot: %v", err)
		}
		if first, _ := fixture.log.FirstSeq(); first != 10 {
			t.Fatalf("original cut was not evicted: retained first=%d, want 10", first)
		}
		waitSnapshotTailStopped(t, tail)
		assertSnapshotTailEmpty(t, fixture.replica)
	})
	t.Run("between finish and claim", func(t *testing.T) {
		fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
		auth := newSnapshotTailAdmissionFixture(t, 1)
		original, cancel := auth.context(t, 0, 10*time.Second)
		token, header, tail := reserveSnapshotTail(t, fixture.replica, original)
		defer tail.Close()
		cancel()
		appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
		requireSnapshotTailReplayStandby(t, tail)
		appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries)
		if first, _ := fixture.log.FirstSeq(); first != 10 {
			t.Fatalf("original cut was not evicted: retained first=%d, want 10", first)
		}
		current, _ := auth.context(t, 0, 10*time.Second)
		claimed, err := snapshotTailReplayClaim(t, fixture, current, token, header, tail.fault)
		if claimed != nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "replay_unavailable") {
			t.Fatalf("claim reopened an evicted original cut: claimed=%t err=%v", claimed != nil, err)
		}
		tail.Close()
		assertSnapshotTailEmpty(t, fixture.replica)
	})
}

func TestSnapshotTailReplayPreservesPeerCursorAndGenerationGuards(t *testing.T) {
	for _, name := range []string{"other member", "other store", "local cut", "origin cut", "namespace", "generation", "expired original admission", "revoked membership"} {
		t.Run(name, func(t *testing.T) {
			fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
			auth := newSnapshotTailAdmissionFixture(t, 2)
			original, cancel := auth.context(t, 0, 10*time.Second)
			token, header, tail := reserveSnapshotTail(t, fixture.replica, original)
			defer tail.Close()
			cancel()
			appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
			requireSnapshotTailReplayStandby(t, tail)
			current, _ := auth.context(t, 0, 10*time.Second)
			request := snapshotTailSubscribeRequest(header)
			fault := tail.fault
			switch name {
			case "other member":
				current, _ = auth.context(t, 1, 10*time.Second)
			case "other store":
				other := newSnapshotTailAdmissionFixture(t, 2)
				current, _ = other.context(t, 0, 10*time.Second)
			case "local cut":
				request.FromLocalSeq++
			case "origin cut":
				request.FromSeqPerOrigin = nil
			case "namespace":
				request.NamespaceFormat = "foreign-domain"
			case "generation":
				fault = make(chan struct{})
			case "expired original admission":
				auth.mu.Lock()
				auth.now = auth.now.Add(31 * time.Second)
				auth.mu.Unlock()
				current, _ = auth.context(t, 0, 10*time.Second)
			case "revoked membership":
				auth.manifest.Version++
				auth.manifest.Members = auth.manifest.Members[1:]
				raw, err := peerauth.SignManifest(auth.manifest, auth.key)
				if err != nil {
					t.Fatal(err)
				}
				if err := auth.store.Apply(raw); err != nil {
					t.Fatal(err)
				}
			}
			if name == "namespace" {
				recorder := &replicationSubscribeRecorder{frames: make(chan *pb.SubscribeResponse, 1)}
				if err := fixture.replica.Subscribe(WithReplicationTailMetadata(current, []string{token}, nil), request, recorder); err == nil || len(recorder.frames) != 0 {
					t.Fatalf("foreign namespace claimed standby: err=%v frames=%d", err, len(recorder.frames))
				}
			} else {
				cursor, err := parseOriginCursor(request.GetFromSeqPerOrigin())
				if err != nil {
					t.Fatal(err)
				}
				claimed, err := fixture.replica.claimSnapshotTail(current, token, request, cursor, fault)
				if claimed != nil || err == nil {
					t.Fatalf("invalid standby claim acquired ownership: claimed=%t err=%v", claimed != nil, err)
				}
			}
			tail.Close()
			assertSnapshotTailEmpty(t, fixture.replica)
		})
	}
}

func TestSnapshotTailReplayDoesNotRenewOriginalDeadline(t *testing.T) {
	fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
	auth := newSnapshotTailAdmissionFixture(t, 1)
	original, _ := auth.context(t, 0, 2*time.Second)
	token, header, tail := reserveSnapshotTail(t, fixture.replica, original)
	appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
	requireSnapshotTailReplayStandby(t, tail)
	select {
	case <-original.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("original finite Snapshot deadline did not expire")
	}
	current, _ := auth.context(t, 0, 10*time.Second)
	claimed, err := snapshotTailReplayClaim(t, fixture, current, token, header, tail.fault)
	if claimed != nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("fresh admission renewed expired standby: claimed=%t err=%v", claimed != nil, err)
	}
	waitSnapshotTailStopped(t, tail)
	assertSnapshotTailEmpty(t, fixture.replica)
}

func TestSnapshotTailReplayCannotRecoverClaimedQueueOverflow(t *testing.T) {
	fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
	auth := newSnapshotTailAdmissionFixture(t, 1)
	original, _ := auth.context(t, 0, 10*time.Second)
	token, header, tail := reserveSnapshotTail(t, fixture.replica, original)
	defer tail.Close()
	current, _ := auth.context(t, 0, 10*time.Second)
	claimed, err := snapshotTailReplayClaim(t, fixture, current, token, header, tail.fault)
	if err != nil || claimed != tail {
		t.Fatalf("initial queue claim=%t err=%v", claimed == tail, err)
	}
	appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
	select {
	case <-tail.subscription.Done():
	case <-time.After(time.Second):
		t.Fatal("claimed queue did not overflow")
	}
	if err := tail.prepareRingReplay(); err == nil {
		t.Fatal("claimed overflow became replay-eligible")
	}
	waitSnapshotTailStopped(t, tail)
	tail.manager.mu.Lock()
	if tail.ringReplay || tail.deliverySubscription != nil || !tail.closed || tail.manager.active[token] != tail {
		tail.manager.mu.Unlock()
		t.Fatal("claimed overflow changed stream or released its unacknowledged owner")
	}
	tail.manager.mu.Unlock()
	tail.Close()
	assertSnapshotTailEmpty(t, fixture.replica)
}

func TestSnapshotTailReplayIgnoresStaleOriginalDone(t *testing.T) {
	fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
	auth := newSnapshotTailAdmissionFixture(t, 1)
	original, _ := auth.context(t, 0, 10*time.Second)
	token, header, tail := reserveSnapshotTail(t, fixture.replica, original)
	defer tail.Close()
	appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
	requireSnapshotTailReplayStandby(t, tail)
	old := tail.subscription
	current, _ := auth.context(t, 0, 10*time.Second)
	claimed, err := snapshotTailReplayClaim(t, fixture, current, token, header, tail.fault)
	if err != nil || claimed != tail {
		t.Fatalf("ring replay claim=%t err=%v", claimed == tail, err)
	}
	replacement := tail.currentSubscription()
	if replacement == old || replacement == nil {
		t.Fatal("replay claim did not publish its new owned stream")
	}
	if !tail.observeSubscriptionEnd(old) {
		t.Fatal("stale original Done was treated as replacement failure")
	}
	if tail.closeUnclaimedResources("admission_rejected") {
		t.Fatal("stale unclaimed admission decision closed a claimed replay")
	}
	tail.manager.mu.Lock()
	closed := tail.closed
	tail.manager.mu.Unlock()
	if closed || replacement.Err() != nil {
		t.Fatalf("stale original Done invalidated healthy replay: closed=%t err=%v", closed, replacement.Err())
	}
	tail.Close()
	assertSnapshotTailEmpty(t, fixture.replica)
}

func TestSnapshotTailReplayStandbyShutdownReleasesOwnership(t *testing.T) {
	fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
	auth := newSnapshotTailAdmissionFixture(t, 1)
	original, cancel := auth.context(t, 0, 10*time.Second)
	_, _, tail := reserveSnapshotTail(t, fixture.replica, original)
	cancel()
	appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
	requireSnapshotTailReplayStandby(t, tail)
	fixture.replica.CloseSnapshotTails()
	waitSnapshotTailStopped(t, tail)
	select {
	case <-tail.released:
	default:
		t.Fatal("shutdown retained an unclaimed standby owner")
	}
	assertSnapshotTailEmpty(t, fixture.replica)
}

// The final elapsed-budget observation runs after Admission has checked its
// original member, certificate interval and request context. Pausing there
// guarantees that the initial admission succeeds, regardless of the later
// withdrawal used below; this is controlled native-unit evidence only.
type snapshotTailFinishObservation struct {
	now      time.Time
	armed    atomic.Bool
	observed chan struct{}
	release  chan struct{}
}

func (o *snapshotTailFinishObservation) Bounds() (time.Time, time.Time) { return o.now, o.now }
func (o *snapshotTailFinishObservation) ElapsedSince(peerauth.TimeObservation) (time.Duration, error) {
	if o.armed.CompareAndSwap(true, false) {
		close(o.observed)
		<-o.release
	}
	return 0, nil
}

func TestSnapshotTailReplayFinishRechecksAdmissionAfterCoreWait(t *testing.T) {
	for _, withdrawal := range []string{"request canceled", "original member revoked"} {
		t.Run(withdrawal, func(t *testing.T) {
			fixture := newSnapshotTailReplayFixture(t, 2*maxSnapshotTailEntries)
			auth := newSnapshotTailAdmissionFixture(t, 2)
			observation := &snapshotTailFinishObservation{now: auth.now, observed: make(chan struct{}), release: make(chan struct{})}
			raw, err := peerauth.SignManifest(auth.manifest, auth.key)
			if err != nil {
				t.Fatal(err)
			}
			store, err := peerauth.CreateStore(peerauth.StoreOptions{
				Path: filepath.Join(t.TempDir(), "finish-membership"), Key: auth.key.Public().(ed25519.PublicKey),
				Domain: auth.manifest.Domain, Time: func() (peerauth.TimeObservation, error) { return observation, nil },
			}, raw)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			admission, err := store.Admit(auth.states[0], "")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(peerauth.WithAdmission(t.Context(), admission), 10*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			fault, _ := fixture.primary.publicationStatus()
			// Isolate finish from the background watcher: otherwise that watcher
			// could close the owner first and conceal a stale-admission success.
			// The native Admission and bounded core subscription remain real.
			tail := &snapshotTail{manager: fixture.replica.snapshotTails, token: strings.Repeat("cd", 32),
				admission: admission, deadline: deadline, local: 8, fault: fault,
				opened: make(chan struct{}), stop: make(chan struct{}), stopped: make(chan struct{}),
				released: make(chan struct{}), updated: make(chan struct{}, 1)}
			tail.subscription, err = fixture.log.SubscribeTail(9, maxSnapshotTailEntries, tail.project)
			if err != nil {
				t.Fatal(err)
			}
			close(tail.opened)
			close(tail.stopped) // This isolated owner deliberately has no watcher.
			tail.manager.mu.Lock()
			tail.manager.active[tail.token] = tail
			tail.manager.mu.Unlock()
			appendSnapshotTailReplayWrites(t, fixture, maxSnapshotTailEntries+1)
			select {
			case <-tail.subscription.Done():
			case <-time.After(time.Second):
				tail.Close()
				t.Fatal("finish fixture did not reach original queue overflow")
			}
			if !errors.Is(tail.subscription.Err(), mutationlog.ErrTailQueueFull) {
				tail.Close()
				t.Fatal("finish fixture lost its queue for a different reason")
			}
			tail.transitionMu.Lock()
			var releaseOnce, unlockOnce sync.Once
			releaseObservation := func() { releaseOnce.Do(func() { close(observation.release) }) }
			unlock := func() { unlockOnce.Do(tail.transitionMu.Unlock) }
			done := make(chan error, 1)
			defer func() {
				releaseObservation()
				unlock()
				tail.Close()
			}()
			observation.armed.Store(true)
			go func() { done <- tail.finish(ctx) }()
			select {
			case <-observation.observed:
			case <-time.After(time.Second):
				t.Fatal("finish did not pass its first native admission checks")
			}
			releaseObservation()
			if withdrawal == "request canceled" {
				cancel()
			} else {
				auth.manifest.Version++
				auth.manifest.Members = auth.manifest.Members[1:]
				raw, err = peerauth.SignManifest(auth.manifest, auth.key)
				if err != nil {
					t.Fatal(err)
				}
				// Apply acquires the same Store lock as the first Check, proving
				// that check's result is fixed before membership is withdrawn.
				if err := store.Apply(raw); err != nil {
					t.Fatal(err)
				}
			}
			unlock()
			select {
			case err := <-done:
				if connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("finish accepted authority withdrawn during its core wait: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("finish did not join after the controlled core wait")
			}
			tail.manager.mu.Lock()
			ready, replay, bytes, entries := tail.ready, tail.ringReplay, tail.bytes, tail.entries
			tail.manager.mu.Unlock()
			if ready || !replay || bytes != 0 || entries != 0 {
				t.Fatalf("withdrawn finish published readiness or skipped retirement: ready=%t replay=%t entries=%d bytes=%d", ready, replay, entries, bytes)
			}
			tail.Close()
			assertSnapshotTailEmpty(t, fixture.replica)
		})
	}
}

// Unlike the tiny-buffer diagnostic test, these controls use the production
// 512-entry queue and join the real watcher while Snapshot is held at its
// header. The original core error separates pure ring eviction from an actual
// boundary commit even though their later finish diagnostics are identical.
func TestSnapshotTailReplayFinishDiagnosticConflatesEvictionAndCommittedBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		capacity    int
		boundary    bool
		unavailable bool
		first, last uint64
	}{
		{name: "retained replay", capacity: 4 * maxSnapshotTailEntries, first: 1, last: 1033},
		{name: "pure eviction after queue overflow", capacity: 2 * maxSnapshotTailEntries, unavailable: true, first: 10, last: 1033},
		{name: "committed boundary with empty queue", capacity: 2 * maxSnapshotTailEntries, boundary: true, unavailable: true, first: 10, last: 9},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newSnapshotTailReplayFixture(t, scenario.capacity)
			auth := newSnapshotTailAdmissionFixture(t, 1)
			original, cancel := auth.context(t, 0, 10*time.Second)
			defer cancel()
			deadline, _ := original.Deadline()
			responseHeader := make(http.Header)
			var tail *snapshotTail
			defer func() {
				if tail != nil {
					tail.Close()
				}
			}()
			recorder := &replicationSnapshotRecorder{onSend: func(frame *pb.SnapshotResponse) error {
				header := frame.GetHeader()
				if header == nil {
					return nil
				}
				m := fixture.replica.snapshotTails
				m.mu.Lock()
				tail = m.active[responseHeader.Get(SnapshotTailHeader)]
				m.mu.Unlock()
				if tail == nil || header.GetCutoffLocalSeq() != 8 || cap(tail.subscription.Entries()) != maxSnapshotTailEntries {
					t.Fatal("Snapshot did not capture cut 8 with the production 512-entry queue")
				}
				origins := maps.Clone(tail.origins)
				admission := tail.admission
				writes := 0
				appendBounded := func(count int) {
					t.Helper()
					for range count {
						if _, err := fixture.primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
							Key: "replay-value", Value: &pb.Vertex_String_{String_: fmt.Sprintf("diagnostic-write-%d", writes)},
						}}}); err != nil {
							t.Fatal(err)
						}
						writes++
						m.mu.Lock()
						entries, bytes := tail.entries, tail.bytes
						totalEntries, totalBytes := m.totalEntries, m.totalBytes
						m.mu.Unlock()
						if entries < 0 || entries > maxSnapshotTailEntries || bytes < 0 || bytes > maxSnapshotTailBytes ||
							totalEntries != entries || totalBytes != bytes || totalEntries > maxSnapshotTotalEntries || totalBytes > maxSnapshotTotalBytes {
							t.Fatalf("tail exceeded or lost its credits: owner=%d/%d total=%d/%d", entries, bytes, totalEntries, totalBytes)
						}
					}
				}
				if scenario.boundary {
					if len(tail.subscription.Entries()) != 0 || tail.subscription.Err() != nil {
						t.Fatal("boundary control did not start with an empty healthy queue")
					}
					// Invoke the genuine core invalidation API; this controlled
					// marker proves its terminal cause, not receipt-baseline validity.
					marker, err := fixture.log.CommitBoundaryWithPostRingPublication("diagnostic boundary", fixture.primary.clock.Now(), nil)
					if err != nil || marker.Seq != 9 || fixture.log.Len() != 0 {
						t.Fatalf("controlled boundary commit failed: seq=%d retained=%d err=%v", marker.Seq, fixture.log.Len(), err)
					}
					if !errors.Is(tail.subscription.Err(), mutationlog.ErrGapped) || errors.Is(tail.subscription.Err(), mutationlog.ErrTailQueueFull) {
						t.Fatal("empty-queue boundary did not report its distinct ErrGapped cause")
					}
				} else {
					appendBounded(maxSnapshotTailEntries + 1)
					requireSnapshotTailReplayStandby(t, tail)
					appendBounded(maxSnapshotTailEntries)
					if !errors.Is(tail.subscription.Err(), mutationlog.ErrTailQueueFull) || errors.Is(tail.subscription.Err(), mutationlog.ErrGapped) {
						t.Fatal("ordinary writes changed the original queue-full cause into a boundary")
					}
				}
				first, _ := fixture.log.FirstSeq()
				last, _ := fixture.log.LastSeq()
				if first != scenario.first || last != scenario.last || tail.local != 8 || tail.deadline != deadline ||
					tail.admission != admission || !maps.Equal(tail.origins, origins) {
					t.Fatalf("controlled history or original owner changed: first=%d last=%d cut=%d", first, last, tail.local)
				}
				if scenario.unavailable {
					// Hold Send until the actual watcher has observed either
					// eviction or boundary and completed owner cleanup. Only then
					// does Snapshot send its footer and reach the finish guard.
					waitSnapshotTailStopped(t, tail)
					assertSnapshotTailEmpty(t, fixture.replica)
				} else {
					requireSnapshotTailReplayStandby(t, tail)
					if err := tail.subscription.ValidateReplay(9); err != nil {
						t.Fatalf("retained control lost the original replay cut: %v", err)
					}
				}
				return nil
			}}
			err := fixture.replica.Snapshot(WithReplicationTailMetadata(original, []string{"reserve"}, responseHeader),
				&pb.SnapshotRequest{RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1}, recorder)
			if len(recorder.frames) == 0 || recorder.frames[len(recorder.frames)-1].GetFooter() == nil {
				t.Fatal("controlled Snapshot did not send its footer before the finish result")
			}
			if scenario.unavailable {
				assertSnapshotTailDiagnostic(t, err, "finish", "subscription_terminal", "log_boundary")
				if tail.ready {
					t.Fatal("invalidated Snapshot became claimable")
				}
				assertSnapshotTailEmpty(t, fixture.replica)
				return
			}
			tail.manager.mu.Lock()
			ready, closed, claimed := tail.ready, tail.closed, tail.claimed
			tail.manager.mu.Unlock()
			if err != nil || !ready || closed || claimed || tail.local != 8 || tail.deadline != deadline {
				t.Fatalf("retained control did not finish with its original owner: err=%v", err)
			}
			requireSnapshotTailReplayStandby(t, tail)
			tail.Close()
			assertSnapshotTailEmpty(t, fixture.replica)
		})
	}
}
