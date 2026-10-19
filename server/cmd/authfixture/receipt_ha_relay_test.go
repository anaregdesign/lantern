package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReceiptHARelaysPreserveCertifiedIdentityAndTLS(t *testing.T) {
	held := make([]net.Listener, 12)
	ports := make([]int, 12)
	for i := range held {
		var err error
		held[i], err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held[i].Close() }()
		ports[i] = held[i].Addr().(*net.TCPAddr).Port
	}
	f, err := generate(filepath.Join(t.TempDir(), "trust"), ports[:4], ports[4:8], "oidc", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Nodes {
		f.Nodes[i].Environment["LANTERN_PEER_LISTEN_ADDR"] = held[i+8].Addr().String()
	}
	control := &receiptHAControl{}
	r, err := receiptHARelays(f, control, held[4:8])
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	if err = r.resume(f); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(raw)
	cert, err := receiptHACert(f.Nodes[3])
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	backend := &http.Server{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.ProtoMajor != 2 {
			t.Error("upstream lost HTTP/2")
		}
		if req.Header.Get("X-Fixture-Block") == "1" {
			blocked <- struct{}{}
			<-req.Context().Done()
			cancelled <- struct{}{}
			return
		}
		_, _ = io.WriteString(w, req.TLS.PeerCertificates[0].URIs[0].String())
	})}
	defer func() { _ = backend.Close() }()
	go func() { _ = backend.ServeTLS(held[11], "", "") }()
	caller, err := receiptHACert(f.Nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{caller}}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request := func(path string, want int) {
		t.Helper()
		response, err := client.Post(f.Nodes[3].PeerOrigin+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("status=%d want=%d", response.StatusCode, want)
		}
		if want == 200 && string(body) != f.Nodes[1].Environment["LANTERN_PEER_WORKLOAD_ID"] {
			t.Fatal("caller identity changed")
		}
	}
	data := "/graph.v1.LanternReplicationService/Subscribe"
	request("/graph.v1.LanternSecurityPeerService/GetPolicySnapshot", 503)
	request(data, 503)
	control.connected.Store(true)
	request(data, 200)
	request("/graph.v1.LanternService/GetVertex", 503)
	config := r.targets[3].transports[1].TLSClientConfig
	if config.InsecureSkipVerify || config.MinVersion != tls.VersionTLS13 {
		t.Fatal("TLS verification weakened")
	}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert.Leaf}, VerifiedChains: [][]*x509.Certificate{{cert.Leaf}}}
	if err = config.VerifyConnection(state); err != nil {
		t.Fatal(err)
	}
	state.PeerCertificates = []*x509.Certificate{caller.Leaf}
	if err = config.VerifyConnection(state); err == nil {
		t.Fatal("wrong recipient URI accepted")
	}
	wrongKey := *cert.Leaf
	wrongKey.RawSubjectPublicKeyInfo = []byte("same URI wrong key")
	state.PeerCertificates = []*x509.Certificate{&wrongKey}
	if err = config.VerifyConnection(state); err == nil {
		t.Fatal("wrong recipient SPKI accepted")
	}
	state.VerifiedChains = nil
	if err = config.VerifyConnection(state); err == nil {
		t.Fatal("unverified recipient accepted")
	}
	state = tls.ConnectionState{PeerCertificates: []*x509.Certificate{caller.Leaf}, VerifiedChains: [][]*x509.Certificate{{caller.Leaf}}}
	if index, ok := control.caller(&state); !ok || index != 1 {
		t.Fatal("caller rejected")
	}
	wrongCaller := &receiptHAControl{identities: control.identities, fingerprints: maps.Clone(control.fingerprints)}
	wrongCaller.fingerprints[caller.Leaf.URIs[0].String()] = sha256.Sum256([]byte("wrong"))
	if _, ok := wrongCaller.caller(&state); ok {
		t.Fatal("wrong caller SPKI accepted")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		req, _ := http.NewRequest(http.MethodPost, f.Nodes[3].PeerOrigin+data, strings.NewReader("{}"))
		req.Header.Set("X-Fixture-Block", "1")
		response, err := client.Do(req)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking request did not enter original backend")
	}
	r.pause()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("backend request survived retired generation")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("downstream request survived retired generation")
	}
	for _, l := range held[4:8] {
		if conn, e := net.Listen("tcp", l.Addr().String()); e == nil {
			_ = conn.Close()
			t.Fatal("relay endpoint released")
		}
	}
	// A same-CA, loopback-valid server for another member must fail the real
	// upstream TLS handshake, even after a fresh backend generation is installed.
	wrongListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wrong := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("wrong member received forwarded request") }), ErrorLog: log.New(io.Discard, "", 0), TLSConfig: &tls.Config{Certificates: []tls.Certificate{caller}}}
	defer func() { _ = wrong.Close() }()
	go func() { _ = wrong.ServeTLS(wrongListener, "", "") }()
	f.Nodes[3].Environment["LANTERN_PEER_LISTEN_ADDR"] = "127.0.0.1:" + strconv.Itoa(wrongListener.Addr().(*net.TCPAddr).Port)
	if err = r.resume(f); err != nil {
		t.Fatal(err)
	}
	request(data, 503)
	r.close()
	r.close()
}
