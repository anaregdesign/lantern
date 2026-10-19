package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

type peerSocketWrite struct {
	id         uint64
	remote     string
	size       int
	start, end time.Time
	failed     bool
}

type peerSocketProbe struct {
	mu               sync.Mutex
	next             uint64
	active           map[uint64]peerSocketWrite
	writes           []peerSocketWrite
	boundInitialRead atomic.Bool
	boundClients     sync.Map
}

type peerClientSocket struct{ err error }

func (p *peerSocketProbe) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if !p.boundInitialRead.Load() {
			return
		}
		var err error
		if connection, ok := info.Conn.(*tls.Conn); ok {
			if tcp, ok := connection.NetConn().(*net.TCPConn); ok {
				err = tcp.SetReadBuffer(1024)
			} else {
				err = fmt.Errorf("private client connection is not TCP")
			}
		} else {
			err = fmt.Errorf("private client connection is not TLS")
		}
		p.boundClients.Store(info.Conn.LocalAddr().String(), peerClientSocket{err: err})
	}}
}

func (p *peerSocketProbe) clientBufferError(remote string) error {
	value, ok := p.boundClients.Load(remote)
	if !ok {
		return fmt.Errorf("private subscriber did not bind its owned receive buffer")
	}
	return value.(peerClientSocket).err
}

// blockedWrite observes real socket backpressure before the test overflows the
// subscription. No test barrier is inserted in Send or net.Conn.Write.
func (p *peerSocketProbe) blockedWrite(ctx context.Context, after time.Time) (peerSocketWrite, error) {
	stacks := make([]byte, 1<<20)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var observedID uint64
	var observedAt time.Time
	for ctx.Err() == nil {
		var candidate peerSocketWrite
		waiting := false
		p.mu.Lock()
		if len(p.active) == 1 {
			n := runtime.Stack(stacks, true)
			for _, stack := range bytes.Split(stacks[:n], []byte("\n\n")) {
				if bytes.Contains(stack, []byte("[IO wait]")) &&
					bytes.Contains(stack, []byte("internal/poll.(*FD).Write(")) &&
					bytes.Contains(stack, []byte("(*peerProbeConn).Write(")) {
					for _, write := range p.active {
						if !write.start.Before(after) {
							candidate, waiting = write, true
						}
					}
				}
			}
		}
		p.mu.Unlock()
		if waiting {
			if observedID != candidate.id {
				observedID, observedAt = candidate.id, time.Now()
			} else if time.Since(observedAt) >= time.Second {
				return candidate, nil
			}
		} else {
			observedID = 0
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
		}
	}
	return peerSocketWrite{}, ctx.Err()
}

type peerProbeListener struct {
	net.Listener
	probe *peerSocketProbe
}

func (l peerProbeListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	// Bound only this owned socket's send buffer, never the host's settings.
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(1024); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return &peerProbeConn{Conn: conn, probe: l.probe}, nil
}

type peerProbeConn struct {
	net.Conn
	probe *peerSocketProbe
}

func (c *peerProbeConn) Write(data []byte) (int, error) {
	c.probe.mu.Lock()
	c.probe.next++
	write := peerSocketWrite{id: c.probe.next, remote: c.RemoteAddr().String(), size: len(data), start: time.Now()}
	c.probe.active[write.id] = write
	c.probe.mu.Unlock()
	n, err := c.Conn.Write(data)
	c.probe.mu.Lock()
	delete(c.probe.active, write.id)
	write.end, write.failed = time.Now(), err != nil
	c.probe.writes = append(c.probe.writes, write)
	c.probe.mu.Unlock()
	return n, err
}

type peerStreamStarted struct {
	started chan struct{}
	once    sync.Once
}

func (m *peerStreamStarted) OnSubscribeStarted()     { m.once.Do(func() { close(m.started) }) }
func (*peerStreamStarted) OnSubscribeEnded()         {}
func (*peerStreamStarted) OnSubscribeDropped(string) {}

type peerStreamEnd struct {
	remote    string
	at        time.Time
	cancelled bool
	proto     int
}

type peerHeldApply struct {
	*service.LanternService
	entered                                        chan struct{}
	release                                        <-chan struct{}
	seed                                           chan struct{}
	committed                                      chan uint64
	installStarted, installFinished                chan struct{}
	seedOnce, enteredOnce, installOnce, finishOnce sync.Once
}

