package provider

import (
	"encoding/base64"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestRateRecoveryRouteClosedCanonicalList(t *testing.T) {
	methods := []string{"GetAuthCapabilities", "GetCurrentPrincipal", "ListIssuers", "GetIssuer", "ListRoles", "GetRole", "ListUsers", "GetUser", "GetRegistrationPolicy", "GetLogSettings", "GetOperationalSettings", "PreviewOperationalPolicy", "ListRoleAssignments", "ListSecurityAudit", "GetRoleTemplates", "ExplainAccess", "ValidateIssuer", "PrepareSecurityChanges", "BeginSecurityChangeAuthorization", "GetSecurityChangeAuthorization", "ApplySecurityChanges", "ApplySecurityChange", "GetSecurityChangeStatus"}
	for _, method := range methods {
		for _, prefix := range []string{"", "/browser"} {
			path := prefix + "/graph.v1.LanternSecurityService/" + method
			for _, verb := range []string{http.MethodPost, http.MethodGet, http.MethodPut, http.MethodOptions} {
				if got := rateRecoveryRoute(httptest.NewRequest(verb, path, nil)); got != (verb == http.MethodPost) {
					t.Errorf("%s %s recovery=%v", verb, path, got)
				}
			}
		}
	}
	ticket := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, path := range []string{"/auth/login", "/auth/session", "/auth/management-authorization/" + ticket, "/auth/callback/" + strings.Repeat("ab", 32)} {
		if !rateRecoveryRoute(httptest.NewRequest(http.MethodGet, path, nil)) || rateRecoveryRoute(httptest.NewRequest(http.MethodPost, path, nil)) {
			t.Errorf("auth method boundary: %s", path)
		}
	}
	if !rateRecoveryRoute(httptest.NewRequest(http.MethodPost, "/auth/logout", nil)) || rateRecoveryRoute(httptest.NewRequest(http.MethodGet, "/auth/logout", nil)) {
		t.Fatal("logout must retain its POST boundary")
	}
	for _, path := range []string{
		"/auth/operations", "/auth/login/", "/auth//login", "/auth/../auth/login", "/auth/%6cogin",
		"/auth/management-authorization/" + ticket + "/extra", "/auth/management-authorization/" + ticket + "=",
		"/auth/management-authorization/" + ticket[:42] + "B", "/auth/management-authorization/" + strings.Repeat("A", 4096),
		"/auth/callback/" + strings.Repeat("AB", 32), "/auth/callback/" + strings.Repeat("ab", 32) + "/extra",
		"/graph.v1.LanternSecurityService/FutureMethod", "/graph.v1.LanternSecurityService/getLogSettings",
		"/browser/browser/graph.v1.LanternSecurityService/GetLogSettings", "/graph.v1.LanternSecurityService/GetLogSettings/",
		"/graph.v1.LanternService/GetVertex", "/graph.v1.LanternChangeService/WatchChanges", "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		"/graph.v1.LanternNativeAuthService/BeginNativeLogin", "/auth/native/apple/callback/mobile",
	} {
		for _, verb := range []string{http.MethodPost, http.MethodGet} {
			req := httptest.NewRequest(verb, path, nil)
			req.Header.Set("X-Lantern-Recovery", "true")
			if rateRecoveryRoute(req) {
				t.Errorf("unlisted route admitted: %s %s", verb, path)
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/graph.v1.LanternSecurityService/GetLogSettings", nil)
	req.URL.Opaque = "//opaque"
	if rateRecoveryRoute(req) || rateRecoveryRoute(nil) {
		t.Fatal("opaque or absent request admitted")
	}
}

func TestRateRecoveryIdentityAndInvalidBootstrap(t *testing.T) {
	for _, input := range []struct {
		runtime *SecurityRuntime
		owner   *RateLimitInterceptor
	}{{nil, NewRateLimitInterceptor(0, 0)}, {&SecurityRuntime{mode: "off"}, nil}, {&SecurityRuntime{mode: "oidc"}, NewRateLimitInterceptor(0, 0)}} {
		if _, err := NewRateRecoveryRuntime(input.runtime, input.owner); err == nil {
			t.Fatal("incomplete recovery ownership accepted")
		}
	}
	// Only transport ownership is synthetic here; actual authority and browser
	// admission are covered by the coordinated native integration fixture.
	runtime := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
	owner := NewRateLimitInterceptor(math.NaN(), 1)
	recovery, err := NewRateRecoveryRuntime(runtime, owner)
	if err != nil || !recovery.available(runtime, owner) {
		t.Fatalf("invalid bootstrap lost actual recovery capability: %v", err)
	}
	if owner.Allow() {
		t.Fatal("invalid ordinary policy was silently disabled")
	}
	if _, err := owner.Configuration(); err == nil {
		t.Fatal("invalid bootstrap policy was hidden")
	}
	foreign := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
	if _, err := NewRateRecoveryRuntime(foreign, owner); err == nil || recovery.available(foreign, owner) || recovery.available(runtime, NewRateLimitInterceptor(0, 0)) {
		t.Fatal("foreign owner/runtime accepted recovery proof")
	}
	if _, err := NewRateRecoveryRuntime(runtime, NewRateLimitInterceptor(0, 0)); err == nil {
		t.Fatal("one runtime certified a second unmounted rate owner")
	}
	forged := &rateRecoveryRuntime{runtime: runtime, owner: owner, reserve: recovery.reserve}
	if forged.available(runtime, owner) {
		t.Fatal("copied identity fields manufactured construction capability")
	}
	if err := owner.Configure(2, 3); err != nil || !owner.Allow() {
		t.Fatalf("valid trusted update did not repair owner: %v", err)
	}
	if again, err := NewRateRecoveryRuntime(runtime, owner); err != nil || again != recovery {
		t.Fatal("repeated construction replaced recovery owner", err)
	}
}

func TestRateRecoveryBoundedNoResetAndOFF(t *testing.T) {
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			runtime := &SecurityRuntime{mode: mode}
			if mode == "oidc" {
				runtime.current = &security.CurrentAuthority{}
			}
			owner := NewRateLimitInterceptor(0.0001, 1)
			clock := time.Unix(100, 0)
			owner.now = func() time.Time { return clock }
			recovery, err := NewRateRecoveryRuntime(runtime, owner)
			if err != nil {
				t.Fatal(err)
			}
			if !owner.Allow() || owner.Allow() {
				t.Fatal("ordinary owner not exhausted")
			}
			request := httptest.NewRequest(http.MethodPost, "/browser/graph.v1.LanternSecurityService/ApplySecurityChanges", nil)
			if mode == "off" {
				if recovery.reserve != nil || recovery.available(runtime, owner) || recovery.allow(request) {
					t.Fatal("OFF gained recovery allowance")
				}
				return
			}
			for range rateRecoveryBurst {
				if !recovery.allow(request) {
					t.Fatal("initial recovery allowance missing")
				}
			}
			for _, policy := range []RateLimitConfig{{RPS: 0.0001, Burst: 1}, {RPS: 0.0002, Burst: 2}, {}, {RPS: 0.0001, Burst: 1}} {
				if err := owner.Configure(policy.RPS, policy.Burst); err != nil {
					t.Fatal(err)
				}
				if again, err := NewRateRecoveryRuntime(runtime, owner); err != nil || again != recovery || recovery.allow(request) {
					t.Fatal("operational update/repeated construction refilled recovery", err)
				}
			}
			clock = clock.Add(time.Second)
			for range rateRecoveryRPS {
				if !recovery.allow(request) {
					t.Fatal("recovery cadence did not earn expected credit")
				}
			}
			if recovery.allow(request) {
				t.Fatal("recovery exceeded fixed cadence")
			}
		})
	}
}

func TestRateRecoveryConcurrentRetainsSingleBudget(t *testing.T) {
	runtime := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
	owner := NewRateLimitInterceptor(1, 1)
	owner.now = func() time.Time { return time.Unix(100, 0) }
	recovery, err := NewRateRecoveryRuntime(runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	var admitted atomic.Int32
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for range 40 {
				again, err := NewRateRecoveryRuntime(runtime, owner)
				if err != nil || again != recovery {
					t.Error("concurrent construction changed recovery binding", err)
				}
				if err := owner.Configure(float64(worker+1), worker+1); err != nil {
					t.Error(err)
				}
				if recovery.allow(request) {
					admitted.Add(1)
				}
			}
		})
	}
	workers.Wait()
	if admitted.Load() != rateRecoveryBurst {
		t.Fatalf("concurrent recovery granted %d requests; fixed burst is %d", admitted.Load(), rateRecoveryBurst)
	}
}
