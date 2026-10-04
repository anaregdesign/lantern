package provider

import (
	"context"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type snapshotDeadlineWriter struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func (w *snapshotDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, deadline)
	return nil
}
func TestSnapshotAdmissionStopsTransportWhenAuthorityIsWithdrawn(t *testing.T) {
	cfg, data, clock := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	clock.advance(35 * time.Second)
	cut, known := runtime.native.Store().Current()
	if !known {
		t.Fatal("missing cut")
	}
	admission, err := security.NewAdmission(security.Identity{Kind: security.OIDCPrincipal, Issuer: cfg.Bootstrap.Issuer.URL, Subject: "admin"}, clock.Now(), clock.Now().Add(28*time.Second), cut, runtime.authorityCheck)
	if err != nil {
		t.Fatal(err)
	}
	entered, done := make(chan struct{}), make(chan struct{})
	handler := runtime.boundSnapshotHTTPHandler(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		ctx, err := runtime.boundSnapshotAdmission(req.Context())
		if err != nil {
			t.Error(err)
			return
		}
		// The production interceptor may be composed twice, but verification and
		// stream lifetime still belong to this same request.
		if _, err := runtime.boundSnapshotAdmission(ctx); err != nil {
			t.Error(err)
			return
		}
		close(entered)
		<-ctx.Done()
	}))
	req := httptest.NewRequest(http.MethodPost, graphv1connect.LanternServiceBackupSnapshotProcedure, nil)
	req = req.WithContext(security.WithAdmission(context.Background(), admission))
	writer := &snapshotDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	go func() { defer close(done); handler.ServeHTTP(writer, req) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("export not admitted")
	}
	clock.advance(-time.Second)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("withdrawn authority kept blocked export open")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.deadlines) < 3 || !writer.deadlines[len(writer.deadlines)-1].IsZero() {
		t.Fatal("export transport deadline not forced/reset")
	}
}
