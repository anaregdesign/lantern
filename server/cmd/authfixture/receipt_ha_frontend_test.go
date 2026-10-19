package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReceiptHAFrontendRetainsEndpointAndJoinsOldTLSGeneration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newReceiptHAFrontend(listener)
	defer f.close()
	start := func(body string) *httptest.Server {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		t.Cleanup(s.Close)
		return s
	}
	old := start("old")
	port := old.Listener.Addr().(*net.TCPAddr).Port
	if err = f.resume(&fixtureLaunch{public: port, peer: 1}); err != nil {
		t.Fatal(err)
	}
	if err = f.resume(&fixtureLaunch{public: port, peer: 1}); err == nil {
		t.Fatal("replaced active generation")
	}
	client := old.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Get("https://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var body [3]byte
	if _, err = io.ReadFull(response.Body, body[:]); err != nil || string(body[:]) != "old" {
		t.Fatal("TLS bytes changed", err)
	}
	f.pause()
	if _, err = io.ReadAll(response.Body); err == nil {
		t.Fatal("old tunnel survived retirement")
	}
	_ = response.Body.Close()
	competing, err := net.Listen("tcp", listener.Addr().String())
	if err == nil {
		_ = competing.Close()
		t.Fatal("public endpoint released during restart")
	}
	replacement := start("new")
	if err = f.resume(&fixtureLaunch{public: replacement.Listener.Addr().(*net.TCPAddr).Port, peer: 1}); err != nil {
		t.Fatal(err)
	}
	client = replacement.Client()
	client.Timeout = 5 * time.Second
	response, err = client.Get("https://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(response.Body, body[:]); err != nil || string(body[:]) != "new" {
		t.Fatal("stale target generation", err)
	}
	_ = response.Body.Close()
	f.close()
	rebound, err := net.Listen("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal("public socket leaked", err)
	}
	_ = rebound.Close()
	if strings.Contains(string(body[:]), "old") {
		t.Fatal("old backend reused")
	}
}