func (a *peerHeldApply) ApplyMutation(ctx context.Context, mutation *pb.Mutation) error {
	if mutation.GetSeq() == 2 {
		a.enteredOnce.Do(func() { close(a.entered) })
		select {
		case <-a.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := a.LanternService.ApplyMutation(ctx, mutation); err != nil {
		return err
	}
	if mutation.GetSeq() == 1 {
		a.seedOnce.Do(func() { close(a.seed) })
	}
	select {
	case a.committed <- mutation.GetSeq():
	default:
	}
	return nil
}

func (a *peerHeldApply) BeginSnapshotInstall() (func(bool), error) {
	finish, err := a.LanternService.BeginSnapshotInstall()
	if err != nil {
		return nil, err
	}
	a.installOnce.Do(func() { close(a.installStarted) })
	return func(verified bool) {
		finish(verified)
		if verified {
			a.finishOnce.Do(func() { close(a.installFinished) })
		}
	}, nil
}

type peerBarrierSnapshotHandler struct {
	graphv1connect.LanternReplicationServiceHandler
	peer    *service.LanternReplicationService
	release <-chan struct{}
	calls   *atomic.Int32
}

func (h peerBarrierSnapshotHandler) Snapshot(ctx context.Context, req *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	h.calls.Add(1)
	return h.peer.Snapshot(ctx, req.Msg, peerBarrierSnapshotSender{ServerStream: stream, ctx: ctx, release: h.release})
}

type peerBarrierSnapshotSender struct {
	*connect.ServerStream[pb.SnapshotResponse]
	ctx     context.Context
	release <-chan struct{}
}

func (s peerBarrierSnapshotSender) Send(frame *pb.SnapshotResponse) error {
	if err := s.ServerStream.Send(frame); err != nil {
		return err
	}
	if frame.GetHeader() != nil {
		select {
		case <-s.release:
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	return nil
}

func TestPeerSecurityOverflowInterruptsRealPrivateSendAndRepairsCursor(t *testing.T) {
	const phaseTimeout = 15 * time.Second
	for _, evict := range []bool{false, true} {
		name := "retained history"
		if evict {
			name = "evicted history"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pressureCtx, cancelPressure := context.WithTimeout(ctx, phaseTimeout)
			defer cancelPressure()
			probe := &peerSocketProbe{active: make(map[uint64]peerSocketWrite)}
			probe.boundInitialRead.Store(true)
			streamStarted := &peerStreamStarted{started: make(chan struct{})}
			startedRemote := make(chan string, 1)
			dropped := make(chan struct{})
			ended := make(chan peerStreamEnd, 8)
			applyRelease, snapshotRelease := make(chan struct{}), make(chan struct{})
			var applyOnce, snapshotOnce, dropOnce sync.Once
			releaseApply := func() { applyOnce.Do(func() { close(applyRelease) }) }
			releaseSnapshot := func() { snapshotOnce.Do(func() { close(snapshotRelease) }) }
			defer releaseApply()
			defer releaseSnapshot()
			var snapshotCalls atomic.Int32
			var receiveGraph *graphcache.GraphCache[string, *pb.Vertex]
			f := newPeerSecurityFixture(t, peerSecurityOptions{
				Listener: func(i int, listener net.Listener) net.Listener {
					if i == 0 {
						return peerProbeListener{Listener: listener, probe: probe}
					}
					return listener
				},
				LogOptions: func(i int) mutationlog.Options {
					options := mutationlog.Options{Capacity: 10000, SubscriberBuffer: 512}
					if i == 0 {
						options.OnDrop = func(cause string) {
							if cause == mutationlog.DropCauseBufferFull {
								dropOnce.Do(func() { close(dropped) })
							}
						}
					}
					return options
				},
				Mount: func(f *peerSecurityFixture, i int, runtime *service.ServingRuntime, mux *http.ServeMux) {
					if i == 1 {
						receiveGraph = runtime.GraphCache()
						return
					}
					peer, err := runtime.NewLanternReplicationService(f.primary[i])
					if err != nil {
						t.Fatal(err)
					}
					peer.WithMetrics(streamStarted)
					base := service.NewLanternReplicationServiceConnectHandler(peer)
					wrapped := peerBarrierSnapshotHandler{LanternReplicationServiceHandler: base, peer: peer, release: snapshotRelease, calls: &snapshotCalls}
					_, handler := graphv1connect.NewLanternReplicationServiceHandler(wrapped)
					mux.Handle("/graph.v1.LanternReplicationService/Subscribe", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						select {
						case startedRemote <- r.RemoteAddr:
						default:
						}
						handler.ServeHTTP(w, r)
						select {
						case ended <- peerStreamEnd{remote: r.RemoteAddr, at: time.Now(), cancelled: r.Context().Err() != nil, proto: r.ProtoMajor}:
						default:
						}
					}))
					mux.Handle("/graph.v1.LanternReplicationService/Snapshot", handler)
				},
			})
			write := func(writeCtx context.Context, key string, value []byte) {
				t.Helper()
				if _, err := f.primary[0].PutVertex(writeCtx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_Bytes{Bytes: value}}}); err != nil {
					t.Fatal(err)
				}
			}
			apply := &peerHeldApply{LanternService: f.primary[1], entered: make(chan struct{}), release: applyRelease, seed: make(chan struct{}), committed: make(chan uint64, 2048), installStarted: make(chan struct{}), installFinished: make(chan struct{})}
			transport, err := provider.NewWorkloadPeerTransport(f.runtimes[1])
			if err != nil {
				t.Fatal(err)
			}
			pump := replication.NewPump(replication.Config{NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{f.servers[0].URL}, PeerTransport: transport, BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond}, apply, receiveGraph)
			pumpCtx, stopPump := context.WithCancel(ctx)
			pumpCtx = httptrace.WithClientTrace(pumpCtx, probe.clientTrace())
			pumpDone := make(chan error, 1)
			go func() { pumpDone <- pump.Run(pumpCtx) }()
			t.Cleanup(func() {
				stopPump()
				releaseApply()
				releaseSnapshot()
				select {
				case <-pumpDone:
				case <-time.After(5 * time.Second):
					t.Error("owned pump did not stop")
				}
			})
			select {
			case <-streamStarted.started:
			case <-pressureCtx.Done():
				t.Fatal("pump did not open the initially empty live subscription", pressureCtx.Err())
			}
			probe.boundInitialRead.Store(false)
			select {
			case remote := <-startedRemote:
				if err := probe.clientBufferError(remote); err != nil {
					t.Fatal(err)
				}
			case <-pressureCtx.Done():
				t.Fatal("private subscriber did not identify its owned socket", pressureCtx.Err())
			}
			// An empty log registers its live subscriber synchronously, so the
			// pressure below cannot be mistaken for an unbounded replay window.
			write(pressureCtx, "data:seed", []byte("seed"))
			select {
			case <-apply.seed:
			case <-pressureCtx.Done():
				t.Fatal("pump did not establish committed seed", pressureCtx.Err())
			}
			payload := make([]byte, 64<<10)
			if _, err := rand.Read(payload); err != nil {
				t.Fatal(err)
			}
			write(pressureCtx, "data:pressure", payload)
			select {
			case <-apply.entered:
			case <-pressureCtx.Done():
				t.Fatal("receiver did not hold second application", pressureCtx.Err())
			}
			heldAt := time.Now()
			// At most 512 new entries fit without overflowing the normal live
			// buffer. First prove an actual Write is blocked, then cause loss.
			for range 511 {
				write(pressureCtx, "data:pressure", payload)
			}
			blocked, err := probe.blockedWrite(pressureCtx, heldAt)
			if err != nil {
				t.Fatal("private socket did not enter actual I/O wait before loss", err)
			}
			select {
			case <-dropped:
				t.Fatal("subscription overflowed before the blocked socket observation")
			default:
			}
		produce:
			for i := 0; i < 1024; i++ {
				select {
				case <-dropped:
					break produce
				default:
				}
				write(pressureCtx, "data:pressure", payload)
			}
			select {
			case <-dropped:
			case <-pressureCtx.Done():
				t.Fatal("finite private stream did not overflow", pressureCtx.Err())
			}
			var end peerStreamEnd
			select {
			case end = <-ended:
			case <-pressureCtx.Done():
				t.Fatal("overflow did not interrupt the actual private handler", pressureCtx.Err())
			}
			if end.proto != 1 || !end.cancelled || pressureCtx.Err() != nil {
				t.Fatalf("private stream end: HTTP%d cancelled=%t pressure=%v", end.proto, end.cancelled, pressureCtx.Err())
			}
			if err := probe.clientBufferError(end.remote); err != nil {
				t.Fatal(err)
			}
			probe.mu.Lock()
			interruptedWrite := false
			var completedObservation peerSocketWrite
			failedWrites := 0
			for _, write := range probe.writes {
				if write.id == blocked.id {
					completedObservation = write
				}
				if write.remote == end.remote && write.failed {
					failedWrites++
				}
				if write.id == blocked.id && write.remote == end.remote && write.failed && !write.end.After(end.at) {
					interruptedWrite = true
				}
			}
			probe.mu.Unlock()
			if !interruptedWrite {
				t.Fatalf("observed socket Write did not fail before handler return: observed=%+v completed=%+v failed_writes=%d", blocked, completedObservation, failedWrites)
			}
			if f.primary[1].LocalSeq(hlc.NodeID{3}) != 1 {
				t.Fatal("held receiver advanced its committed cursor")
			}
			if first, _ := f.logs[0].FirstSeq(); first != 1 {
				t.Fatal("retention was lost before the intended branch")
			}
			// The interruption proof is complete. Evicting a normal-size ring
			// is finite fixture preparation, not part of its elapsed deadline.
			// Cancel completed phases so later work cannot borrow their context.
			cancelPressure()
			if evict {
				// This fixed-count, in-process preparation uses the owning
				// test budget. A separate short deadline would assert a
				// fixture throughput floor rather than recovery behavior.
				for i := 0; i < 10016; i++ {
					write(ctx, "data:pressure", []byte("later"))
				}
				if first, _ := f.logs[0].FirstSeq(); first <= 3 {
					t.Fatal("finite fixture failed to evict the committed resume cursor")
				}
			}
			recoveryCtx, cancelRecovery := context.WithTimeout(ctx, phaseTimeout)
			defer cancelRecovery()
			last := f.primary[0].LocalSeq(hlc.NodeID{3})
			releaseApply()
			if evict {
				select {
				case <-apply.installStarted:
				case <-recoveryCtx.Done():
					t.Fatal("missing prefix did not trigger private Snapshot", recoveryCtx.Err())
				}
				if _, err := f.primary[1].GetVertex(recoveryCtx, &pb.GetVertexRequest{Key: "data:seed"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("incomplete Snapshot exposed public data: %v", err)
				}
				releaseSnapshot()
				select {
				case <-apply.installFinished:
				case <-recoveryCtx.Done():
					t.Fatal("verified Snapshot did not reopen publication", recoveryCtx.Err())
				}
				if snapshotCalls.Load() == 0 {
					t.Fatal("evicted branch skipped Snapshot")
				}
			} else {
				for f.primary[1].LocalSeq(hlc.NodeID{3}) < last {
					select {
					case <-apply.committed:
					case <-recoveryCtx.Done():
						t.Fatal("retained prefix did not converge", recoveryCtx.Err())
					}
				}
				if snapshotCalls.Load() != 0 {
					t.Fatal("retained prefix unnecessarily used Snapshot")
				}
			}
			if f.primary[1].LocalSeq(hlc.NodeID{3}) < last {
				t.Fatal("recovery did not certify the expected origin frontier")
			}
			if _, err := f.primary[1].GetVertex(recoveryCtx, &pb.GetVertexRequest{Key: "data:seed"}); err != nil {
				t.Fatal("verified recovery did not restore public read", err)
			}
			stopPump()
			t.Logf("HTTP/1 private socket Write interrupted before handler return; evicted=%t SnapshotCalls=%d", evict, snapshotCalls.Load())
		})
	}
}

