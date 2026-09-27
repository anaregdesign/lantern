package provider

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"

	"github.com/anaregdesign/lantern/server/replication"
)

// NewPeerTransport certifies the outbound security boundary before any
// listener can serve a bearer-enabled HA deployment. The inbound certificate
// is required, but its client-CA setting is not an outbound trust store.
func NewPeerTransport(pc PeerConfig, inbound TLSConfig, auth AuthConfig) (*replication.PeerTransport, error) {
	if !auth.Enabled() || (len(pc.Peers) == 0 && pc.Discovery != "dns") {
		return nil, nil
	}
	if pc.Discovery != "static" && pc.Discovery != "dns" {
		return nil, fmt.Errorf("LANTERN_PEER_DISCOVERY %q is not supported with bearer authentication", pc.Discovery)
	}
	if pc.CAFile == "" {
		return nil, errors.New("LANTERN_PEER_CA_FILE is required for bearer-bearing HA peers")
	}
	serverTLS, err := loadTLSConfig(inbound)
	if err != nil {
		return nil, err
	}
	if serverTLS == nil {
		return nil, errors.New("bearer-bearing HA requires LANTERN_TLS_CERT_FILE and LANTERN_TLS_KEY_FILE")
	}
	if (pc.ClientCertFile == "") != (pc.ClientKeyFile == "") {
		return nil, errors.New("LANTERN_PEER_CLIENT_CERT_FILE and LANTERN_PEER_CLIENT_KEY_FILE must both be set")
	}
	if inbound.ClientCAFile != "" && pc.ClientCertFile == "" {
		return nil, errors.New("peer mTLS requires LANTERN_PEER_CLIENT_CERT_FILE and LANTERN_PEER_CLIENT_KEY_FILE")
	}
	var clientCert *tls.Certificate
	if pc.ClientCertFile != "" {
		cert, err := tls.LoadX509KeyPair(pc.ClientCertFile, pc.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load peer client certificate/key: %w", err)
		}
		clientCert = &cert
	}
	caPEM, err := os.ReadFile(pc.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read LANTERN_PEER_CA_FILE: %w", err)
	}
	dnsName, dnsPort, peers := "", "", pc.Peers
	if pc.Discovery == "dns" {
		dnsName, dnsPort, peers = pc.DNSName, pc.DefaultPort, nil
		if len(pc.Peers) > 0 {
			return nil, errors.New("LANTERN_PEERS cannot be combined with bearer-enabled DNS discovery")
		}
	}
	transport, err := replication.NewAuthenticatedPeerTransport(
		caPEM, clientCert, firstToken(auth.Tokens), peers, dnsName, dnsPort,
	)
	if err != nil {
		return nil, fmt.Errorf("configure authenticated peer transport: %w", err)
	}
	return transport, nil
}
