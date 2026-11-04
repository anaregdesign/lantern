package provider

import (
	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"net/http"
	"strings"
)

// Rate limiting precedes JWT verification, session lookup, discovery and
// streaming admission. Production Connect handlers do not charge it twice.
func publicRateLimit(handler http.Handler, limiter *RateLimitInterceptor) http.Handler {
	return publicRateLimitWithRecovery(handler, limiter, nil)
}

func publicRateLimitWithRecovery(handler http.Handler, limiter *RateLimitInterceptor, recovery *rateRecoveryRuntime) http.Handler {
	if limiter == nil {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/grpc.health.v1.Health/Check" || limiter.Allow() || recovery.allow(req) {
			handler.ServeHTTP(w, req)
			return
		}
		limiter.rejected()
		if strings.HasPrefix(req.URL.Path, "/auth/") {
			security.BindCurrentFailure(w)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		publicRPCError(w, req, connect.CodeResourceExhausted)
	})
}
