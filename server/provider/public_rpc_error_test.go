package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestPublicRPCErrorPreservesNegotiatedProtocol(t *testing.T) {
	for _, contentType := range []string{"application/json", "application/connect+proto", "application/grpc", "application/grpc-web+proto"} {
		t.Run(contentType, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/graph.v1.LanternChangeService/WatchChanges", nil)
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			publicRPCError(w, req, connect.CodeUnauthenticated)
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("error response may be cached")
			}
			if contentType == "application/json" {
				if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":"unauthenticated"`) {
					t.Fatal("Connect unary status lost", w.Code, w.Body.String())
				}
			} else if w.Code != http.StatusOK || w.Header().Get("Content-Type") != contentType {
				t.Fatal("stream protocol status lost", w.Code, w.Header())
			}
			if contentType == "application/grpc" && w.Header().Get("Grpc-Status") != "16" {
				t.Fatal("gRPC authentication status lost", w.Header())
			}
		})
	}
}