// Independent operator wire fixture: root tests do not import Server internals
// or rely on their signing/serialization helpers to validate the same contract.
type peerOperatorDomain struct {
	Deployment         [16]byte `json:"deployment"`
	NamespaceFormat    string   `json:"namespace_format"`
	AuthMode           string   `json:"auth_mode"`
	SecurityGeneration [16]byte `json:"security_generation"`
	WriterPublicKey    [32]byte `json:"writer_public_key"`
	TrustDigest        [32]byte `json:"trust_digest"`
	CurrentProfile     string   `json:"current_profile,omitempty"`
}

type peerOperatorMember struct {
	ID       [16]byte `json:"id"`
	Identity string   `json:"identity"`
	SPKI     [32]byte `json:"spki"`
	Origin   string   `json:"origin"`
}

type peerOperatorManifest struct {
	Version   uint64               `json:"version"`
	Domain    peerOperatorDomain   `json:"domain"`
	IssuedAt  time.Time            `json:"issued_at"`
	ExpiresAt time.Time            `json:"expires_at"`
	Members   []peerOperatorMember `json:"members"`
}

type peerSecurityFixture struct {
	servers  [2]*httptest.Server
	runtimes [2]*provider.PeerIdentityRuntime
	primary  [2]*service.LanternService
	configs  [2]provider.PeerIdentityConfig
	manifest peerOperatorManifest
	key      ed25519.PrivateKey
	roots    *x509.CertPool
	certs    [2]tls.Certificate
	logs     [2]*mutationlog.Log
}

