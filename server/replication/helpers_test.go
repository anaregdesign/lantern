package replication

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func snapshotScratchTestConfig(t *testing.T, maxBytes uint64) GraphSnapshotSpoolConfig {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return GraphSnapshotSpoolConfig{Directory: directory, MaxDiskBytes: maxBytes, MaxFrameBytes: int(min(maxBytes, 32<<20))}
}

func snapshotScratchTestOwner(t *testing.T, ctx context.Context, config GraphSnapshotSpoolConfig) *snapshotScratch {
	t.Helper()
	scratch, err := newSnapshotScratch(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = scratch.Close() // Tests deliberately retain injected sticky errors.
		assertSnapshotScratchStats(t, scratch, 0, 0)
	})
	return scratch
}

func assertSnapshotScratchStats(t *testing.T, scratch *snapshotScratch, wantBytes uint64, wantFiles int) {
	t.Helper()
	if used, files := scratch.stats(); used != wantBytes || files != wantFiles {
		t.Fatalf("scratch retained bytes=%d files=%d; want bytes=%d files=%d", used, files, wantBytes, wantFiles)
	}
}

func assertSnapshotScratchOnlyOwner(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != snapshotScratchOwnerName {
		t.Fatalf("scratch left named artifacts: %v", entries)
	}
}

type scriptedSnapshotInstaller struct {
	mu             sync.Mutex
	required       pb.SnapshotFormat
	compatible     func(pb.SnapshotFormat) bool
	installs       int
	searchIndexErr error
	transport      SnapshotTransportLimits
}

func testGraphSnapshotSpoolConfig(t *testing.T) GraphSnapshotSpoolConfig {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return GraphSnapshotSpoolConfig{Directory: directory, MaxDiskBytes: 128 << 20, MaxFrameBytes: 32 << 20}
}

func testGraphOnlySnapshotInstaller(t *testing.T, apply MutationApplier, snap SnapshotApplier) SnapshotInstaller {
	t.Helper()
	return newGraphOnlySnapshotInstaller(apply, snap, testGraphSnapshotSpoolConfig(t))
}

func (i *scriptedSnapshotInstaller) RequiredFormat() pb.SnapshotFormat {
	return i.required
}

func (i *scriptedSnapshotInstaller) CompatibleFormat(format pb.SnapshotFormat) bool {
	if i.compatible != nil {
		return i.compatible(format)
	}
	return format == i.required
}

func (i *scriptedSnapshotInstaller) SnapshotTransportLimits() SnapshotTransportLimits {
	if i.transport == (SnapshotTransportLimits{}) {
		return SnapshotTransportLimits{
			MaxFrameBytes:  defaultSnapshotMaxFrameBytes,
			MaxStreamBytes: defaultSnapshotMaxStreamBytes,
		}
	}
	return i.transport
}

func (i *scriptedSnapshotInstaller) Install(_ context.Context, stream SnapshotStream) (SnapshotInstallResult, error) {
	var (
		header *pb.SnapshotHeader
		counts SnapshotGraphCounts
	)
	for stream.Receive() {
		frame := stream.Msg()
		if frame == nil {
			continue
		}
		owned := proto.Clone(frame).(*pb.SnapshotResponse)
		if current := owned.GetHeader(); current != nil {
			header = proto.Clone(current).(*pb.SnapshotHeader)
		}
		if footer := owned.GetFooter(); footer != nil {
			counts = SnapshotGraphCounts{
				Vertices:                   footer.GetVertexCount(),
				Edges:                      footer.GetEdgeCount(),
				VertexCausalBarriers:       footer.GetVertexCausalBarrierCount(),
				EdgeCausalBarriers:         footer.GetEdgeCausalBarrierCount(),
				VertexTombstones:           footer.GetVertexTombstoneCount(),
				EdgeTombstones:             footer.GetEdgeTombstoneCount(),
				EdgeContributionTombstones: footer.GetEdgeContributionTombstoneCount(),
			}
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return SnapshotInstallResult{}, err
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	i.installs++
	return SnapshotInstallResult{
		Header:         header,
		Graph:          counts,
		searchIndexErr: i.searchIndexErr,
	}, nil
}

func (i *scriptedSnapshotInstaller) installCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.installs
}

type installerTestPeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler

	mu                    sync.Mutex
	requiredFormat        pb.SnapshotFormat
	header                *pb.SnapshotHeader
	frames                []*pb.SnapshotResponse
	selfOrigin            hlc.NodeID
	originSeq             uint64
	extraOrigins          []*pb.OriginState
	gapFirstSubscribe     bool
	firstSubscribeErr     error
	resumeFloor           map[string]uint64
	subscribeMutation     *pb.Mutation
	subscribeMutations    []*pb.Mutation
	subscribeRequests     []*pb.SubscribeRequest
	snapshotRequests      []*pb.SnapshotRequest
	snapshotTailValues    []string
	subscribeTailMetadata [][]string
	snapshotTailMetadata  [][]string
}

