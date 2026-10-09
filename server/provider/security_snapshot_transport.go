package provider

import (
	"context"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"net/http"
	"strings"
	"time"
)

type snapshotTransportKey struct{}
type snapshotTransport struct {
	controller *http.ResponseController
	cancel     context.CancelFunc
	stop       chan struct{}
	stopped    chan struct{}
}

// Only protected streaming exports allocate a transport authority watcher.
// Ordinary data RPCs retain the existing local-admission hot path.
func (r *SecurityRuntime) boundSnapshotHTTPHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/browser")
		if r.mode != "oidc" || r.current != nil || path != graphv1connect.LanternServiceBackupSnapshotProcedure {
			handler.ServeHTTP(w, req)
			return
		}
		boundary := &snapshotTransport{controller: http.NewResponseController(w)}
		defer func() {
			if boundary.cancel != nil {
				close(boundary.stop)
				<-boundary.stopped
				boundary.cancel()
				_ = boundary.controller.SetWriteDeadline(time.Time{})
			}
		}()
		handler.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), snapshotTransportKey{}, boundary)))
	})
}
func (r *SecurityRuntime) boundSnapshotAdmission(ctx context.Context) (context.Context, error) {
	boundary, found := ctx.Value(snapshotTransportKey{}).(*snapshotTransport)
	if !found {
		return ctx, nil
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known || admission.Check(ctx, r.now()) != nil {
		return ctx, security.ErrAuthorityUnavailable
	}
	if boundary.cancel != nil {
		return ctx, nil
	}
	remaining := minSecurityTime(admission.ExpiresAt(), r.now().Add(28*time.Second)).Sub(r.now())
	if remaining <= 0 || boundary.controller.SetWriteDeadline(time.Now().Add(remaining)) != nil {
		return ctx, security.ErrAuthorityUnavailable
	}
	ctx, boundary.cancel = context.WithTimeout(ctx, remaining)
	boundary.stop, boundary.stopped = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(boundary.stopped)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-boundary.stop:
				return
			case <-ctx.Done():
				_ = boundary.controller.SetWriteDeadline(time.Now())
				return
			case <-ticker.C:
				if admission.Check(ctx, r.now()) != nil {
					boundary.cancel()
					_ = boundary.controller.SetWriteDeadline(time.Now())
					return
				}
			}
		}
	}()
	return ctx, nil
}
