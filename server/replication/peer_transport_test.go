package replication

import (
	"net/http"
	"strings"
	"testing"
)

func TestVerifiedPeerTransportUsesOwnedClientAndCurrentMembership(t *testing.T) {
	eligible := true
	client := &http.Client{Transport: http.DefaultTransport}
	transport, err := NewVerifiedPeerTransport(client, func(origin string) bool { return eligible && origin == "https://localhost:6391" })
	if err != nil {
		t.Fatal(err)
	}
	if url, err := transport.BaseURL("https://localhost:6391"); err != nil || url != "https://localhost:6391" {
		t.Fatalf("current peer origin = %q, %v", url, err)
	}
	if _, err := transport.BaseURL("https://localhost:6380"); err == nil {
		t.Fatal("unapproved origin eligible")
	}
	eligible = false
	if _, err := transport.BaseURL("https://localhost:6391"); err == nil {
		t.Fatal("removed member still eligible")
	}
	eligible = true
	if _, err := transport.BaseURL("https://localhost:6391"); err != nil {
		t.Fatal("current membership not consulted again", err)
	}
	if transport.client != client {
		t.Fatal("certified client replaced")
	}
}

func TestVerifiedPeerTransportRequiresOwnedClientAndMembership(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	for _, tc := range []struct {
		name     string
		client   *http.Client
		approved func(string) bool
	}{
		{"missing client", nil, func(string) bool { return true }},
		{"ambient client", &http.Client{}, func(string) bool { return true }},
		{"missing membership", client, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if transport, err := NewVerifiedPeerTransport(tc.client, tc.approved); err == nil || transport != nil {
				t.Fatal("unowned peer transport accepted")
			}
		})
	}
	for _, transport := range []*PeerTransport{nil, {client: client}} {
		if _, err := transport.BaseURL("https://localhost:6391"); err == nil {
			t.Fatal("missing membership admitted an origin")
		}
	}
}

func TestVerifiedPeerTransportRejectsInvalidOriginsBeforeMembership(t *testing.T) {
	var checked []string
	transport, err := NewVerifiedPeerTransport(&http.Client{Transport: http.DefaultTransport}, func(origin string) bool {
		checked = append(checked, origin)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{
		"https://peer.example:6391",
		"https://127.0.0.1:6391",
		"https://[::1]:6391",
	} {
		if url, err := transport.BaseURL(origin); err != nil || url != origin {
			t.Fatalf("valid workload origin %q = %q, %v", origin, url, err)
		}
	}
	if len(checked) != 3 {
		t.Fatal("valid origins bypassed membership")
	}
	checked = nil
	for _, origin := range []string{
		"",
		"peer.example:6391",
		"http://peer.example:6391",
		"HTTPS://peer.example:6391",
		"https://user:pass@peer.example:6391",
		"https://peer.example:6391/",
		"https://peer.example:6391/snapshot",
		"https://peer.example:6391/%2f",
		"https://peer.example:6391?query=value",
		"https://peer.example:6391?",
		"https://peer.example:6391#fragment",
		"https:peer.example:6391",
		"https://peer.example",
		"https://peer.example:0",
		"https://peer.example:65536",
		"https://peer.example:06391",
		"https://peer.example:not-a-port",
		"https://:6391",
		"https://[fe80::1%25en0]:6391",
		"https://-peer.example:6391",
		"https://peer-.example:6391",
		"https://peer..example:6391",
		"https://peer_name.example:6391",
		"https://" + strings.Repeat("a", 64) + ".example:6391",
		"https://" + strings.Repeat("peer.", 51) + "example:6391",
	} {
		t.Run(origin, func(t *testing.T) {
			if url, err := transport.BaseURL(origin); err == nil || url != "" {
				t.Fatalf("invalid workload origin eligible: %q", origin)
			}
		})
	}
	if len(checked) != 0 {
		t.Fatal("invalid origins reached membership approval", checked)
	}
}
