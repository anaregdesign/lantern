package provider

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

// PublicChangeHTTPHandler mounts only the scoped public change protocol. It
// never mounts a peer handler or treats a user Role as workload authority.
func (r *SecurityRuntime) PublicChangeHTTPHandler(svc *service.LanternService, config service.ChangeServiceOptions, options ...connect.HandlerOption) (string, http.Handler, error) {
	if err := r.protectDataService(svc); err != nil {
		return "", nil, err
	}
	config.Now = r.now
	changes, err := service.NewChangeConnectHandler(svc, config)
	if err != nil {
		return "", nil, err
	}
	options = append(options, service.StrictJSONHandlerOption(), connect.WithReadMaxBytes(16<<10), connect.WithSendMaxBytes(1<<20))
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternChangeServiceName+"/", func(options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternChangeServiceHandler(changes, options...)
	}, options...)
	return path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, err := r.AuthenticateBearer(req.Context(), req.Header)
		if err != nil {
			publicRPCError(w, req, connect.CodeOf(err))
			return
		}
		r.serveBoundedChanges(w, req.WithContext(ctx), handler)
	}), nil
}

func (r *SecurityRuntime) BrowserChangeHTTPHandler(svc *service.LanternService, config service.ChangeServiceOptions, options ...connect.HandlerOption) (string, http.Handler, error) {
	if err := r.protectDataService(svc); err != nil {
		return "", nil, err
	}
	config.Now = r.now
	changes, err := service.NewChangeConnectHandler(svc, config)
	if err != nil {
		return "", nil, err
	}
	options = append(options, service.StrictJSONHandlerOption(), connect.WithReadMaxBytes(16<<10), connect.WithSendMaxBytes(1<<20))
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternChangeServiceName+"/", func(options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternChangeServiceHandler(changes, options...)
	}, options...)
	bounded := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.serveBoundedChanges(w, req, handler) })
	return "/browser" + path, r.BrowserRPCHandler(path, bounded), nil
}

func (r *SecurityRuntime) serveBoundedChanges(w http.ResponseWriter, req *http.Request, handler http.Handler) {
	if r.current != nil {
		// Each immutable encoded unit is independently authorized by the
		// native owner. A previously authorized unit may complete late (A).
		handler.ServeHTTP(w, req)
		return
	}
	wallStarted := time.Now()
	lifetime := 28 * time.Second
	if r.mode == "oidc" {
		admission, known := security.AdmissionFromContext(req.Context())
		if !known || admission.Check(req.Context(), r.now()) != nil {
			publicRPCError(w, req, connect.CodeUnavailable)
			return
		}
		if remaining := admission.ExpiresAt().Sub(r.now()); remaining < lifetime {
			lifetime = remaining
		}
	}
	if lifetime <= 0 {
		publicRPCError(w, req, connect.CodeUnavailable)
		return
	}
	// Set a transport write deadline, not only a derived handler context: a
	// blocked Send must lose authority even when the client stops reading.
	controller := http.NewResponseController(w)
	hardDeadline := wallStarted.Add(lifetime)
	if err := controller.SetWriteDeadline(hardDeadline); err != nil {
		publicRPCError(w, req, connect.CodeUnavailable)
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	// Leave terminal-status delivery inside the existing authority window.
	// If the context and transport expire together, HTTP/2 may reset a healthy
	// stream before Connect sends its DeadlineExceeded trailer. Blocked writes
	// still stop at the original hard deadline; this never extends admission.
	terminalBudget := min(100*time.Millisecond, lifetime/4)
	ctx, cancel := context.WithDeadline(req.Context(), hardDeadline.Add(-terminalBudget))
	defer cancel()
	handler.ServeHTTP(w, req.WithContext(ctx))
}
