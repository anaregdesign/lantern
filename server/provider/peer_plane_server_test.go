package provider

import (
	"context"
	"errors"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPeerPlaneRunReturnsDrainTimeout(t *testing.T) {
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsConfig, client := seed.TLS.Clone(), seed.Client()
	seed.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	server := &http.Server{TLSConfig: tlsConfig, Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	})}
	t.Cleanup(func() { _ = server.Close() })
	p := &PeerPlaneServer{server: server, listener: listener}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, _ := client.Get("https://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("private TLS request did not enter")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("private forced close became orderly success", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("private worker did not join")
	}
	<-requestDone
}

func TestPeerPlaneDisabledStillRequiresCertifiedServiceAndJoins(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	svc := data.NewLanternService(nil)
	rep, err := data.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, nil, rep, NetConfig{}, nil, runtimeCertified{}, nil); err == nil {
		t.Fatal("private surface ignored runtime identity")
	}
	restored, err := NewRuntimeRestored(data, svc)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewRuntimeCertified(data, svc, rep, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	server, cleanup, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, nil, rep, NetConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cert, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disabled private lifecycle did not join")
	}
}

func TestPeerListenerAdoptionRequiresExactRuntime(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	svc := data.NewLanternService(nil)
	rep, err := data.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, svc)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewRuntimeCertified(data, svc, rep, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"foreign_replication", "frame", "missing_workload", "foreign_policy", "unexpected_private"} {
		t.Run(phase, func(t *testing.T) {
			owner, err := listenerlaunch.Reserve(t.Context(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			_, address := owner.Addresses()
			config := PeerPlaneConfig{ListenAddress: address}
			certified := cert
			var policy *SecurityPeerRuntime
			switch phase {
			case "foreign_replication":
				certified.replication = nil
			case "frame":
				certified.replicationSendMaxBytes = 1
			case "foreign_policy":
				policy = &SecurityPeerRuntime{}
			case "unexpected_private":
				config.ListenAddress = ""
			}
			if _, cleanup, err := NewPeerPlaneServer(config, nil, policy, rep, NetConfig{}, nil, certified, owner); err == nil {
				cleanup()
				t.Fatal("private guard bypassed")
			}
			// A failing guard cannot consume the role-bound capability.
			if listener, err := owner.Peer(address); err != nil {
				t.Fatal("failed guard consumed private listener", err)
			} else {
				_ = listener.Close()
			}
		})
	}
}
