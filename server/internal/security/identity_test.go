package security

import (
	"strings"
	"testing"
)

func TestExactIdentityValidation(t *testing.T) {
	for _, issuer := range []string{"https://idp.example", "https://idp.example/", "https://idp.example/realm", "https://idp.example:443/realm"} {
		for _, subject := range []string{"User1", "user1", " user1 ", strings.Repeat("a", 255)} {
			identity := Identity{Kind: OIDCPrincipal, Issuer: issuer, Subject: subject}
			if !identity.valid() {
				t.Fatalf("valid identity rejected: %v", identity)
			}
		}
	}
	for _, issuer := range []string{"", "http://idp.example", "https://user:pass@idp.example", "https://idp.example?x=y", "https://idp.example?", "https://idp.example#", "https://idp.example#fragment", "https://idp.example:0", "https://idp.example:65536", "https://idp.example:bad", "https:///realm"} {
		if (Identity{Kind: OIDCPrincipal, Issuer: issuer, Subject: "user1"}).valid() {
			t.Fatalf("invalid Issuer admitted: %q", issuer)
		}
	}
	for _, subject := range []string{"", strings.Repeat("a", 256), "日本語", "\xff"} {
		identity := testIdentity()
		identity.Subject = subject
		if identity.valid() {
			t.Fatalf("invalid subject admitted: %q", subject)
		}
	}
	if !(Identity{Kind: MachinePrincipal, MachineName: "service_1"}).valid() {
		t.Fatal("machine identity rejected")
	}
	for _, identity := range []Identity{{Kind: "unknown"}, {Kind: MachinePrincipal}, {Kind: MachinePrincipal, MachineName: "service_1", Subject: "user1"}, {Kind: OIDCPrincipal, Issuer: testIdentity().Issuer, Subject: "user1", MachineName: "service_1"}} {
		if identity.valid() {
			t.Fatal("ambiguous identity admitted")
		}
	}
}
