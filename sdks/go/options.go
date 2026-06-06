// Package client: options.go owns the constructor option set.
package client

import (
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