func (p *installerTestPeer) PeerStatus(
	context.Context,
	*connect.Request[pb.PeerStatusRequest],
) (*connect.Response[pb.PeerStatusResponse], error) {
	response := &pb.PeerStatusResponse{
		RequiredSnapshotFormat: p.requiredFormat,
	}
	if p.selfOrigin != (hlc.NodeID{}) {
		response.SelfOrigin = append([]byte(nil), p.selfOrigin[:]...)
		if p.originSeq > 0 {
			response.Origins = []*pb.OriginState{{
				Origin:  append([]byte(nil), p.selfOrigin[:]...),
				LastSeq: p.originSeq,
			}}
		}
	}
	for _, origin := range p.extraOrigins {
		response.Origins = append(response.Origins, proto.Clone(origin).(*pb.OriginState))
	}
	return connect.NewResponse(response), nil
}

func (p *installerTestPeer) Subscribe(
	_ context.Context,
	req *connect.Request[pb.SubscribeRequest],
	stream *connect.ServerStream[pb.SubscribeResponse],
) error {
	p.mu.Lock()
	p.subscribeTailMetadata = append(p.subscribeTailMetadata, append([]string(nil), req.Header().Values(snapshotTailHeader)...))
	p.subscribeRequests = append(
		p.subscribeRequests,
		proto.Clone(req.Msg).(*pb.SubscribeRequest),
	)
	call := len(p.subscribeRequests)
	gap := p.gapFirstSubscribe && call == 1
	p.mu.Unlock()
	if gap {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped"))
	}
	if call > 1 {
		for origin, next := range p.resumeFloor {
			if req.Msg.GetFromSeqPerOrigin()[origin] < next {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped: committed origin cursor was not used after Snapshot"))
			}
		}
	}
	if p.subscribeMutation != nil {
		if err := stream.Send(&pb.SubscribeResponse{Event: &pb.SubscribeResponse_Mutation{
			Mutation: proto.Clone(p.subscribeMutation).(*pb.Mutation),
		}}); err != nil {
			return err
		}
	}
	for _, mutation := range p.subscribeMutations {
		if err := stream.Send(&pb.SubscribeResponse{Event: &pb.SubscribeResponse_Mutation{
			Mutation: proto.Clone(mutation).(*pb.Mutation),
		}}); err != nil {
			return err
		}
	}
	if call == 1 && p.firstSubscribeErr != nil {
		return p.firstSubscribeErr
	}
	return nil
}

