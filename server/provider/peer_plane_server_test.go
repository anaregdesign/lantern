package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
)

func startPeerPlaneRequest(t *testing.T, handler http.Handler, configure ...func(*PeerPlaneServer)) (context.CancelFunc, <-chan error, <-chan struct{}) {
	t.Helper()
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsConfig, client := seed.TLS.Clone(), seed.Client()
	seed.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{TLSConfig: tlsConfig, Handler: handler}
	t.Cleanup(func() { _ = server.Close() })
	p := &PeerPlaneServer{server: server, listener: listener}
	for _, apply := range configure {
		apply(p)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, _ := client.Get("https://" + listener.Addr().String())
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	t.Cleanup(client.CloseIdleConnections)
	return cancel, done, requestDone
}

func TestPeerPlaneRunCancelsCooperativeStreamAndJoins(t *testing.T) {
	for _, holdAfterCancellation := range []bool{false, true} {
		name := "idle_subscription"
		if holdAfterCancellation {
			name = "entered_work_must_join"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan int, 1)
			canceled, joined, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			cancel, done, requestDone := startPeerPlaneRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(joined)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				entered <- r.ProtoMajor
				<-r.Context().Done()
				close(canceled)
				if holdAfterCancellation {
					<-release
				}
			}))
			select {
			case major := <-entered:
				if major != 1 {
					t.Fatal("stream did not use the private client's HTTP/1.1 protocol", major)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("private stream did not enter")
			}
			// The client remains alive. Only the post-drain Run owner is canceled.
			cancel()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("Run cancellation did not reach the active request")
			}
			if holdAfterCancellation {
				select {
				case err := <-done:
					t.Fatal("Run returned before entered work joined", err)
				case <-time.After(50 * time.Millisecond):
				}
			}
			close(release)
			released = true
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("cooperative private stop failed", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("private worker did not join before its shutdown deadline")
			}
			select {
			case <-joined:
			default:
				t.Fatal("Run returned without joining the request handler")
			}
			select {
			case <-requestDone:
			case <-time.After(time.Second):
				t.Fatal("stream client did not observe completion")
			}
		})
	}
}

func TestPeerPlaneRunReturnsDrainTimeout(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	cancel, done, requestDone := startPeerPlaneRequest(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(entered)
		// Deliberately ignore request cancellation: a real failure must retain
		// the shutdown deadline and forced-close error after cooperative repair.
		<-release
	}))
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
	data := securityDataFixture(t)
	svc := data.NewLanternService(nil)
	rep, err := data.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, rep, NetConfig{}, nil, runtimeCertified{}, nil); err == nil {
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
	server, cleanup, err := NewPeerPlaneServer(PeerPlaneConfig{}, nil, rep, NetConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cert, nil)
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
	data := securityDataFixture(t)
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
	for _, phase := range []string{"foreign_replication", "frame", "missing_workload", "unexpected_private"} {
		t.Run(phase, func(t *testing.T) {
			owner, err := listenerlaunch.Reserve(t.Context(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			_, address := owner.Addresses()
			config := PeerPlaneConfig{ListenAddress: address}
			certified := cert
			switch phase {
			case "foreign_replication":
				certified.replication = nil
			case "frame":
				certified.replicationSendMaxBytes = 1
			case "unexpected_private":
				config.ListenAddress = ""
			}
			if _, cleanup, err := NewPeerPlaneServer(config, nil, rep, NetConfig{}, nil, certified, owner); err == nil {
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

func TestPeerPlaneRunClosesAndJoinsSnapshotTailsBeforeHTTPShutdown(t *testing.T) {
	entered, tailClosing, releaseTail := make(chan struct{}), make(chan struct{}), make(chan struct{})
	httpShutdown := make(chan struct{})
	defer func() {
		select {
		case <-releaseTail:
		default:
			close(releaseTail)
		}
	}()
	cancel, done, requestDone := startPeerPlaneRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}), func(p *PeerPlaneServer) {
		p.closeSnapshotTails = func() {
			close(tailClosing)
			<-releaseTail
		}
		p.server.RegisterOnShutdown(func() { close(httpShutdown) })
	})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("private request did not enter")
	}
	cancel()
	select {
	case <-tailClosing:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not close detached Snapshot tails")
	}
	select {
	case <-httpShutdown:
		t.Fatal("HTTP shutdown ran before Snapshot tail owners joined")
	case err := <-done:
		t.Fatal("Run returned before Snapshot tail owners joined", err)
	default:
	}
	close(releaseTail)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("private worker did not join after tail close completed")
	}
	select {
	case <-httpShutdown:
	case <-time.After(time.Second):
		t.Fatal("HTTP shutdown did not follow Snapshot tail close")
	}
	<-requestDone
}

func TestPeerPlaneDisabledClosesSnapshotTails(t *testing.T) {
	closed := false
	server := &PeerPlaneServer{closeSnapshotTails: func() { closed = true }}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := server.Run(ctx); err != nil || !closed {
		t.Fatalf("disabled private lifecycle did not close tails: closed=%v, err=%v", closed, err)
	}
}
