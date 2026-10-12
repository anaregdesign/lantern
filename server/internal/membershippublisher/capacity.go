package membershippublisher

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// validateCapacity checks currently valid complete chains, not a leaf alone or
// different paths at disconnected instants. It does not load serving TLS or
// give raw operator/voter signing keys a fictional certificate expiry.
func (p *Publisher) validateCapacity(now, cleanup time.Time) (time.Time, error) {
	var capacity time.Time
	for i, s := range p.config.Streams {
		members := p.signers[i].data.Members
		trust := p.signers[i].data.Domain.TrustDigest
		if s.Kind == "control" {
			trust = p.signers[i].control.Profile.TrustDigest
			for _, v := range p.signers[i].control.Profile.Voters {
				members = append(members, v.Workload)
			}
		}
		if len(s.Certificates) != len(members) {
			return time.Time{}, ErrState
		}
		rootPEM, err := readPrivate(s.RootsFile, 1<<20)
		roots := x509.NewCertPool()
		if err != nil || sha256.Sum256(rootPEM) != trust || !roots.AppendCertsFromPEM(rootPEM) {
			return time.Time{}, ErrState
		}
		covered := map[[32]byte]bool{}
		for _, path := range s.Certificates {
			raw, err := readPrivate(path, peerauth.MaxManifestBytes)
			if err != nil {
				return time.Time{}, ErrState
			}
			block, rest := pem.Decode(raw)
			if block == nil || block.Type != "CERTIFICATE" {
				return time.Time{}, ErrState
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return time.Time{}, ErrState
			}
			intermediates := x509.NewCertPool()
			if len(bytes.TrimSpace(rest)) != 0 && !intermediates.AppendCertsFromPEM(rest) {
				return time.Time{}, ErrState
			}
			digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			if covered[digest] || len(cert.URIs) != 1 {
				return time.Time{}, ErrState
			}
			found := false
			for _, member := range members {
				found = found || member.SPKI == digest && member.Identity == cert.URIs[0].String()
			}
			if !found {
				return time.Time{}, ErrState
			}
			covered[digest] = true
			for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
				chains, err := cert.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}})
				if err != nil {
					return time.Time{}, errors.Join(ErrState, ErrCapacity)
				}
				var best time.Time
				for _, chain := range chains {
					end := chain[0].NotAfter
					for _, element := range chain {
						if element.NotAfter.Before(end) {
							end = element.NotAfter
						}
					}
					if end.After(best) {
						best = end
					}
				}
				if !best.After(cleanup.Add(peerauth.ClockMargin)) {
					return time.Time{}, errors.Join(ErrState, ErrCapacity)
				}
				if capacity.IsZero() || best.Before(capacity) {
					capacity = best
				}
			}
		}
	}
	return capacity, nil
}
