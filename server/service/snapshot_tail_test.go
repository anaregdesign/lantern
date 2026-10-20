package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
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

// This fixture exercises real signed membership and Admission checks without
// claiming transport/TLS qualification. Real-wire coverage lives in integration.
type snapshotTailAdmissionFixture struct {
	mu       sync.Mutex
	now      time.Time
	manifest peerauth.Manifest
	key      ed25519.PrivateKey
	store    *peerauth.Store
	states   []*tls.ConnectionState
}

func newSnapshotTailAdmissionFixture(t *testing.T, members int) *snapshotTailAdmissionFixture {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &snapshotTailAdmissionFixture{now: time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC), key: key}
	f.manifest = peerauth.Manifest{Version: 1, Domain: peerauth.Domain{
		Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: [32]byte{2},
	}, IssuedAt: f.now.Add(-time.Second), ExpiresAt: f.now.Add(5 * time.Minute)}
	for i := range members {
		identity := fmt.Sprintf("spiffe://tail.test/peer-%02d", i)
		uri, err := url.Parse(identity)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte(identity),
			NotBefore: f.now.Add(-time.Hour), NotAfter: f.now.Add(time.Hour)}
		f.manifest.Members = append(f.manifest.Members, peerauth.Member{ID: [16]byte{byte(i + 1)},
			Identity: identity, SPKI: sha256.Sum256(leaf.RawSubjectPublicKeyInfo), Origin: fmt.Sprintf("https://localhost:%d", 17000+i)})
		f.states = append(f.states, &tls.ConnectionState{Version: tls.VersionTLS13,
			PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}})
	}
	raw, err := peerauth.SignManifest(f.manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	f.store, err = peerauth.CreateStore(peerauth.StoreOptions{Path: filepath.Join(t.TempDir(), "membership"),
		Key: public, Domain: f.manifest.Domain, Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.now
		}}, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	return f
}

func (f *snapshotTailAdmissionFixture) context(t *testing.T, member int, lifetime time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	a, err := f.store.Admit(f.states[member], "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(peerauth.WithAdmission(t.Context(), a), lifetime)
	t.Cleanup(cancel)
	return ctx, cancel
}

type snapshotTailServiceFixture struct {
	primary *LanternService
	replica *LanternReplicationService
	log     *mutationlog.Log
}

func newSnapshotTailServiceFixture(t *testing.T, buffer int) snapshotTailServiceFixture {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 4, SubscriberBuffer: buffer})
	clock := hlc.New(hlc.NodeID{0xDA}, hlc.Options{})
	primary := NewLanternService(cache).WithReplication(log, clock, nil)
	replica := NewLanternReplicationService(log, cache, clock).WithOriginStates(primary)
	t.Cleanup(func() { _ = log.Close() })
	t.Cleanup(replica.CloseSnapshotTails)
	for i := range 8 {
		if _, err := primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
			Key: fmt.Sprintf("seed-%d", i), Value: &pb.Vertex_String_{String_: "seed"},
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	return snapshotTailServiceFixture{primary, replica, log}
}

func reserveSnapshotTail(t *testing.T, replica *LanternReplicationService, ctx context.Context) (string, *pb.SnapshotHeader, *snapshotTail) {
	t.Helper()
	header := make(http.Header)
	recorder := &replicationSnapshotRecorder{}
	if err := replica.Snapshot(WithReplicationTailMetadata(ctx, []string{"reserve"}, header),
		&pb.SnapshotRequest{RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1}, recorder); err != nil {
		t.Fatal(err)
	}
	values := header.Values(SnapshotTailHeader)
	if len(values) != 1 || len(values[0]) != 64 {
		t.Fatalf("Snapshot tail token metadata = %q", values)
	}
	if _, err := hex.DecodeString(values[0]); err != nil {
		t.Fatal(err)
	}
	if len(recorder.frames) == 0 || recorder.frames[0].GetHeader() == nil {
		t.Fatal("Snapshot omitted header")
	}
	replica.snapshotTails.mu.Lock()
	tail := replica.snapshotTails.active[values[0]]
	replica.snapshotTails.mu.Unlock()
	if tail == nil {
		t.Fatal("successful Snapshot did not retain its exact tail")
	}
	return values[0], recorder.frames[0].GetHeader(), tail
}

func snapshotTailSubscribeRequest(header *pb.SnapshotHeader) *pb.SubscribeRequest {
	req := &pb.SubscribeRequest{FromLocalSeq: header.GetCutoffLocalSeq() + 1, FromSeqPerOrigin: make(map[string]uint64),
		AcceptReceiptEnvelopes: true, AcceptGraphMutations: true}
	for origin, cutoff := range header.GetCutoffSeqPerOrigin() {
		req.FromSeqPerOrigin[origin] = cutoff + 1
	}
	return req
}

func assertSnapshotTailEmpty(t *testing.T, replica *LanternReplicationService) {
	t.Helper()
	m := replica.snapshotTails
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.active) != 0 || m.totalEntries != 0 || m.totalBytes != 0 {
		t.Fatalf("tail storage retained owners=%d entries=%d bytes=%d", len(m.active), m.totalEntries, m.totalBytes)
	}
}

func waitSnapshotTailStopped(t *testing.T, tail *snapshotTail) {
	t.Helper()
	select {
	case <-tail.stopped:
	case <-time.After(time.Second):
		t.Fatal("Snapshot tail owner did not join")
	}
}

type snapshotTailClaimSender struct {
	ctx    context.Context
	ready  chan struct{}
	frames chan *pb.SubscribeResponse
	once   sync.Once
}

func (s *snapshotTailClaimSender) FlushHeaders() error {
	s.once.Do(func() { close(s.ready) })
	return nil
}

func (s *snapshotTailClaimSender) Send(frame *pb.SubscribeResponse) error {
	select {
	case s.frames <- frame:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

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
		}{
			{"tail bytes", maxSnapshotTailBytes - size + 1, 0, 0, 0},
			{"global bytes", 0, 0, maxSnapshotTotalBytes - size + 1, 0},
			{"tail entries", 0, maxSnapshotTailEntries, 0, 0},
			{"global entries", 0, 0, 0, maxSnapshotTotalEntries},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tail.bytes, tail.entries = tc.bytes, tc.entries
				m.totalBytes, m.totalEntries = tc.totalBytes, tc.totalEntries
				entry, err := tail.project(mutationlog.Entry{Seq: 1, Op: mutation})
				if !errors.Is(err, mutationlog.ErrSubscriptionLost) || entry.Op != nil {
					t.Fatalf("over-budget projection = %+v, %v", entry, err)
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
