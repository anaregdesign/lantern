package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
