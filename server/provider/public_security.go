package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/readiness"
)

type publicSecurityCertified struct {
	runtime *SecurityRuntime
	primary *dataruntime.Executor
}

func NewPublicSecurityCertified(runtime *SecurityRuntime, tls TLSConfig, peer *PeerIdentityRuntime, data *dataruntime.ServingRuntime, primary *dataruntime.Executor, certified runtimeCertified) (publicSecurityCertified, error) {
	if !certified.valid || certified.runtime != data || certified.primary != primary || certified.replication == nil {
		return publicSecurityCertified{}, errors.New("public security requires the exact installed data services")
	}
	if runtime == nil || data == nil || primary == nil || !primary.SharesServingRuntime(data) || data.DataNamespaceFormat() != keyspace.Version {
		return publicSecurityCertified{}, errors.New("public serving requires the certified namespace and security runtime")
	}
	if runtime.mode == "oidc" && (runtime.current == nil || runtime.data != data || tls.CertFile == "" && tls.CertPEM == "" && len(runtime.config.TrustedProxyIPs) == 0) {
		return publicSecurityCertified{}, errors.New("OIDC requires its owned authority and TLS or an exact trusted HTTPS gateway")
	}
	if err := runtime.bindWorkload(peer); err != nil {
		return publicSecurityCertified{}, err
	}
	if err := certifyEdgeCreateTopology(primary, runtime.mode, runtime.current.FixedVoterCount(), peer); err != nil {
		return publicSecurityCertified{}, err
	}
	if err := runtime.protectDataService(primary); err != nil {
		return publicSecurityCertified{}, err
	}
	return publicSecurityCertified{runtime: runtime, primary: primary}, nil
}

func certifyEdgeCreateTopology(primary *dataruntime.Executor, mode string, voters int, peer *PeerIdentityRuntime) error {
	if peer == nil {
		return nil
	}
	ha := true
	if mode == "oidc" {
		if voters < 1 || voters > 31 || peer.store.FixedMemberCount() != voters {
			return errors.New("data workload differs from the fixed authority topology")
		}
		ha = voters > 1
	}
	if ha {
		primary.WithEdgeCreateHA()
	}
	return nil
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
			return len(req.Header.Values("X-Forwarded-Proto")) == 1 && req.Header.Get("X-Forwarded-Proto") == "https" && len(req.Header.Values("X-Forwarded-Host")) == 1 && req.Header.Get("X-Forwarded-Host") == req.Host
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
		if admission.BindCurrentGlobalOutput(ctx, action) != nil {
			publicRPCError(w, req, connect.CodeUnavailable)
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
