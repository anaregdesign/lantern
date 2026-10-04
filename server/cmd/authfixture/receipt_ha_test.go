package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiptHAControlPartitionsOnlyData(t *testing.T) {
	control := &receiptHAControl{}
	data := "/graph.v1.LanternReplicationService/Subscribe"
	policy := "/graph.v1.LanternSecurityPeerService/GetPolicySnapshot"
	for _, tc := range []struct {
		caller, target int
		path           string
		allow          bool
	}{
		{1, 2, data, true}, {2, 1, data, true}, {1, 3, data, false}, {3, 2, data, false},
		{0, 1, data, false}, {1, 0, data, false}, {3, 0, policy, true}, {1, 3, policy, true},
		{1, 2, "/graph.v1.LanternService/GetVertex", false},
	} {
		if got := control.allowed(tc.caller, tc.target, tc.path); got != tc.allow {
			t.Fatalf("%+v got %t", tc, got)
		}
	}
	control.connected.Store(true)
	if !control.allowed(3, 2, data) || control.allowed(1, 0, data) || control.allowed(0, 3, data) {
		t.Fatal("relay broadened the policy writer's data plane")
	}
}

func TestReceiptHARelaysPreserveCertifiedIdentityAndTLS(t *testing.T) {
	ports := make([]int, 8)
	held := make([]net.Listener, 8)
	for i := range held {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held[i] = listener
		ports[i] = listener.Addr().(*net.TCPAddr).Port
	}
	for _, listener := range held {
		_ = listener.Close()
	}
	result, err := generate(filepath.Join(t.TempDir(), "trust"), ports[:4], ports[4:], "oidc", "")
	if err != nil {
		t.Fatal(err)
	}
	control := &receiptHAControl{}
	relayed, closeRelays, err := receiptHARelays(result, control)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRelays()
	defer closeRelays()
	raw, err := os.ReadFile(result.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(raw)
	recipient := relayed.Nodes[3]
	cert, err := receiptHACert(recipient)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", recipient.Environment["LANTERN_PEER_LISTEN_ADDR"])
	if err != nil {
		t.Fatal(err)
	}
	backend := &http.Server{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("upstream lost HTTP/2")
		}
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].URIs[0].String())
	})}
	defer backend.Close()
	go func() { _ = backend.ServeTLS(listener, "", "") }()
	caller, err := receiptHACert(relayed.Nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{caller}}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request := func(path string, want int) {
		t.Helper()
		response, err := client.Post(recipient.PeerOrigin+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("status=%d", response.StatusCode)
		}
		if want == 200 && string(body) != relayed.Nodes[1].Environment["LANTERN_PEER_WORKLOAD_ID"] {
			t.Fatal("upstream caller identity changed")
		}
	}
	request("/graph.v1.LanternSecurityPeerService/GetPolicySnapshot", 200)
	request("/graph.v1.LanternReplicationService/Subscribe", 503)
	control.connected.Store(true)
	request("/graph.v1.LanternReplicationService/Subscribe", 200)
	request("/graph.v1.LanternService/GetVertex", 503)
	state := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{caller.Leaf}, VerifiedChains: [][]*x509.Certificate{{caller.Leaf}}}
	if index, ok := control.caller(state); !ok || index != 1 {
		t.Fatal("certified caller rejected")
	}
	state.VerifiedChains = nil
	if _, ok := control.caller(state); ok {
		t.Fatal("unverified caller accepted")
	}
	state.VerifiedChains = [][]*x509.Certificate{{caller.Leaf}}
	control.fingerprints[caller.Leaf.URIs[0].String()] = sha256.Sum256([]byte("wrong-key"))
	if _, ok := control.caller(state); ok {
		t.Fatal("wrong SPKI accepted")
	}
}
