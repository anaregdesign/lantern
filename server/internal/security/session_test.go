package security

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestNativeCredentialAndSessionProfile(t *testing.T) {
	raw := NativeCredentialPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	digest, ok := NativeCredentialDigest(raw)
	if !ok {
		t.Fatal("canonical credential rejected")
	}
	browser := sha256.Sum256([]byte(raw))
	if digest == hex.EncodeToString(browser[:]) {
		t.Fatal("native digest shares cookie domain")
	}
	for _, input := range []string{raw + "=", " " + raw, raw + "\n", strings.TrimPrefix(raw, NativeCredentialPrefix), raw[:len(raw)-1] + "B", "lnm1_" + raw[5:]} {
		if _, ok := NativeCredentialDigest(input); ok {
			t.Fatal("malformed/foreign credential accepted")
		}
	}
	now := time.Now().UTC().Round(0)
	s := Session{Profile: NativeEndUserProfile, Digest: digest, Identity: Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "subject"}, IssuerConfigRevision: 1, ClientProfileID: "mobile", ServiceOrigin: "https://lantern.example", CreatedAt: now, ExpiresAt: now.Add(MaxSessionLifetime)}
	if !s.valid() {
		t.Fatal("native session rejected")
	}
	for _, change := range []func(*Session){
		func(s *Session) { s.CSRFDigest = digest }, func(s *Session) { s.Profile = BrowserCookieProfile },
		func(s *Session) { s.Profile = "" }, func(s *Session) { s.ClientProfileID = "" },
		func(s *Session) { s.ExpiresAt = s.ExpiresAt.Add(time.Nanosecond) }, func(s *Session) { s.AuthTime = now.Add(time.Second) },
	} {
		invalid := s
		change(&invalid)
		if invalid.valid() {
			t.Fatal("invalid session profile accepted")
		}
	}
	legacy := s
	legacy.Profile = ""
	legacy.ClientProfileID = ""
	legacy.ServiceOrigin = ""
	if !legacy.valid() || legacy.CredentialProfile() != BrowserCookieProfile {
		t.Fatal("historical browser classification changed")
	}
}

func BenchmarkNativeCredentialDigest(b *testing.B) {
	raw := NativeCredentialPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := NativeCredentialDigest(raw); !ok {
			b.Fatal("native credential rejected")
		}
	}
}

// The credential parsing/immutable lookup part of native authentication. The
// paired warm-admission test independently guards current-owner I/O/final Write.
func BenchmarkNativeSessionLookup(b *testing.B) {
	image, session, raw := nativeSessionBenchmarkImage()
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		b.Fatal(err)
	}
	now := session.CreatedAt.Add(time.Minute)
	b.ReportAllocs()
	for b.Loop() {
		digest, ok := NativeCredentialDigest(raw)
		if !ok {
			b.Fatal("native credential rejected")
		}
		if _, _, ok := snapshot.SessionAccess(digest, now); !ok {
			b.Fatal("native session lookup rejected")
		}
		if s, ok := snapshot.Session(digest); !ok || s.CredentialProfile() != NativeEndUserProfile {
			b.Fatal("native credential profile lost")
		}
	}
}
