package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
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
