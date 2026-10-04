package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

type changeDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func TestSecurityChangesBlockedHTTP2ReaderRetainsHardDeadline(t *testing.T) {
	cfg, data, clock := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	clock.advance(35 * time.Second)
	cut, known := runtime.native.Store().Current()
	if !known {
		t.Fatal("missing policy cut")
	}
	finished := make(chan error, 1)
	// A transport stress handler deliberately fills HTTP/2 flow control. The
	// normal ChangeService validates its bounded frames separately; this test
	// proves a stalled Send cannot bypass the wrapper's authority deadline.
	handler := connect.NewServerStreamHandler(graphv1connect.LanternChangeServiceWatchChangesProcedure, func(ctx context.Context, _ *connect.Request[pb.WatchChangesRequest], stream *connect.ServerStream[pb.WatchChangesResponse]) (failure error) {
		defer func() { finished <- failure }()
		if err := stream.Send(&pb.WatchChangesResponse{Bootstrap: true, Cursor: []byte{1}}); err != nil {
			return err
		}
		frame := &pb.WatchChangesResponse{Cursor: make([]byte, 1<<20)}
		for range 100 {
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
		return nil
	}, connect.WithCompressMinBytes(2<<20))
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		admission, err := security.NewAdmission(security.Identity{Kind: security.OIDCPrincipal, Issuer: cfg.Bootstrap.Issuer.URL, Subject: "admin"}, clock.Now(), clock.Now().Add(500*time.Millisecond), cut, runtime.authorityCheck)
		if err != nil {
			t.Error(err)
			return
		}
		runtime.serveBoundedChanges(w, req.WithContext(security.WithAdmission(req.Context(), admission)), handler)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := graphv1connect.NewLanternChangeServiceClient(srv.Client(), srv.URL, connect.WithGRPC()).WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Bootstrap {
		t.Fatal("HTTP/2 reader did not start", stream.Err())
	}
	select {
	case failure := <-finished:
		if failure == nil {
			t.Fatal("stress stream did not encounter blocked flow control")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled HTTP/2 Send retained authority beyond its hard deadline")
	}
}

func (w *changeDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestSecurityChangesRequireTransportWriteDeadline(t *testing.T) {
	runtime := &SecurityRuntime{mode: "off", now: time.Now}
	req := httptest.NewRequest(http.MethodPost, "/graph.v1.LanternChangeService/WatchChanges", nil)
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if deadline, exists := req.Context().Deadline(); !exists || time.Until(deadline) > 28*time.Second {
			t.Fatal("handler lacks bounded stream lifetime")
		}
		w.WriteHeader(http.StatusOK)
	})
	w := &changeDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	runtime.serveBoundedChanges(w, req, handler)
	if !called || len(w.deadlines) != 2 || w.deadlines[0].IsZero() || !w.deadlines[1].IsZero() {
		t.Fatal("transport deadline not installed/reset", w.deadlines)
	}
	called = false
	unsupported := httptest.NewRecorder()
	runtime.serveBoundedChanges(unsupported, req, handler)
	if called || unsupported.Code != http.StatusServiceUnavailable {
		t.Fatal("unsupported transport silently enabled an unbounded stream")
	}
	runtime.mode = "oidc"
	w = &changeDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	runtime.serveBoundedChanges(w, req, handler)
	if called || len(w.deadlines) != 0 || w.Code != http.StatusServiceUnavailable {
		t.Fatal("OIDC stream deadline fabricated admission")
	}
}

func TestSecurityChangesReserveTerminalTimeWithinShortAdmission(t *testing.T) {
	cfg, data, clock := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	clock.advance(35 * time.Second)
	cut, known := runtime.native.Store().Current()
	if !known {
		t.Fatal("missing policy cut")
	}
	for _, lifetime := range []time.Duration{time.Millisecond, 200 * time.Millisecond, 2 * time.Second} {
		admission, err := security.NewAdmission(security.Identity{Kind: security.OIDCPrincipal, Issuer: cfg.Bootstrap.Issuer.URL, Subject: "admin"}, clock.Now(), clock.Now().Add(lifetime), cut, runtime.authorityCheck)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/changes", nil).WithContext(security.WithAdmission(context.Background(), admission))
		writer := &changeDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		started := time.Now()
		runtime.serveBoundedChanges(writer, req, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			deadline, exists := req.Context().Deadline()
			if !exists || len(writer.deadlines) != 1 {
				t.Fatal("missing bounded transport/context")
			}
			hard := writer.deadlines[0]
			if hard.Sub(started) > lifetime+10*time.Millisecond || hard.Sub(deadline) != min(100*time.Millisecond, lifetime/4) {
				t.Fatal("authority was extended or terminal budget not reserved")
			}
		}))
		if len(writer.deadlines) != 2 || !writer.deadlines[1].IsZero() {
			t.Fatal("transport deadline not reset")
		}
	}
}
