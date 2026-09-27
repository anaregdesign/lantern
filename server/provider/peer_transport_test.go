package provider

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func peerTLSFixture(t *testing.T) (TLSConfig, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	cert := srv.TLS.Certificates[0]
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server.key")
	caFile := filepath.Join(dir, "ca.pem")
	for _, file := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o644},
		{keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600},
		{caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o644},
	} {
		if err := os.WriteFile(file.path, file.data, file.mode); err != nil {
			t.Fatal(err)
		}
	}
	return TLSConfig{CertFile: certFile, KeyFile: keyFile}, caFile
}

func TestNewPeerTransportRequiresTLSAndTrustedOrigins(t *testing.T) {
	inbound, caFile := peerTLSFixture(t)
	peer := PeerConfig{
		Discovery: "static", Peers: []string{"https://peer.example:6380"}, CAFile: caFile,
	}
	auth := AuthConfig{Tokens: []string{"cluster-token"}}
	if transport, err := NewPeerTransport(peer, inbound, auth); err != nil || transport == nil {
		t.Fatalf("valid HTTPS peer transport = (%v, %v)", transport, err)
	}
	if transport, err := NewPeerTransport(peer, TLSConfig{}, AuthConfig{}); err != nil || transport != nil {
		t.Fatalf("bearer-free graph-only peer = (%v, %v)", transport, err)
	}
	if transport, err := NewPeerTransport(PeerConfig{Discovery: "static"}, TLSConfig{}, auth); err != nil || transport != nil {
		t.Fatalf("single-node bearer without peer TLS = (%v, %v)", transport, err)
	}

	for _, tc := range []struct {
		name    string
		peer    PeerConfig
		inbound TLSConfig
		want    string
	}{
		{"missing outbound CA", PeerConfig{Discovery: "static", Peers: peer.Peers}, inbound, "LANTERN_PEER_CA_FILE"},
		{"missing inbound TLS", peer, TLSConfig{}, "LANTERN_TLS_CERT_FILE"},
		{"incomplete inbound TLS", peer, TLSConfig{CertFile: inbound.CertFile}, "must both be set"},
		{"invalid outbound CA", PeerConfig{Discovery: "static", Peers: peer.Peers, CAFile: inbound.KeyFile}, inbound, "no valid PEM certificates"},
		{"plaintext static URL", PeerConfig{Discovery: "static", Peers: []string{"http://peer.example:6380"}, CAFile: caFile}, inbound, "explicit https://"},
		{"bare static URL", PeerConfig{Discovery: "static", Peers: []string{"peer.example:6380"}, CAFile: caFile}, inbound, "explicit https://"},
		{"DNS without identity", PeerConfig{Discovery: "dns", CAFile: caFile, DefaultPort: "6380"}, inbound, "DNS discovery identity"},
		{"DNS without valid port", PeerConfig{Discovery: "dns", CAFile: caFile, DNSName: "lantern.example"}, inbound, "DNS discovery port"},
		{"ambiguous discovery", PeerConfig{Discovery: "dns", CAFile: caFile, DNSName: "lantern.example", DefaultPort: "6380", Peers: peer.Peers}, inbound, "cannot be combined"},
		{"missing mTLS certificate", peer, TLSConfig{CertFile: inbound.CertFile, KeyFile: inbound.KeyFile, ClientCAFile: caFile}, "peer mTLS requires"},
		{"incomplete outbound mTLS", PeerConfig{Discovery: "static", Peers: peer.Peers, CAFile: caFile, ClientCertFile: inbound.CertFile}, inbound, "must both be set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := NewPeerTransport(tc.peer, tc.inbound, auth); err == nil || got != nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewPeerTransport = (%v, %v), want %q", got, err, tc.want)
			}
		})
	}
	mutual := peer
	mutual.ClientCertFile, mutual.ClientKeyFile = inbound.CertFile, inbound.KeyFile
	inbound.ClientCAFile = caFile
	if transport, err := NewPeerTransport(mutual, inbound, auth); err != nil || transport == nil {
		t.Fatalf("mutual TLS configuration = (%v, %v)", transport, err)
	}
}
