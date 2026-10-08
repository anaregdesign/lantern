package security

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/privatefile"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

type s3aIdentityFiles struct {
	Roots, Certificate, TLSKey, VotingKey string
}

type s3aIdentity struct {
	certificate tls.Certificate
	roots       *x509.CertPool
	self        peerauth.Member
	expires     time.Time
	votingKey   ed25519.PrivateKey
	signer      *s3aSigner
}

func s3aReadProvisioned(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errS3AConfig
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errS3AConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || privatefile.Check(f) != nil {
		return nil, errS3AConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errS3AConfig
	}
	return raw, nil
}

func s3aLoadIdentity(c s3aConfig) (_ *s3aIdentity, err error) {
	p, t := c.Membership.Profile, c.Participant.Trust
	if t == nil || !p.Valid() || p.ProtocolScope != t.scope || len(p.Voters) != len(t.members) || len(c.Participant.Key) != 0 {
		return nil, errS3AConfig
	}
	var self peerauth.Member
	for _, v := range p.Voters {
		m, ok := t.member(v.Voter)
		if !ok || m.PublicKey != v.Key || m.Proposer != v.Proposer {
			return nil, errS3AConfig
		}
		if v.Voter == c.Participant.Member {
			self = v.Workload
		}
	}
	if self == (peerauth.Member{}) || c.Membership.Self != self {
		return nil, errS3AConfig
	}
	rootsPEM, err := s3aReadProvisioned(c.Identity.Roots, 1<<20)
	if err != nil || sha256.Sum256(rootsPEM) != p.TrustDigest {
		return nil, errS3AConfig
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootsPEM) {
		return nil, errS3AConfig
	}
	certPEM, err := s3aReadProvisioned(c.Identity.Certificate, 64<<10)
	if err != nil {
		return nil, err
	}
	keyPEM, err := s3aReadProvisioned(c.Identity.TLSKey, 64<<10)
	if err != nil {
		return nil, err
	}
	defer clear(keyPEM)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(cert.Certificate) == 0 {
		return nil, errS3AConfig
	}
	key, ok := cert.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errS3AConfig
	}
	signer := &s3aSigner{key: key, public: key.Public()}
	transferred := false
	defer func() {
		if !transferred {
			signer.close()
		}
	}()
	cert.PrivateKey = signer
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || len(leaf.URIs) != 1 || leaf.URIs[0].String() != self.Identity || sha256.Sum256(leaf.RawSubjectPublicKeyInfo) != self.SPKI {
		return nil, errS3AConfig
	}
	cert.Leaf = leaf
	u, err := url.Parse(self.Origin)
	if err != nil || leaf.VerifyHostname(u.Hostname()) != nil {
		return nil, errS3AConfig
	}
	intermediates := x509.NewCertPool()
	for _, raw := range cert.Certificate[1:] {
		parsed, err := x509.ParseCertificate(raw)
		if err != nil {
			return nil, errS3AConfig
		}
		intermediates.AddCert(parsed)
	}
	expiry := leaf.NotAfter
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
		chains, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: c.Membership.Now(), KeyUsages: []x509.ExtKeyUsage{usage}})
		if err != nil {
			return nil, errS3AConfig
		}
		latest := time.Time{}
		for _, chain := range chains {
			end := leaf.NotAfter
			for _, certificate := range chain {
				if certificate.NotAfter.Before(end) {
					end = certificate.NotAfter
				}
			}
			if end.After(latest) {
				latest = end
			}
		}
		if latest.Before(expiry) {
			expiry = latest
		}
	}
	voting, err := s3aReadProvisioned(c.Identity.VotingKey, ed25519.PrivateKeySize)
	if err != nil || len(voting) != ed25519.PrivateKeySize {
		clear(voting)
		return nil, errS3AConfig
	}
	m, _ := t.member(c.Participant.Member)
	if !bytes.Equal(ed25519.PrivateKey(voting).Public().(ed25519.PublicKey), m.PublicKey[:]) ||
		!bytes.Equal(ed25519.NewKeyFromSeed(voting[:ed25519.SeedSize]), voting) ||
		bytes.Equal(leaf.RawSubjectPublicKeyInfo, s3aVotingSPKI(m.PublicKey)) {
		clear(voting)
		return nil, errS3AConfig
	}
	transferred = true
	return &s3aIdentity{certificate: cert, roots: roots, self: self, expires: expiry.Add(-peerauth.ClockMargin), votingKey: voting, signer: signer}, nil
}

func s3aVotingSPKI(key [32]byte) []byte {
	raw, _ := x509.MarshalPKIXPublicKey(ed25519.PublicKey(key[:]))
	return raw
}

func (i *s3aIdentity) clear() {
	if i == nil {
		return
	}
	clear(i.votingKey)
	i.votingKey = nil
	if i.signer != nil {
		i.signer.close()
	}
}

// TLS may still be unwinding a handshake when Server.Close returns. The owned
// signer serializes its last Sign with key erasure; no network goroutine can
// race key clearing or use a closed signer retained in a TLS config copy.
type s3aSigner struct {
	mu     sync.Mutex
	key    crypto.Signer
	public crypto.PublicKey
}

func (s *s3aSigner) Public() crypto.PublicKey { return s.public }

func (s *s3aSigner) Sign(r io.Reader, digest []byte, options crypto.SignerOpts) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return nil, errS3AClosed
	}
	return s.key.Sign(r, digest, options)
}

func (s *s3aSigner) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	zero := func(n *big.Int) {
		if n != nil {
			clear(n.Bits())
			n.SetInt64(0)
		}
	}
	switch key := s.key.(type) {
	case ed25519.PrivateKey:
		clear(key)
	case *ecdsa.PrivateKey:
		zero(key.D)
	case *rsa.PrivateKey:
		zero(key.D)
		for _, prime := range key.Primes {
			zero(prime)
		}
		zero(key.Precomputed.Dp)
		zero(key.Precomputed.Dq)
		zero(key.Precomputed.Qinv)
		for _, value := range key.Precomputed.CRTValues {
			zero(value.Exp)
			zero(value.Coeff)
			zero(value.R)
		}
	}
	s.key = nil
}