func (p *installerTestPeer) Snapshot(
	_ context.Context,
	req *connect.Request[pb.SnapshotRequest],
	stream *connect.ServerStream[pb.SnapshotResponse],
) error {
	p.mu.Lock()
	p.snapshotTailMetadata = append(p.snapshotTailMetadata, append([]string(nil), req.Header().Values(snapshotTailHeader)...))
	if p.snapshotTailValues != nil {
		stream.ResponseHeader()[snapshotTailHeader] = append([]string(nil), p.snapshotTailValues...)
	}
	p.snapshotRequests = append(
		p.snapshotRequests,
		proto.Clone(req.Msg).(*pb.SnapshotRequest),
	)
	frames := make([]*pb.SnapshotResponse, 0, len(p.frames))
	for _, frame := range p.frames {
		frames = append(frames, proto.Clone(frame).(*pb.SnapshotResponse))
	}
	if p.frames == nil {
		header := p.header
		if header == nil {
			header = &pb.SnapshotHeader{Format: p.requiredFormat}
		}
		frames = []*pb.SnapshotResponse{
			{Entry: &pb.SnapshotResponse_Header{
				Header: proto.Clone(header).(*pb.SnapshotHeader),
			}},
			{Entry: &pb.SnapshotResponse_Footer{
				Footer: &pb.SnapshotFooter{},
			}},
		}
	}
	p.mu.Unlock()
	for _, frame := range frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

func (p *installerTestPeer) requests() ([]*pb.SubscribeRequest, []*pb.SnapshotRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	subscribes := make([]*pb.SubscribeRequest, 0, len(p.subscribeRequests))
	for _, req := range p.subscribeRequests {
		subscribes = append(subscribes, proto.Clone(req).(*pb.SubscribeRequest))
	}
	snapshots := make([]*pb.SnapshotRequest, 0, len(p.snapshotRequests))
	for _, req := range p.snapshotRequests {
		snapshots = append(snapshots, proto.Clone(req).(*pb.SnapshotRequest))
	}
	return subscribes, snapshots
}

func startInstallerTestPeer(t *testing.T, peer graphv1connect.LanternReplicationServiceHandler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	server := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	return server
}

type fixedLocalState struct {
	seq uint64
}

func (s fixedLocalState) LocalSeq(hlc.NodeID) uint64 {
	return s.seq
}

type cursorTestState struct {
	fixedLocalState
	cursor map[string]uint64
	err    error
}

func (s *cursorTestState) SubscribeResumeCursor() (map[string]uint64, error) {
	return s.cursor, s.err
}

func (*cursorTestState) ApplyMutation(context.Context, *pb.Mutation) error { return nil }

// This fixture exercises outbound tail metadata with test-owned signed OFF
// membership and verified workload TLS. Its synthetic handler does not perform
// inbound membership admission or qualify a native environment or time provider.
func startTailMetadataPeer(t *testing.T, peer *installerTestPeer) (*httptest.Server, *PeerTransport) {
	t.Helper()
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	sample := snapshotAdmissionObservation{owner: new(byte), now: now}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(10 * time.Minute),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	serverCertificate, serverLeaf := tailMetadataWorkloadCertificate(t, ca, caPrivate, 2, "spiffe://lantern.test/tail-peer", x509.ExtKeyUsageServerAuth)
	clientCertificate, clientLeaf := tailMetadataWorkloadCertificate(t, ca, caPrivate, 3, "spiffe://lantern.test/tail-client", x509.ExtKeyUsageClientAuth)
	clientChains, err := clientLeaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil {
		t.Fatal(err)
	}
	clientWindow, err := peerauth.SelectCertificateWindow(clientChains, clientLeaf, now, now)
	if err != nil {
		t.Fatal(err)
	}

	membershipPath := filepath.Join(t.TempDir(), "membership")
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	server := httptest.NewUnstartedServer(mux)
	var store *peerauth.Store
	t.Cleanup(func() {
		server.Close()
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
		Time:         func() time.Time { return now },
	}
	server.StartTLS()

	self := peerauth.Member{ID: [16]byte{1}, Identity: clientLeaf.URIs[0].String(), SPKI: sha256.Sum256(clientLeaf.RawSubjectPublicKeyInfo), Origin: "https://localhost:1"}
	remote := peerauth.Member{ID: [16]byte{2}, Identity: serverLeaf.URIs[0].String(), SPKI: sha256.Sum256(serverLeaf.RawSubjectPublicKeyInfo), Origin: server.URL}
	domain := peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: keyspace.Version, AuthMode: "off", TrustDigest: sha256.Sum256(caDER)}
	manifest := peerauth.Manifest{Version: 1, Domain: domain, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), Members: []peerauth.Member{self, remote}}
	operatorPublic, operatorPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := peerauth.SignManifest(manifest, operatorPrivate)
	if err != nil {
		t.Fatal(err)
	}
	store, err = peerauth.CreateStore(peerauth.StoreOptions{
		Path:             membershipPath,
		Key:              operatorPublic,
		Domain:           domain,
		Self:             self,
		Now:              func() time.Time { return now },
		Time:             func() (peerauth.TimeObservation, error) { return sample, nil },
		SelfCertificates: []peerauth.CertificateWindow{clientWindow},
	}, signed)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewVerifiedPeerTransport(peerauth.NewHTTPClient(store, clientCertificate, roots), func(origin string) bool {
		origins, err := store.Origins()
		if err != nil || origin != server.URL {
			return false
		}
		for _, current := range origins {
			if current == origin {
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, transport
}

func tailMetadataWorkloadCertificate(t *testing.T, ca *x509.Certificate, caPrivate ed25519.PrivateKey, serial int64, identity string, usage x509.ExtKeyUsage) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		NotBefore:    ca.NotBefore,
		NotAfter:     ca.NotAfter,
		URIs:         []*url.URL{uri},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	if usage == x509.ExtKeyUsageServerAuth {
		certificate.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificate, ca, public, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{certificateDER}, PrivateKey: private, Leaf: leaf}, leaf
}

type tailAppliedState struct {
	origin hlc.NodeID
	seq    atomic.Uint64
}

func (s *tailAppliedState) LocalSeq(hlc.NodeID) uint64 { return s.seq.Load() }
func (s *tailAppliedState) SubscribeResumeCursor() (map[string]uint64, error) {
	return map[string]uint64{hex.EncodeToString(s.origin[:]): s.seq.Load() + 1}, nil
}
func (s *tailAppliedState) ApplyMutation(_ context.Context, m *pb.Mutation) error {
	s.seq.Store(m.GetSeq())
	return nil
}
