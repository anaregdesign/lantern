package provider

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
	"net"
	"net/http"
	"net/netip"
)

type publicSecurityCertified struct {
	runtime *SecurityRuntime
	primary *service.LanternService
}

func NewPublicSecurityCertified(runtime *SecurityRuntime, tls TLSConfig, peer *PeerIdentityRuntime, policy *SecurityPeerRuntime, data *service.ServingRuntime, primary *service.LanternService, certified runtimeCertified) (publicSecurityCertified, error) {
	if !certified.valid || certified.runtime != data || certified.primary != primary || certified.replication == nil {
		return publicSecurityCertified{}, errors.New("public security requires the exact installed data services")
	}
	if runtime == nil || data == nil || primary == nil || !primary.SharesServingRuntime(data) || data.DataNamespaceFormat() != keyspace.Version {
		return publicSecurityCertified{}, errors.New("public serving requires the certified namespace and security runtime")
	}
	if runtime.peer != peer || policy != nil && (policy.runtime != runtime || policy.peer != peer) {
		return publicSecurityCertified{}, errors.New("public security requires its owned workload and policy runtime")
	}
	if runtime.mode == "oidc" {
		if runtime.native == nil || runtime.data != data || (tls.CertFile == "" && len(runtime.config.TrustedProxyIPs) == 0) {
			return publicSecurityCertified{}, errors.New("OIDC requires native authority and a TLS or exact trusted HTTPS gateway boundary")
		}
		if peer != nil && policy == nil || runtime.config.NodeRole == "replica" && policy == nil {
			return publicSecurityCertified{}, errors.New("OIDC peer serving requires policy-lease composition")
		}
	}
	if peer != nil {
		primary.WithEdgeCreateHA()
	}
	if err := runtime.protectDataService(primary); err != nil {
		return publicSecurityCertified{}, err
	}
	return publicSecurityCertified{runtime: runtime, primary: primary}, nil
}
func (r *SecurityRuntime) protectedIngress(req *http.Request) bool {
	if r.mode == "off" || req.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, trusted := range r.config.TrustedProxyIPs {
		if trusted.Unmap() == ip.Unmap() {
			return exactBrowserHeader(req.Header, "X-Forwarded-Proto", "https") && exactBrowserHeader(req.Header, "X-Forwarded-Host", req.Host)
		}
	}
	return false
}
func (r *SecurityRuntime) publicIngressHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.peer != nil && req.URL.Path != graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure && req.URL.Path != "/grpc.health.v1.Health/Check" && r.peer.CheckWorkload(req.Context()) != nil {
			publicRPCError(w, req, connect.CodeUnavailable)
			return
		}
		if r.mode == "oidc" && req.URL.Path != graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure && req.URL.Path != "/grpc.health.v1.Health/Check" && !r.protectedIngress(req) {
			publicRPCError(w, req, connect.CodePermissionDenied)
			return
		}
		next.ServeHTTP(w, req)
	})
}
func (r *SecurityRuntime) requireGlobalHTTP(action security.Action, next http.Handler) http.Handler {
	if r.mode == "off" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, err := r.AuthenticateBearer(req.Context(), req.Header)
		if err != nil {
			publicRPCError(w, req, connectCode(err))
			return
		}
		admission, known := security.AdmissionFromContext(ctx)
		if !known || !admission.Access().AllowsGlobal(action) {
			publicRPCError(w, req, connectCode(security.ErrPermissionDenied))
			return
		}
		r.serveBoundedChanges(w, req.WithContext(ctx), next)
	})
}

// connectCode is kept local to HTTP wrappers; it exposes only status category.
func connectCode(err error) connect.Code {
	if errors.Is(err, security.ErrPermissionDenied) {
		return connect.CodePermissionDenied
	}
	return connect.CodeOf(err)
}
func NewServingReadinessGate(config ReadinessConfig, peers PeerConfig, resolver *PeerResolver, health *HealthChecker, runtime *SecurityRuntime) *readiness.Gate {
	gate := NewReadinessGate(config, peers, resolver, health)
	gate.SetServingPermission(runtime.Ready(context.Background()))
	return gate
}
