package provider

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// Only generated codecs/pools are request-owned; the typed service and its
// immutable schema remain shared. CurrentOutput reserves actual connection and
// request credit before invoking this factory.
func (r *SecurityRuntime) publicRPCHandler(path string, create func(...connect.HandlerOption) (string, http.Handler), options ...connect.HandlerOption) (string, http.Handler) {
	if r.current == nil {
		return create(options...)
	}
	return path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.output == nil {
			panic(http.ErrAbortHandler)
		}
		r.output.Handler(func() http.Handler {
			_, handler := create(r.output.Options(options...)...)
			return handler
		}).ServeHTTP(w, req)
	})
}

func (r *SecurityRuntime) publicOutputMetadata(next http.Handler) http.Handler {
	if r.current == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var kind security.CurrentPublicMetadata
		switch {
		case req.Method == http.MethodOptions:
			kind = security.CurrentCORSMetadata
		case req.URL.Path == "/grpc.health.v1.Health/Check":
			kind = security.CurrentHealthMetadata
		}
		if kind != 0 && r.current.BindPublicMetadata(req.Context(), kind) != nil {
			panic(http.ErrAbortHandler)
		}
		next.ServeHTTP(w, req)
	})
}
