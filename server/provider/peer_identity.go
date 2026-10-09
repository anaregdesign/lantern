package provider

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// PeerIdentityRuntime is staged trusted composition. It certifies one
// independently durable membership/certificate boundary; it does not certify
// the security-policy lease or enable the production listener by itself.
type PeerIdentityRuntime struct {
	config      PeerIdentityConfig
	store       *peerauth.Store
	certificate tls.Certificate
	roots       *x509.CertPool
	selfExpiry  time.Time
}

func NewPeerIdentityRuntime(config PeerIdentityConfig) (_ *PeerIdentityRuntime, cleanup func(), err error) {
	if config.SelfIdentity == "" || config.StateMode != "fresh" && config.StateMode != "resume" {
		return nil, nil, peerauth.ErrMembership
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ca, err := readSecurityOperatorFile(config.CAFile, false, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, nil, peerauth.ErrMembership
	}
	key, err := loadSecurityWriterPublicKey(config.OperatorKeyFile)
	if err != nil {
		return nil, nil, err
	}
	raw, err := readSecurityOperatorFile(config.ManifestFile, false, peerauth.MaxManifestBytes)
	if err != nil {
		return nil, nil, err
	}
	domain := peerauth.Domain{Deployment: config.Deployment, NamespaceFormat: keyspace.Version, AuthMode: config.AuthMode, SecurityGeneration: config.SecurityGeneration, WriterPublicKey: config.WriterPublicKey, TrustDigest: sha256.Sum256(ca), CurrentProfile: config.CurrentProfile}
	manifest, err := peerauth.VerifyManifest(raw, key, domain)
	if err != nil {
		return nil, nil, err
	}
	certPEM, err := readSecurityOperatorFile(config.CertFile, false, 64<<10)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := readSecurityOperatorFile(config.KeyFile, true, 64<<10)
	if err != nil {
		return nil, nil, err
	}
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(certificate.Certificate) == 0 {
		return nil, nil, peerauth.ErrMembership
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || len(leaf.URIs) != 1 || leaf.URIs[0].String() != config.SelfIdentity {
		return nil, nil, peerauth.ErrMembership
	}
	certificate.Leaf = leaf
	var self peerauth.Member
	for _, member := range manifest.Members {
		if member.Identity == config.SelfIdentity {
			self = member
		}
	}
	if self == (peerauth.Member{}) || self.SPKI != sha256.Sum256(leaf.RawSubjectPublicKeyInfo) {
		return nil, nil, peerauth.ErrMembership
	}
	intermediates := x509.NewCertPool()
	for _, encoded := range certificate.Certificate[1:] {
		cert, err := x509.ParseCertificate(encoded)
		if err != nil {
			return nil, nil, peerauth.ErrMembership
		}
		intermediates.AddCert(cert)
	}
	selfExpiry := leaf.NotAfter
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
		chains, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: config.Now(), KeyUsages: []x509.ExtKeyUsage{usage}})
		if err != nil {
			return nil, nil, peerauth.ErrMembership
		}
		latest := time.Time{}
		for _, chain := range chains {
			expiry := leaf.NotAfter
			for _, cert := range chain {
				if cert.NotAfter.Before(expiry) {
					expiry = cert.NotAfter
				}
			}
			if expiry.After(latest) {
				latest = expiry
			}
		}
		if latest.Before(selfExpiry) {
			selfExpiry = latest
		}
	}
	origin, err := url.Parse(self.Origin)
	if err != nil || leaf.VerifyHostname(origin.Hostname()) != nil {
		return nil, nil, peerauth.ErrMembership
	}
	options := peerauth.StoreOptions{Path: config.StateFile, Key: key, Domain: domain, Self: self, Now: config.Now}
	var store *peerauth.Store
	if config.StateMode == "fresh" {
		store, err = peerauth.CreateStore(options, raw)
	} else {
		store, err = peerauth.ResumeStore(options)
		if err == nil {
			err = store.Apply(raw)
		}
	}
	if err != nil {
		if store != nil {
			err = errors.Join(err, store.Close())
		}
		return nil, nil, err
	}
	if _, _, err := store.Member(self.Identity); err != nil {
		return nil, nil, errors.Join(err, store.Close())
	}
	runtime := &PeerIdentityRuntime{config: config, store: store, certificate: certificate, roots: roots, selfExpiry: selfExpiry.Add(-peerauth.ClockMargin)}
	if runtime.CheckWorkload(context.Background()) != nil {
		return nil, nil, errors.Join(peerauth.ErrMembership, store.Close())
	}
	return runtime, func() { _ = store.Close() }, nil
}

func (r *PeerIdentityRuntime) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{r.certificate},
		ClientCAs: r.roots.Clone(), ClientAuth: tls.RequireAndVerifyClientCert, NextProtos: []string{"h2", "http/1.1"},
		SessionTicketsDisabled: true, VerifyConnection: func(state tls.ConnectionState) error {
			_, err := r.store.Admit(&state, "")
			return err
		}}
}

func (r *PeerIdentityRuntime) ProtectPeerHTTPHandler(next http.Handler, options ...connect.HandlerOption) http.Handler {
	return r.store.ProtectHandler(next, options...)
}

func (r *PeerIdentityRuntime) ReloadMembership() error {
	raw, err := readSecurityOperatorFile(r.config.ManifestFile, false, peerauth.MaxManifestBytes)
	if err != nil {
		return err
	}
	return r.store.Apply(raw)
}

func (r *PeerIdentityRuntime) ApprovedOrigins() ([]string, error) { return r.store.Origins() }

// HTTPClient carries workload certificates only. It cannot inherit browser or
// machine credentials, environment proxies, redirects or plaintext fallback.
func (r *PeerIdentityRuntime) HTTPClient() *http.Client {
	return peerauth.NewHTTPClient(r.store, r.certificate, r.roots)
}

func (r *PeerIdentityRuntime) CheckWorkload(ctx context.Context) error {
	if r == nil || r.store == nil {
		return peerauth.ErrMembership
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.config.Now().Before(r.selfExpiry) {
		return peerauth.ErrMembership
	}
	_, _, err := r.store.Member(r.config.SelfIdentity)
	return err
}
