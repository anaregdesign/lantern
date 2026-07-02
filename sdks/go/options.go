// Package client: options.go owns the constructor option set.
package client

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
)

// defaultBatchChunkSize is used by PutVertices / AddEdges / PutEdges
// to slice large input slices into RPC-sized batches. The server's
// MaxBatchSize cap defaults to 10_000 — we stay well below that to
// leave headroom for per-message overhead.
const defaultBatchChunkSize = 1000

// options collects the knobs a Connect-backed Lantern client accepts.
// Callers configure them via the WithXxx helpers below.
type options struct {
	httpClient     *http.Client
	clientOptions  []connect.ClientOption
	defaultTimeout time.Duration
	batchChunkSize int
	idempotentAdds bool
}

// Option configures a Lantern client built via NewLantern.
type Option func(*options)

// WithHTTPClient supplies the http.Client used by the Connect
// transport. When omitted, NewLantern builds an h2c-capable client via
// defaultH2CClient (HTTP/2 over plaintext) so the SDK works out of the
// box against the Lantern primary listener.
//
// For TLS / mTLS supply a TLS-configured http.Client: build an
// http2.Transport with a real *tls.Config and wrap it in
// &http.Client{Transport: ...}.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithConnectClientOption forwards arbitrary connect.ClientOption
// values (interceptors, codec selection, gzip compression, OTel) to
// the generated Connect client constructor. Use this as the escape
// hatch for anything the named WithXxx helpers do not cover, for
// example:
//
//   - connect.WithGRPC()                    — force the gRPC binary wire
//   - connect.WithSendCompression("gzip")   — gzip request bodies
//   - otelconnect.NewInterceptor()          — OTel instrumentation
func WithConnectClientOption(opts ...connect.ClientOption) Option {
	return func(o *options) { o.clientOptions = append(o.clientOptions, opts...) }
}

// WithAuthToken attaches "Authorization: Bearer <token>" to every RPC —
// unary and streaming — matching the server's LANTERN_AUTH_TOKENS bearer
// auth (#850). An empty token is a no-op. Works unchanged through
// NewLanternFailover (each endpoint client inherits the option).
//
// Bearer tokens over plaintext h2c are sniffable: pair with
// WithHTTPClient(TLS-configured client) outside trusted networks.
func WithAuthToken(token string) Option {
	return func(o *options) {
		if token == "" {
			return
		}
		o.clientOptions = append(o.clientOptions, connect.WithInterceptors(authTokenInterceptor(token)))
	}
}

// authTokenInterceptor is the client-side bearer injector behind
// WithAuthToken. It implements all three connect.Interceptor hooks so
// streaming RPCs (Subscribe-style) carry the header too.
type authTokenInterceptor string

func (t authTokenInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+string(t))
		return next(ctx, req)
	}
}

func (t authTokenInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set("Authorization", "Bearer "+string(t))
		return conn
	}
}

func (t authTokenInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next // client-side only
}

// WithDefaultTimeout applies a per-call timeout to every RPC whose
// context has no deadline. RPCs whose caller already supplied a
// deadline are left alone. Pass 0 (the default) to disable.
func WithDefaultTimeout(d time.Duration) Option {
	return func(o *options) { o.defaultTimeout = d }
}

// WithBatchChunkSize overrides the auto-chunk size used by
// PutVertices, AddEdges, PutEdges, DeleteVertices, DeleteEdges,
// GetVertices, and GetEdges. Must be > 0; otherwise the default
// (1000) is kept.
func WithBatchChunkSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.batchChunkSize = n
		}
	}
}

// WithIdempotentAdds makes AddEdge / AddEdgeAt / AddEdges attach a
// client-generated, per-call idempotency key (ContribID) to every edge
// contribution (#588). The server records each ContribID at most once, so a
// transport-level retry that re-sends the identical request — e.g. a Connect
// retry interceptor reacting to a transient Unavailable — adds the edge
// weight exactly once instead of double-counting.
//
// The guarantee is scoped to retries of a single logical call: the SDK bakes
// the keys into the request when it is built, and a transport retry re-sends
// those same bytes. Calling AddEdge twice yourself is still two distinct
// contributions (their keys differ), preserving the additive semantics of
// the API. Off by default; opt in per client.
func WithIdempotentAdds() Option {
	return func(o *options) { o.idempotentAdds = true }
}
