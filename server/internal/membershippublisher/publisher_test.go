package membershippublisher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

func publisherFixture(t *testing.T) (string, string, Config, time.Time) {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	now := time.Now().UTC().Truncate(time.Second)
	var members []peerauth.Member
	var voters []peerauth.ControlVoter
	var certs []string
	var rootPEM []byte
	for i := 1; i <= 3; i++ {
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		uri, _ := url.Parse("spiffe://renew.test/node" + string(rune('0'+i)))
		template := &x509.Certificate{SerialNumber: big.NewInt(int64(i)), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature}
		der, e := x509.CreateCertificate(rand.Reader, template, template, pub, key)
		if e != nil {
			t.Fatal(e)
		}
		cert, _ := x509.ParseCertificate(der)
		member := peerauth.Member{ID: [16]byte{byte(i)}, Identity: uri.String(), SPKI: sha256.Sum256(cert.RawSubjectPublicKeyInfo), Origin: "https://127.0.0.1:1700" + string(rune('0'+i))}
		members = append(members, member)
		vote, _, _ := ed25519.GenerateKey(rand.Reader)
		voters = append(voters, peerauth.ControlVoter{Workload: member, Voter: uint32(i), Key: [32]byte(vote), Proposer: true})
		rootPEM = append(rootPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
		certs = append(certs, write("cert"+string(rune('0'+i))+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	}
	rootPath := write("roots.pem", rootPEM)
	trust := sha256.Sum256(rootPEM)
	c := Config{Version: 1, End: now.Add(15 * time.Minute), CleanupBy: now.Add(16 * time.Minute), Interval: 5 * time.Second, Lifetime: 30 * time.Second}
	for _, kind := range []string{"control", "data"} {
		_, key, _ := ed25519.GenerateKey(rand.Reader)
		der, _ := x509.MarshalPKCS8PrivateKey(key)
		keyPath := write(kind+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		var raw []byte
		if kind == "control" {
			m := peerauth.ControlManifest{Version: 1, Profile: peerauth.ControlProfile{Version: 1, Lineage: peerauth.ControlLineage{Deployment: [16]byte{1}, Instance: [16]byte{2}}, NamespaceFormat: "namespaced-v1", ProtocolScope: [32]byte{3}, TrustDigest: trust, Voters: voters}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-30 * time.Second)}
			raw, e = peerauth.SignControlManifest(m, key)
		} else {
			m := peerauth.Manifest{Version: 1, Domain: peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "off", TrustDigest: trust}, Members: members, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-30 * time.Second)}
			raw, e = peerauth.SignManifest(m, key)
		}
		if e != nil {
			t.Fatal(e)
		}
		c.Streams = append(c.Streams, Stream{Kind: kind, KeyFile: keyPath, RootsFile: rootPath, Initial: raw, Certificates: certs, Targets: []string{write(kind+"-a", raw), write(kind+"-b", raw)}})
	}
	raw, _ := json.Marshal(c)
	return write("config.json", raw), filepath.Join(dir, "state.json"), c, now
}

func TestPublisherPartialDistributionExactResumeAndBudget(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	// A second journal cannot compete for the same publication targets.
	if other, e := Open(config, state+".other", "init"); e == nil {
		other.Close()
		t.Fatal("second publisher admitted")
	}
	base := p.files.rename
	p.files.rename = func(a, b string) error {
		if b != state {
			raw, e := readPrivate(state, maxStateBytes)
			if e != nil {
				t.Fatal(e)
			}
			var j journal
			if decode(raw, &j) != nil {
				t.Fatal("unreadable candidate")
			}
			found := false
			for _, s := range j.Streams {
				if s.Pending && s.Version == 2 && s.Digest == sha256.Sum256(s.Current) {
					found = true
				}
			}
			if !found {
				t.Fatal("publication preceded durable exact candidate")
			}
		}
		if b == c.Streams[0].Targets[1] {
			return injected
		}
		return base(a, b)
	}
	if e = p.Step(now); !errors.Is(e, ErrPublication) {
		t.Fatal(e)
	}
	control := append([]byte{}, p.state.Streams[0].Current...)
	if !p.state.Streams[0].Pending || p.state.Streams[1].Pending || p.state.Streams[1].Version != 2 {
		t.Fatal("partial control blocked data")
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	p, e = Open(config, state, "resume")
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	// The saved pending control interval is expired here. Resume still sends
	// those exact bytes first, then a later step may issue its next revision.
	if e = p.Step(now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	for _, target := range c.Streams[0].Targets {
		raw, _ := os.ReadFile(target)
		if !bytes.Equal(raw, control) {
			t.Fatal("pending revision was re-signed")
		}
	}
	if e = p.Step(now.Add(time.Minute + time.Second)); e != nil {
		t.Fatal(e)
	}
	if p.state.Streams[0].Version != 3 {
		t.Fatal("expired predecessor could not renew")
	}
	before, _ := os.ReadFile(state)
	if e = p.Step(c.End); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(state)
	if !bytes.Equal(before, after) {
		t.Fatal("budget generated another signature")
	}
}

func TestPublisherUncertainJournalAndKnownStateLoss(t *testing.T) {
	for _, fail := range []string{"rename", "directory"} {
		t.Run(fail, func(t *testing.T) {
			config, state, c, now := publisherFixture(t)
			p, e := Open(config, state, "init")
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			if fail == "rename" {
				base := p.files.rename
				p.files.rename = func(a, b string) error {
					if b == state {
						return injected
					}
					return base(a, b)
				}
			} else {
				p.files.directory = func(string) error { return injected }
			}
			if !errors.Is(p.Step(now), ErrState) || !errors.Is(p.Step(now.Add(time.Second)), ErrState) {
				t.Fatal("uncertain write did not latch")
			}
			for _, s := range c.Streams {
				for _, path := range s.Targets {
					raw, _ := os.ReadFile(path)
					if !bytes.Equal(raw, s.Initial) {
						t.Fatal("distributed before durable journal")
					}
				}
			}
			p.Close()
			p, e = Open(config, state, "resume")
			if e != nil {
				t.Fatal("intact retained journal refused", e)
			}
			defer p.Close()
			if e = p.Step(now.Add(time.Second)); e != nil {
				t.Fatal(e)
			}
			p.Close()
			if e = os.Remove(state); e != nil {
				t.Fatal(e)
			}
			for _, mode := range []string{"init", "resume"} {
				if q, e := Open(config, state, mode); e == nil {
					q.Close()
					t.Fatal("lost state recreated", mode)
				}
			}
		})
	}
}

func TestPublisherRejectsDriftAndCertificateCapacity(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if e = p.Step(now); e != nil {
		t.Fatal(e)
	}
	p.Close()
	if e = os.WriteFile(c.Streams[0].Targets[0], c.Streams[0].Initial, 0600); e != nil {
		t.Fatal(e)
	}
	if q, e := Open(config, state, "resume"); e == nil {
		q.Close()
		t.Fatal("rolled-back target accepted")
	}
	c.CleanupBy = now.Add(2 * time.Hour)
	raw, _ := json.Marshal(c)
	if e = os.WriteFile(config, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if q, e := Open(config, state+".new", "init"); e == nil {
		q.Close()
		t.Fatal("insufficient certificate lifetime")
	}
}

func TestPublisherOverflowAliasAndNoncanonicalState(t *testing.T) {
	for _, kind := range []string{"overflow", "alias", "noncanonical"} {
		t.Run(kind, func(t *testing.T) {
			config, state, c, _ := publisherFixture(t)
			if kind == "noncanonical" {
				p, e := Open(config, state, "init")
				if e != nil {
					t.Fatal(e)
				}
				p.Close()
				raw, e := os.ReadFile(state)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(state, append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
				if p, e := Open(config, state, "resume"); e == nil {
					p.Close()
					t.Fatal("noncanonical retained record accepted")
				}
				return
			}
			if kind == "overflow" {
				c.Interval = time.Duration(1 << 62)
			} else {
				c.Streams[1].Targets[0] = c.Streams[0].Targets[0]
			}
			raw, _ := json.Marshal(c)
			if e := os.WriteFile(config, raw, 0600); e != nil {
				t.Fatal(e)
			}
			if p, e := Open(config, state, "init"); e == nil {
				p.Close()
				t.Fatal("invalid immutable plan accepted")
			}
		})
	}
}

func TestPublisherTargetDirectoryUncertaintyReusesExactCandidate(t *testing.T) {
	config, state, c, now := publisherFixture(t)
	p, e := Open(config, state, "init")
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	base := p.files.rename
	last := ""
	p.files.rename = func(a, b string) error { last = b; return base(a, b) }
	directory := p.files.directory
	p.files.directory = func(path string) error {
		if last == c.Streams[0].Targets[0] {
			return injected
		}
		return directory(path)
	}
	if !errors.Is(p.Step(now), ErrPublication) {
		t.Fatal("directory uncertainty was counted as durable publication")
	}
	saved := append([]byte{}, p.state.Streams[0].Current...)
	p.files = systemFiles()
	if e = p.Step(now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(saved, p.state.Streams[0].Current) || p.state.Streams[0].Pending {
		t.Fatal("uncertain target caused new signing")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(p.Run(ctx), context.Canceled) {
		t.Fatal("canceled run continued")
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(p.Step(now.Add(2*time.Second)), ErrState) {
		t.Fatal("closed publisher accepted work")
	}
}
