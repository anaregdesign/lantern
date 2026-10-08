package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// Shared by source-paired tests and the M/P/B/TLS/process integration gates.
// The original H issuer remains the inherited test-only S2-C fixture.
type s3aTestNetwork struct {
	t         *testing.T
	f         *s2cTestFixture
	dir       string
	clock     atomic.Int64
	operator  ed25519.PrivateKey
	ca        *x509.Certificate
	caKey     ed25519.PrivateKey
	manifest  peerauth.ControlManifest
	configs   map[uint32]s3aConfig
	nodes     map[uint32]*s3aOwner
	listeners map[uint32]net.Listener
}

func s3aTestCluster(t *testing.T, change func(uint32, *s3aConfig)) *s3aTestNetwork {
	t.Helper()
	n := &s3aTestNetwork{t: t, f: s2cTestCluster(t, 3), dir: t.TempDir(), configs: map[uint32]s3aConfig{}, nodes: map[uint32]*s3aOwner{}, listeners: map[uint32]net.Listener{}}
	now := time.Now().UTC()
	n.clock.Store(now.UnixNano())
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "S3-A test-only root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	n.ca, n.caKey = ca, caKey
	rootsPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	operatorPub, operator, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	n.operator = operator
	profile := peerauth.ControlProfile{Version: 1, Lineage: peerauth.ControlLineage{Deployment: [16]byte{0x31}, Instance: [16]byte{0x32}}, NamespaceFormat: keyspace.Version, ProtocolScope: n.f.trust.scope, TrustDigest: sha256.Sum256(rootsPEM)}
	for _, m := range n.f.members {
		id := m.ID
		dir := filepath.Join(n.dir, fmt.Sprintf("node-%d", id))
		c := s2cTestConfig(t, n.f, id, dir)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		n.listeners[id] = listener
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		identity, _ := url.Parse(fmt.Sprintf("spiffe://s3a.test/node-%d", id))
		template := &x509.Certificate{SerialNumber: big.NewInt(int64(id) + 10), Subject: pkix.Name{CommonName: fmt.Sprintf("workload-%d", id)}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(30 * time.Minute), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{identity}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, pub, caKey)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		files := s3aIdentityFiles{Roots: filepath.Join(dir, "roots.pem"), Certificate: filepath.Join(dir, "cert.pem"), TLSKey: filepath.Join(dir, "tls.key"), VotingKey: filepath.Join(dir, "vote.key")}
		for path, raw := range map[string][]byte{files.Roots: rootsPEM, files.Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), files.TLSKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), files.VotingKey: c.Key} {
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		workload := peerauth.Member{ID: [16]byte{byte(id)}, Identity: identity.String(), SPKI: sha256.Sum256(leaf.RawSubjectPublicKeyInfo), Origin: "https://" + listener.Addr().String()}
		profile.Voters = append(profile.Voters, peerauth.ControlVoter{Workload: workload, Voter: id, Key: m.PublicKey, Proposer: m.Proposer})
		c.Key = nil
		n.configs[id] = s3aConfig{Participant: c, Identity: files, Membership: peerauth.ControlStoreOptions{Path: filepath.Join(dir, "membership"), Key: operatorPub, Self: workload, Now: func() time.Time { return time.Unix(0, n.clock.Load()).UTC() }}, Limits: s3aLimits{QueueBytes: 96 << 20, PerPeerQueue: 4, TransitionCount: 16, RangeSlots: 8, RangeBytes: 32 << 20, MaxConnections: 12, Attempts: 3, Rounds: 32, RPCTimeout: 500 * time.Millisecond, Backoff: 20 * time.Millisecond, BallotBackoff: 3 * time.Second, ScheduleWindow: 15 * time.Second}}
	}
	n.manifest = peerauth.ControlManifest{Version: 1, Profile: profile, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(5 * time.Minute)}
	raw := n.sign(n.manifest)
	for id, c := range n.configs {
		c.Membership.Profile = profile
		c.Manifest = raw
		if change != nil {
			change(id, &c)
		}
		n.configs[id] = c
	}
	t.Cleanup(func() {
		for _, o := range n.nodes {
			if o != nil {
				_ = o.Close()
			}
		}
		for _, l := range n.listeners {
			_ = l.Close()
		}
	})
	return n
}

func (n *s3aTestNetwork) sign(m peerauth.ControlManifest) []byte {
	n.t.Helper()
	raw, err := peerauth.SignControlManifest(m, n.operator)
	if err != nil {
		n.t.Fatal(err)
	}
	return raw
}

func (n *s3aTestNetwork) start(id uint32) *s3aOwner {
	n.t.Helper()
	o, err := createS3AOwner(n.configs[id])
	if err != nil {
		n.t.Fatal("create", id, err)
	}
	n.nodes[id] = o
	if err := o.Start(n.listeners[id]); err != nil {
		n.t.Fatal("start", id, err)
	}
	return o
}

func (n *s3aTestNetwork) startAll() {
	n.t.Helper()
	for id := uint32(1); id <= 3; id++ {
		n.start(id)
	}
}

func (n *s3aTestNetwork) waitCut(id uint32, slot uint64) s2LocalReceipt {
	n.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, r, err := n.nodes[id].kernel.ReadLocalCut()
		if err == nil && r.ControlSlot >= slot {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, r, err := n.nodes[id].kernel.ReadLocalCut()
	n.t.Fatal("cut did not advance", id, slot, r, err)
	return s2LocalReceipt{}
}

func (n *s3aTestNetwork) origin(id uint32, nonce byte) (FullChangeID, [32]byte) {
	n.t.Helper()
	o := n.nodes[id]
	state, _, err := o.kernel.ReadLocalCut()
	if err != nil {
		n.t.Fatal(err)
	}
	change := s1ReaderRole()
	change.Role.ID = fmt.Sprintf("network-role-%d", nonce)
	op := s1Operation(n.t, state.projection, testIdentity(), s1Changes(change))
	full := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{nonce}}
	raw := s2cTestSeal(n.t, n.f.trust, id, n.f.keys[id], full, op, uint64(nonce), false)
	historical, err := verifyHistoricalH(n.f.trust, raw)
	if err != nil || !historical.handoff.authorization.credentialDeadline.Before(time.Unix(0, n.clock.Load())) {
		n.t.Fatal("fixture did not preserve an expired original H", err)
	}
	digest, err := o.kernel.persistSealedOriginH(raw)
	if err != nil {
		n.t.Fatal(err)
	}
	return full, digest
}

func s3aTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (n *s3aTestNetwork) certificate(identity string) tls.Certificate {
	n.t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		n.t.Fatal(err)
	}
	uri, _ := url.Parse(identity)
	now := time.Unix(0, n.clock.Load())
	template := &x509.Certificate{SerialNumber: big.NewInt(99), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Minute), URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, n.ca, pub, n.caKey)
	if err != nil {
		n.t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
