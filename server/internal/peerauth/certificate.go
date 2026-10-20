package peerauth

import (
	"crypto/x509"
	"time"
)

// CertificateWindow retains one selected verified chain, never a union of
// different chains. End is the unmodified certificate expiry.
type CertificateWindow struct{ Start, End time.Time }

func (w CertificateWindow) Valid(low, high time.Time) bool {
	return !w.Start.IsZero() && !w.End.IsZero() && !low.Before(w.Start) && high.Add(ClockMargin).Before(w.End)
}

// SelectCertificateWindow requires the whole interval in every certificate of
// one verified chain for this exact leaf, preferring the latest usable expiry.
func SelectCertificateWindow(chains [][]*x509.Certificate, leaf *x509.Certificate, low, high time.Time) (CertificateWindow, error) {
	var selected CertificateWindow
	for _, chain := range chains {
		if len(chain) == 0 || leaf == nil || !chain[0].Equal(leaf) {
			continue
		}
		window := CertificateWindow{chain[0].NotBefore, chain[0].NotAfter}
		for _, cert := range chain {
			if cert.NotBefore.After(window.Start) {
				window.Start = cert.NotBefore
			}
			if cert.NotAfter.Before(window.End) {
				window.End = cert.NotAfter
			}
		}
		if window.Valid(low, high) && window.End.After(selected.End) {
			selected = window
		}
	}
	if selected.End.IsZero() {
		return CertificateWindow{}, ErrMembership
	}
	return selected, nil
}
