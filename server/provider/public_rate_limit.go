package provider

import (
	"connectrpc.com/connect"
	"errors"
	"net/http"
	"strings"
)

// Rate limiting precedes JWT verification, session lookup, discovery and
// streaming admission. Production Connect handlers do not charge it twice.
func publicRateLimit(handler http.Handler, limiter *RateLimitInterceptor) http.Handler {
	if limiter == nil || limiter.lim == nil {
		return handler
	}
	writer := connect.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/grpc.health.v1.Health/Check" || limiter.lim.Allow() {
			handler.ServeHTTP(w, req)
			return
		}
		if limiter.rejectHook != nil {
			limiter.rejectHook()
		}
		if strings.HasPrefix(req.URL.Path, "/auth/") {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		_ = writer.Write(w, req, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded")))
	})
}
