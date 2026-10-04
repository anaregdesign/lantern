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