type peerSecurityOptions struct {
	Generation     [16]byte
	CurrentProfile string
	Now            func() time.Time
	TimeBounds     func() (time.Time, time.Time, error)
	Mount          func(*peerSecurityFixture, int, *service.ServingRuntime, *http.ServeMux)
	LogOptions     func(int) mutationlog.Options
	Listener       func(int, net.Listener) net.Listener
}

func peerSecurityPEM(t *testing.T, directory, name, kind string, raw []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := createPrivateTestFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: raw})); err != nil {
		t.Fatal(err)
	}
	return path
}

func peerSecuritySign(t *testing.T, manifest peerOperatorManifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Payload   json.RawMessage `json:"payload"`
		Signature []byte          `json:"signature"`
	}{payload, ed25519.Sign(key, append([]byte("lantern.peer.membership.v1\x00"), payload...))})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newPeerSecurityFixture(t *testing.T, options ...peerSecurityOptions) *peerSecurityFixture {
	t.Helper()
	f := &peerSecurityFixture{}
	dir := t.TempDir()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = private
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	operatorPath := peerSecurityPEM(t, dir, "operator.pub", "PUBLIC KEY", publicDER)
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Lantern peer test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPath := peerSecurityPEM(t, dir, "ca.pem", "CERTIFICATE", caDER)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	f.roots = x509.NewCertPool()
	f.roots.AddCert(ca)
	f.manifest = peerOperatorManifest{Version: 1, Domain: peerOperatorDomain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: sha256.Sum256(caPEM)}, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if len(options) != 0 && options[0].CurrentProfile != "" {
		f.manifest.Domain.AuthMode = "oidc"
		f.manifest.Domain.SecurityGeneration = options[0].Generation
		f.manifest.Domain.CurrentProfile = options[0].CurrentProfile
	}
	for i := range f.servers {
		server := httptest.NewUnstartedServer(nil)
		if len(options) != 0 && options[0].Listener != nil {
			server.Listener = options[0].Listener(i, server.Listener)
		}
		f.servers[i] = server
		identity := "spiffe://lantern.test/node-" + string(rune('a'+i))
		uri, _ := url.Parse(identity)
		certPub, certKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)),
			NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), URIs: []*url.URL{uri},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, certPub, caKey)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(leafDER)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(certKey)
		if err != nil {
			t.Fatal(err)
		}
		certPath := peerSecurityPEM(t, dir, "cert-"+string(rune('a'+i))+".pem", "CERTIFICATE", leafDER)
		keyPath := peerSecurityPEM(t, dir, "key-"+string(rune('a'+i))+".pem", "PRIVATE KEY", keyDER)
		f.certs[i], err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		origin := "https://" + server.Listener.Addr().String()
		f.manifest.Members = append(f.manifest.Members, peerOperatorMember{ID: [16]byte{byte(i + 2)}, Identity: identity, SPKI: sha256.Sum256(leaf.RawSubjectPublicKeyInfo), Origin: origin})
		f.configs[i] = provider.PeerIdentityConfig{AuthMode: "off", Deployment: [16]byte{1}, CAFile: caPath, OperatorKeyFile: operatorPath,
			ManifestFile: filepath.Join(dir, "membership-"+string(rune('a'+i))+".json"), StateFile: filepath.Join(dir, "floor-"+string(rune('a'+i))),
			StateMode: "fresh", CertFile: certPath, KeyFile: keyPath, SelfIdentity: identity}
		f.configs[i].AuthMode = f.manifest.Domain.AuthMode
		f.configs[i].SecurityGeneration = f.manifest.Domain.SecurityGeneration
		f.configs[i].WriterPublicKey = f.manifest.Domain.WriterPublicKey
		f.configs[i].CurrentProfile = f.manifest.Domain.CurrentProfile
		if len(options) != 0 {
			f.configs[i].Now, f.configs[i].TimeBounds = options[0].Now, options[0].TimeBounds
		}
	}
	raw := peerSecuritySign(t, f.manifest, f.key)
	for i := range f.servers {
		if err := os.WriteFile(f.configs[i].ManifestFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
		runtime, cleanup, err := provider.NewPeerIdentityRuntime(f.configs[i])
		if err != nil {
			t.Fatal(err)
		}
		f.runtimes[i] = runtime
		t.Cleanup(cleanup)
		graph := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
		logOptions := mutationlog.Options{Capacity: 16}
		if len(options) != 0 && options[0].LogOptions != nil {
			logOptions = options[0].LogOptions(i)
		}
		f.logs[i] = mutationlog.New(logOptions)
		data, err := service.NewGraphOnlyServingRuntime(graph, f.logs[i], hlc.New(hlc.NodeID{byte(i + 3)}, hlc.Options{}), "namespaced-v1")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = data.Close() })
		primary := data.NewLanternService(nil).WithDataNamespace()
		f.primary[i] = primary
		peer, err := data.NewLanternReplicationService(primary)
		if err != nil {
			t.Fatal(err)
		}
		if err := data.CertifyInstallation(primary, peer); err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(peer)))
		if len(options) != 0 && options[0].Mount != nil {
			options[0].Mount(f, i, data, mux)
		}
		server := f.servers[i]
		server.Config.Handler = runtime.ProtectPeerHTTPHandler(mux)
		server.TLS = runtime.TLSConfig()
		server.EnableHTTP2 = true
		server.StartTLS()
		t.Cleanup(server.Close)
	}
	return f
}

