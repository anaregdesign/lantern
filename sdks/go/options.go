// Package client: options.go — the Connect-only client option set.
//
// Pre-#367 the SDK exposed two parallel option types (Option for the
// legacy gRPC transport, ConnectOption for the additive Connect path).
// The gRPC transport has now been retired; this file collapses both
// names to a single canonical Option = ConnectOption alias so existing
// source compiles while the docstring directs callers to the v1.0
// replacement for any knob that disappeared with the gRPC dial path.
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

// options collects the knobs the Connect-backed client accepts.
//
// Pre-#367 the struct also carried gRPC-only fields
// (dialOptions, transportCreds, serviceConfigJSON, keepalive); those
// have been deleted alongside the gRPC dial path. Callers that need
// per-RPC OpenTelemetry / compression / interceptors layer them in via
// WithConnectClientOption.
type options struct {
	httpClient     *http.Client
	clientOptions  []connect.ClientOption
	defaultTimeout time.Duration
	batchChunkSize int
}

// Option configures a Lantern client built via NewLantern /
// NewLanternConnect. Both constructors accept this single Option type
// so call sites that previously passed gRPC-flavoured options keep
// compiling — the runtime behaviour of unmapped options simply
// disappears (see the WithXxx docstrings for each surface change).
type Option func(*options)

// ConnectOption is the historical name used by NewLanternConnect.
// Kept as a true alias of Option so callers using either name keep
// compiling. New code should use Option directly.
type ConnectOption = Option

// WithHTTPClient supplies the http.Client used by the Connect
// transport. When omitted, NewLantern / NewLanternConnect builds an
// h2c-capable client via defaultH2CClient (HTTP/2 over plaintext) so
// the SDK works out of the box against the Lantern primary listener.
//
// For production over TLS supply a TLS-configured http.Client: build
// an http2.Transport with a real *tls.Config and supply it via
// &http.Client{Transport: ...}. mTLS replaces the legacy
// WithTransportCredentials path — wire the client cert into the same
// *tls.Config.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithConnectClientOption forwards arbitrary connect.ClientOption
// values (interceptors, codec selection, gzip compression, OTel) to
// the generated Connect client constructor. Use this as the escape
// hatch for anything the named WithXxx helpers do not cover:
//
//   - connect.WithGRPC()                    — force the gRPC binary wire
//   - connect.WithSendCompression("gzip")   — replace legacy WithCompression
//   - otelconnect.NewInterceptor()          — replace legacy WithOpenTelemetry
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
//
// This option supersedes the previous WithConnectBatchChunkSize name;
// both functions still exist (the latter aliased to this one) so
// existing source compiles.
func WithBatchChunkSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.batchChunkSize = n
		}
	}
}

// WithConnectBatchChunkSize is a deprecated alias for
// WithBatchChunkSize, kept so call sites built against the additive
// Connect transport (#338) keep compiling.
//
// Deprecated: use WithBatchChunkSize.
func WithConnectBatchChunkSize(n int) Option { return WithBatchChunkSize(n) }
