package provider

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestPublicRateLimitStopsAuthAndStreamBeforeWork(t *testing.T) {
	for _, path := range []string{"/auth/login", "/browser/graph.v1.LanternService/GetVertex", "/graph.v1.LanternChangeService/WatchChanges"} {
		calls, rejections := 0, 0
		handler := publicRateLimit(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { calls++; w.WriteHeader(204) }), NewRateLimitInterceptor(0.0001, 1).WithRejectHook(func() { rejections++ }))
		first := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(first, req)
		second := httptest.NewRecorder()
		handler.ServeHTTP(second, req)
		if calls != 1 || rejections != 1 || second.Code < 400 {
			t.Fatal("authentication work bypassed admission rate", path, calls, rejections, second.Code)
		}
		health := httptest.NewRecorder()
		handler.ServeHTTP(health, httptest.NewRequest(http.MethodPost, "/grpc.health.v1.Health/Check", nil))
		if health.Code != 204 || calls != 2 {
			t.Fatal("liveness charged an exhausted bucket")
		}
	}
}

func TestPublicRateRecoveryPreservesDownstreamAndDenialMetric(t *testing.T) {
	owner := NewRateLimitInterceptor(math.Inf(1), 1)
	owner.now = func() time.Time { return time.Unix(100, 0) }
	runtime := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
	recovery, err := NewRateRecoveryRuntime(runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	calls, rejected := 0, 0
	owner.WithRejectHook(func() { rejected++ })
	handler := publicRateLimitWithRecovery(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}), owner, recovery)
	request := func(path string) int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		return response.Code
	}
	if request("/graph.v1.LanternService/GetVertex") != http.StatusTooManyRequests || calls != 0 {
		t.Fatal("data bypassed invalid ordinary admission")
	}
	for range rateRecoveryBurst {
		if request(graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure) != http.StatusUnauthorized {
			t.Fatal("recovery bypassed downstream refusal or initial allowance")
		}
	}
	if request(graphv1connect.LanternSecurityServiceApplySecurityChangesProcedure) != http.StatusTooManyRequests || calls != rateRecoveryBurst || rejected != 2 {
		t.Fatalf("bounded recovery calls/rejections = %d/%d", calls, rejected)
	}
	if request("/grpc.health.v1.Health/Check") != http.StatusUnauthorized || calls != rateRecoveryBurst+1 || rejected != 2 {
		t.Fatal("health exemption changed or consumed recovery credit")
	}
}

// This real Connect test exercises ingress routing and the unchanged original
// handler refusal. The synthetic guard is not native credential/CSRF evidence.
func TestPublicRateRecoveryRealConnect(t *testing.T) {
	owner := NewRateLimitInterceptor(math.NaN(), 1)
	owner.now = func() time.Time { return time.Unix(100, 0) }
	runtime := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
	recovery, err := NewRateRecoveryRuntime(runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var guardCalls atomic.Int32
	mux.Handle(graphv1connect.LanternSecurityServiceGetCurrentPrincipalProcedure, connect.NewUnaryHandler(graphv1connect.LanternSecurityServiceGetCurrentPrincipalProcedure, func(_ context.Context, req *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
		guardCalls.Add(1)
		if req.Header().Get("Authorization") != "Bearer isolated-fixture" {
			return nil, connect.NewError(connect.CodeUnauthenticated, nil)
		}
		return connect.NewResponse(&pb.GetCurrentPrincipalResponse{Identity: &pb.SecurityIdentity{Subject: "isolated-owner"}}), nil
	}))
	mux.Handle(graphv1connect.LanternServiceGetVertexProcedure, connect.NewUnaryHandler(graphv1connect.LanternServiceGetVertexProcedure, func(context.Context, *connect.Request[pb.GetVertexRequest]) (*connect.Response[pb.GetVertexResponse], error) {
		return connect.NewResponse(&pb.GetVertexResponse{Vertex: &pb.Vertex{Key: "after-repair"}}), nil
	}))
	server := httptest.NewServer(publicRateLimitWithRecovery(mux, owner, recovery))
	defer server.Close()
	control := graphv1connect.NewLanternSecurityServiceClient(server.Client(), server.URL)
	data := graphv1connect.NewLanternServiceClient(server.Client(), server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := connect.NewRequest(&pb.GetCurrentPrincipalRequest{})
	request.Header().Set("Authorization", "Bearer isolated-fixture")
	principal, err := control.GetCurrentPrincipal(ctx, request)
	if err != nil || principal.Msg.GetIdentity().GetSubject() != "isolated-owner" {
		t.Fatal("closed ordinary bucket lost bounded management routing", principal, err)
	}
	if _, err := control.GetCurrentPrincipal(ctx, connect.NewRequest(&pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("recovery bypassed original handler guard", err)
	}
	if _, err := data.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "blocked"})); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("data borrowed management allowance", err)
	}
	if guardCalls.Load() != 2 {
		t.Fatal("management guard did not execute exactly once per admitted call")
	}
	if err := owner.Configure(0, 0); err != nil {
		t.Fatal(err)
	}
	vertex, err := data.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "after-repair"}))
	if err != nil || vertex.Msg.GetVertex().GetKey() != "after-repair" {
		t.Fatal("repaired ordinary owner not visible on mounted handler", vertex, err)
	}
}

func TestPublicRateLimitResidentOwner(t *testing.T) {
	owner := NewRateLimitInterceptor(0, 0)
	calls, rejections := 0, 0
	owner.WithRejectHook(func() { rejections++ })
	handler := publicRateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}), owner)
	request := func(path string) int {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		return response.Code
	}
	if status := request("/auth/login"); status != http.StatusNoContent {
		t.Fatalf("disabled owner returned %d", status)
	}
	if err := owner.Configure(0.0001, 1); err != nil {
		t.Fatal(err)
	}
	// Auth and streams share one admission bucket; mounting while disabled
	// must not omit the rate gate when the policy is enabled later.
	if request("/graph.v1.LanternChangeService/WatchChanges") != http.StatusNoContent || request("/auth/login") != http.StatusTooManyRequests {
		t.Fatal("existing public wrapper did not apply the enabled policy")
	}
	if calls != 2 || rejections != 1 {
		t.Fatalf("calls/rejections = %d/%d, want 2/1", calls, rejections)
	}
	if err := owner.Configure(0, 0); err != nil || request("/auth/login") != http.StatusNoContent {
		t.Fatalf("disable not applied by existing wrapper: %v", err)
	}
}