func TestPeerSecurityRealConnectAdmissionRemovalAndRestart(t *testing.T) {
	f := newPeerSecurityFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := graphv1connect.NewLanternReplicationServiceClient(f.runtimes[1].HTTPClient(), f.servers[0].URL)
	request := func() *connect.Request[pb.PeerStatusRequest] {
		return connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"})
	}
	if response, err := client.PeerStatus(ctx, request()); err != nil || response.Msg.GetNamespaceFormat() != "namespaced-v1" {
		t.Fatal("approved mTLS peer failed", err)
	}
	credential := request()
	credential.Header().Set("Authorization", "Bearer "+testToken)
	if _, err := client.PeerStatus(ctx, credential); err == nil {
		t.Fatal("public credential entered workload transport")
	}
	plainClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: f.roots, Certificates: []tls.Certificate{f.certs[1]}}}}
	plain := graphv1connect.NewLanternReplicationServiceClient(plainClient, f.servers[0].URL)
	if _, err := plain.PeerStatus(ctx, request()); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("missing domain admitted", err)
	}
	wrongDomain := request()
	wrongDomain.Header().Set("Lantern-Peer-Domain", hex.EncodeToString(make([]byte, 32)))
	if _, err := plain.PeerStatus(ctx, wrongDomain); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("mixed domain admitted", err)
	}
	if _, err := f.primary[0].PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:visible"}}}); err != nil {
		t.Fatal(err)
	}
	stream, err := client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", AcceptReceiptEnvelopes: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Receive() {
		t.Fatal("approved stream did not deliver mutation", stream.Err())
	}
	f.manifest.Version++
	f.manifest.Members = f.manifest.Members[:1]
	raw := peerSecuritySign(t, f.manifest, f.key)
	if err := os.WriteFile(f.configs[0].ManifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.runtimes[0].ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	if stream.Receive() || stream.Err() == nil {
		t.Fatal("removed workload retained active stream")
	}
	if _, err := client.PeerStatus(ctx, request()); err == nil {
		t.Fatal("removed workload opened another RPC")
	}
	if err := os.WriteFile(f.configs[1].ManifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.runtimes[1].ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtimes[1].ApprovedOrigins(); err == nil {
		t.Fatal("locally removed replica retained outbound authority")
	}
}

func TestPeerSecurityRefusesRollbackAcrossProcessReconstruction(t *testing.T) {
	f := newPeerSecurityFixture(t)
	// Use a third independent state path so live fixture ownership is preserved.
	config := f.configs[0]
	config.StateFile = filepath.Join(t.TempDir(), "restart-floor")
	runtime, cleanup, err := provider.NewPeerIdentityRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	f.manifest.Version++
	newer := peerSecuritySign(t, f.manifest, f.key)
	if err := os.WriteFile(config.ManifestFile, newer, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReloadMembership(); err != nil {
		t.Fatal(err)
	}
	cleanup()
	f.manifest.Version--
	if err := os.WriteFile(config.ManifestFile, peerSecuritySign(t, f.manifest, f.key), 0600); err != nil {
		t.Fatal(err)
	}
	config.StateMode = "resume"
	if runtime, cleanup, err := provider.NewPeerIdentityRuntime(config); err == nil || runtime != nil || cleanup != nil {
		t.Fatal("operator file rolled persisted floor backward", err)
	}
	if err := os.WriteFile(config.ManifestFile, newer, 0600); err != nil {
		t.Fatal(err)
	}
	runtime, cleanup, err = provider.NewPeerIdentityRuntime(config)
	if err != nil {
		t.Fatal("current signed checkpoint restart failed", err)
	}
	defer cleanup()
	if _, err := runtime.ApprovedOrigins(); err != nil {
		t.Fatal(err)
	}
}

func TestPeerSecurityIdleSubscribeFlushesHeadersWithoutAnEvent(t *testing.T) {
	f := newPeerSecurityFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := graphv1connect.NewLanternReplicationServiceClient(f.runtimes[1].HTTPClient(), f.servers[0].URL)
	// Bound only the opening handshake. An empty log must not need a Mutation
	// to finish Subscribe, nor should opening fabricate a protobuf event.
	opening := time.AfterFunc(500*time.Millisecond, cancel)
	stream, err := client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", AcceptReceiptEnvelopes: true}))
	opening.Stop()
	if err != nil {
		t.Fatal("empty private subscription did not open promptly", err)
	}
	defer func() { _ = stream.Close() }()
	first := make(chan bool, 1)
	go func() { first <- stream.Receive() }()
	select {
	case <-first:
		t.Fatal("idle subscription emitted a synthetic frame", stream.Err())
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := f.primary[0].PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:first", Value: &pb.Vertex_String_{String_: "committed"}}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-first:
		if !received {
			t.Fatal("first real mutation missing", stream.Err())
		}
		mutation := stream.Msg().GetMutation()
		if mutation == nil || mutation.Seq != 1 || len(mutation.GetOp().GetReplicatedPutVertices().GetEntries()) != 1 || mutation.GetOp().GetReplicatedPutVertices().Entries[0].GetLive().GetKey() != "data:first" {
			t.Fatal("mutation/cursor changed", stream.Msg())
		}
	case <-ctx.Done():
		t.Fatal("real mutation was not delivered", ctx.Err())
	}
	cancel()
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeCanceled {
		t.Fatal("cancel did not release the idle stream", stream.Err())
	}
	rejected, err := client.Subscribe(t.Context(), connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", FromSeqPerOrigin: map[string]uint64{"malformed": 1}, AcceptReceiptEnvelopes: true}))
	if err == nil {
		defer func() { _ = rejected.Close() }()
		if rejected.Receive() || connect.CodeOf(rejected.Err()) != connect.CodeInvalidArgument {
			t.Fatal("malformed cursor admitted", rejected.Err())
		}
	} else if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
}

// Origin authorization is covered by the OIDC Head-managed real-wire cases.
// Here the same immutable accepted effects cross the OFF operator mTLS peer plane,
// including an actual Subscribe gap repaired by private Snapshot.
func TestPeerSecurityEdgeOnlyHistorySubscribeSnapshotAndTombstoneRealConnect(t *testing.T) {
	var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
	var snapshots atomic.Int32
	f := newPeerSecurityFixture(t, peerSecurityOptions{Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
		graphs[index] = runtime.GraphCache()
		graphs[index].RetainDanglingEdgeHistory()
		f.primary[index].WithTombstoneTTL(time.Hour)
		if index == 0 {
			peer, err := runtime.NewLanternReplicationService(f.primary[index])
			if err != nil {
				t.Fatal(err)
			}
			_, handler := graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(peer))
			mux.Handle("/graph.v1.LanternReplicationService/Snapshot", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { snapshots.Add(1); handler.ServeHTTP(w, r) }))
		}
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	origin := hlc.NodeID{9}
	apply := func(seq uint64, op *pb.MutationOp) {
		t.Helper()
		ts := &pb.HLCTimestamp{WallNs: time.Now().UnixNano(), NodeId: origin[:]}
		if err := f.primary[0].ApplyMutation(ctx, &pb.Mutation{NamespaceFormat: "namespaced-v1", Seq: seq, Origin: origin[:], Hlc: ts, Op: op}); err != nil {
			t.Fatal(err)
		}
	}
	apply(1, &pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 2}}}})
	apply(2, &pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 3}}}})
	transport, err := provider.NewWorkloadPeerTransport(f.runtimes[1])
	if err != nil {
		t.Fatal(err)
	}
	start := func() (context.CancelFunc, <-chan error) {
		pctx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		pump := replication.NewPump(replication.Config{NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{f.servers[0].URL}, PeerTransport: transport, BackoffMin: 10 * time.Millisecond, BackoffMax: 20 * time.Millisecond}, f.primary[1], graphs[1])
		go func() { done <- pump.Run(pctx) }()
		return stop, done
	}
	wait := func(message string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !condition() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !condition() {
			t.Fatal(message)
		}
	}
	stop, done := start()
	wait("private Subscribe did not apply sources", func() bool { return f.primary[1].LocalSeq(origin) == 2 })
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertHiddenHistory := func() {
		t.Helper()
		if _, ok := graphs[1].GetVertex("data:head"); ok {
			t.Fatal("peer fabricated/revived head")
		}
		if _, ok := graphs[1].GetWeight("data:tail", "data:head"); ok {
			t.Fatal("dangling Edge exposed")
		}
		rows := graphs[1].SnapshotReplication().Graph.Edges
		if len(rows) != 1 || len(rows[0].Contributions) != 2 {
			t.Fatal("private accepted history lost", rows)
		}
	}
	assertHiddenHistory()
	for _, graph := range graphs {
		gctx, gcancel := context.WithCancel(ctx)
		ended := make(chan struct{})
		graph.SetGCHooks(nil, func(time.Duration) { gcancel() })
		go func() { defer close(ended); graph.Watch(gctx, time.Millisecond) }()
		select {
		case <-ended:
		case <-ctx.Done():
			gcancel()
			t.Fatal(ctx.Err())
		}
	}
	assertHiddenHistory()
	// Evict the original relay log window. The next pump must use Snapshot,
	// whose graph contains pending sources but neither missing endpoint.
	for i := range 20 {
		if _, err := f.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: fmt.Sprintf("data:noise:%02d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	stop, done = start()
	wait("private Snapshot did not repair the gap", func() bool { return snapshots.Load() > 0 && f.primary[1].LocalSeq(hlc.NodeID{3}) == 20 })
	assertHiddenHistory()
	for _, key := range []string{"data:tail", "data:head"} {
		if _, err := f.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}}); err != nil {
			t.Fatal(err)
		}
	}
	wait("explicit endpoints did not expose original converged sources", func() bool { weight, ok := graphs[1].GetWeight("data:tail", "data:head"); return ok && weight == 5 })
	if _, err := f.primary[0].DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "data:head"}); err != nil {
		t.Fatal(err)
	}
	wait("Vertex tombstone did not replicate", func() bool { return f.primary[1].LocalSeq(hlc.NodeID{3}) == 23 })
	apply(3, &pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "data:tail", Head: "data:head", Weight: 1}}}})
	wait("post-tombstone Edge-only Add did not replicate", func() bool { return f.primary[1].LocalSeq(origin) == 3 })
	if _, ok := graphs[1].GetVertex("data:head"); ok {
		t.Fatal("accepted Edge revived tombstoned head")
	}
	if _, ok := graphs[1].GetWeight("data:tail", "data:head"); ok {
		t.Fatal("post-tombstone dangling Edge exposed")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCurrentDataPeerIntervalRecoveryOverPrivateTLS(t *testing.T) {
	var mu sync.RWMutex
	low := time.Now().UTC().Add(2 * time.Second)
	high := low.Add(time.Second)
	unavailable := false
	bounds := func() (time.Time, time.Time, error) {
		mu.RLock()
		defer mu.RUnlock()
		if unavailable {
			return time.Time{}, time.Time{}, fmt.Errorf("no qualified interval")
		}
		return low, high, nil
	}
	now := func() time.Time {
		_, high, err := bounds()
		if err != nil {
			return time.Unix(0, int64(^uint64(0)>>1)).UTC()
		}
		return high
	}
	f := newPeerSecurityFixture(t, peerSecurityOptions{Generation: [16]byte{7}, CurrentProfile: "current-v2:abababababababababababababababababababababababababababababababab", Now: now, TimeBounds: bounds})
	client := graphv1connect.NewLanternReplicationServiceClient(f.runtimes[0].HTTPClient(), f.servers[1].URL)
	check := func(want bool) {
		t.Helper()
		_, err := client.PeerStatus(t.Context(), connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"}))
		if (err == nil) != want {
			t.Fatal("current data TLS interval admission", want, err)
		}
	}
	before := make([][]byte, len(f.configs))
	for i, cfg := range f.configs {
		var err error
		before[i], err = os.ReadFile(cfg.StateFile)
		if err != nil {
			t.Fatal(err)
		}
		if f.runtimes[i].TLSConfig().Time == nil {
			t.Fatal("current TLS lost qualified verification clock")
		}
	}
	check(true)
	mu.Lock()
	low = low.Add(time.Millisecond)
	high = high.Add(-time.Millisecond)
	mu.Unlock()
	check(true)
	mu.Lock()
	unavailable = true
	mu.Unlock()
	check(false)
	for _, r := range f.runtimes {
		if _, err := r.ApprovedOrigins(); err == nil || r.ReloadMembership() == nil || r.CheckWorkload(t.Context()) == nil {
			t.Fatal("source loss retained workload authority")
		}
	}
	mu.Lock()
	unavailable = false
	mu.Unlock()
	check(true)
	for i, cfg := range f.configs {
		if r := f.runtimes[i]; r.ReloadMembership() != nil || r.CheckWorkload(t.Context()) != nil {
			t.Fatal("qualified interval did not recover data workload")
		}
		after, err := os.ReadFile(cfg.StateFile)
		if err != nil || string(before[i]) != string(after) {
			t.Fatal("interval recovery changed signed checkpoint", err)
		}
	}
}
