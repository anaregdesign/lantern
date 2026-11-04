package provider

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"golang.org/x/time/rate"
)

const (
	rateRecoveryRPS   = 4
	rateRecoveryBurst = 8
)

var errRateRecoveryBinding = errors.New("rate recovery requires the exact runtime and admission owner")

// rateRecoveryRuntime is a construction-owned capability, not a request grant.
// Its bucket and identity are retained independently of operational generations.
type rateRecoveryRuntime struct {
	runtime *SecurityRuntime
	owner   *RateLimitInterceptor
	reserve *rate.Limiter
}

// NewRateRecoveryRuntime binds one fixed recovery allowance to the actual OIDC
// runtime and rate owner. OFF binds the owner without creating an allowance.
// Repeated construction with the same identities never resets the bucket.
func NewRateRecoveryRuntime(runtime *SecurityRuntime, owner *RateLimitInterceptor) (*rateRecoveryRuntime, error) {
	if runtime == nil || owner == nil || (runtime.mode != "off" && runtime.mode != "oidc") || runtime.mode == "oidc" && runtime.current == nil {
		return nil, errRateRecoveryBinding
	}
	runtime.rateRecoveryMu.Lock()
	defer runtime.rateRecoveryMu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if runtime.rateRecovery != nil {
		capability := runtime.rateRecovery
		if capability.owner != owner || owner.recovery != capability {
			return nil, errRateRecoveryBinding
		}
		return capability, nil
	}
	if owner.recovery != nil {
		return nil, errRateRecoveryBinding
	}
	capability := &rateRecoveryRuntime{runtime: runtime, owner: owner}
	if runtime.mode == "oidc" {
		capability.reserve = rate.NewLimiter(rateRecoveryRPS, rateRecoveryBurst)
	}
	owner.recovery = capability
	runtime.rateRecovery = capability
	return capability, nil
}

func (r *rateRecoveryRuntime) bound(runtime *SecurityRuntime, owner *RateLimitInterceptor) bool {
	if r == nil || runtime == nil || owner == nil || r.runtime != runtime || r.owner != owner {
		return false
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return r.boundLocked()
}

func (r *rateRecoveryRuntime) boundLocked() bool {
	return r.owner.recovery == r && r.runtime.rateRecovery == r && ((r.runtime.mode == "off" && r.reserve == nil) ||
		(r.runtime.mode == "oidc" && r.runtime.current != nil && r.reserve != nil))
}

// available attests the same capability consumed by the public listener. A
// nonfinite bootstrap policy remains repairable and cannot fake this binding.
func (r *rateRecoveryRuntime) available(runtime *SecurityRuntime, owner *RateLimitInterceptor) bool {
	return r.bound(runtime, owner) && r.reserve != nil
}

func (r *rateRecoveryRuntime) allow(req *http.Request) bool {
	if r == nil || r.owner == nil || r.runtime == nil || !rateRecoveryRoute(req) {
		return false
	}
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	if !r.boundLocked() || r.reserve == nil {
		return false
	}
	now := time.Now
	if r.owner.now != nil {
		now = r.owner.now
	}
	return r.reserve.AllowN(now(), 1)
}

// rateRecoveryRoute is deliberately a closed route and method list. New RPCs
// require a separate review; a service prefix or a caller header grants none.
func rateRecoveryRoute(req *http.Request) bool {
	if req == nil || req.URL == nil || len(req.URL.Path) > 128 || req.URL.RawPath != "" || req.URL.Opaque != "" || req.URL.EscapedPath() != req.URL.Path {
		return false
	}
	path := req.URL.Path
	if req.Method == http.MethodPost {
		if path == "/auth/logout" {
			return true
		}
		path = strings.TrimPrefix(path, "/browser")
		switch path {
		case graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure,
			graphv1connect.LanternSecurityServiceGetCurrentPrincipalProcedure,
			graphv1connect.LanternSecurityServiceListIssuersProcedure,
			graphv1connect.LanternSecurityServiceGetIssuerProcedure,
			graphv1connect.LanternSecurityServiceListRolesProcedure,
			graphv1connect.LanternSecurityServiceGetRoleProcedure,
			graphv1connect.LanternSecurityServiceListUsersProcedure,
			graphv1connect.LanternSecurityServiceGetUserProcedure,
			graphv1connect.LanternSecurityServiceGetRegistrationPolicyProcedure,
			graphv1connect.LanternSecurityServiceGetLogSettingsProcedure,
			// Generated consumers arrive with the bounded operational API.
			"/graph.v1.LanternSecurityService/GetOperationalSettings",
			"/graph.v1.LanternSecurityService/PreviewOperationalPolicy",
			graphv1connect.LanternSecurityServiceListRoleAssignmentsProcedure,
			graphv1connect.LanternSecurityServiceListSecurityAuditProcedure,
			graphv1connect.LanternSecurityServiceGetRoleTemplatesProcedure,
			graphv1connect.LanternSecurityServiceExplainAccessProcedure,
			graphv1connect.LanternSecurityServiceValidateIssuerProcedure,
			graphv1connect.LanternSecurityServicePrepareSecurityChangesProcedure,
			graphv1connect.LanternSecurityServiceBeginSecurityChangeAuthorizationProcedure,
			graphv1connect.LanternSecurityServiceGetSecurityChangeAuthorizationProcedure,
			graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure,
			graphv1connect.LanternSecurityServiceApplySecurityChangeProcedure,
			graphv1connect.LanternSecurityServiceGetSecurityChangeStatusProcedure:
			return true
		}
		return false
	}
	if req.Method != http.MethodGet {
		return false
	}
	if path == "/auth/login" || path == "/auth/session" {
		return true
	}
	if ticket, found := strings.CutPrefix(path, "/auth/management-authorization/"); found {
		if len(ticket) != 43 {
			return false
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(ticket)
		return err == nil && len(raw) == 32
	}
	if digest, found := strings.CutPrefix(path, "/auth/callback/"); found {
		if len(digest) != 64 || strings.ToLower(digest) != digest {
			return false
		}
		raw, err := hex.DecodeString(digest)
		return err == nil && len(raw) == 32
	}
	return false
}
