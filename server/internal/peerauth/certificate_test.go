package peerauth

import (
	"crypto/x509"
	"testing"
	"time"
)

func TestCertificateWindowSelectsOneWholeVerifiedChain(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{Raw: []byte("leaf"), NotBefore: now, NotAfter: now.Add(time.Hour)}
	tooLate := &x509.Certificate{NotBefore: now.Add(time.Second), NotAfter: now.Add(2 * time.Hour)}
	short := &x509.Certificate{NotBefore: now.Add(-time.Second), NotAfter: now.Add(time.Minute)}
	window, err := SelectCertificateWindow([][]*x509.Certificate{{leaf, tooLate}, {leaf, short}}, leaf, now, now.Add(time.Second))
	if err != nil || window.Start != now || window.End != short.NotAfter {
		t.Fatal("mixed alternate chain windows", window, err)
	}
	if _, err = SelectCertificateWindow([][]*x509.Certificate{{leaf}}, leaf, now.Add(-time.Nanosecond), now.Add(time.Second)); err == nil {
		t.Fatal("high-only NotBefore admitted")
	}
	if window.Valid(now, short.NotAfter.Add(-ClockMargin)) {
		t.Fatal("expiry equality admitted")
	}
	if !window.Valid(now, short.NotAfter.Add(-ClockMargin-time.Nanosecond)) {
		t.Fatal("strict valid edge rejected")
	}
}
