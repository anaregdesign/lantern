package client

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/encoding/gzip" // register gzip so WithCompression("gzip") works
)

// defaultServiceConfig enables transparent gRPC retries on idempotent RPCs.
// AddEdge is deliberately excluded because it is additive (retrying would
// double-count weight).
const defaultServiceConfig = `{
  "methodConfig": [
    {
      "name": [
        {"service": "graph.v1.LanternService", "method": "GetVertex"},
        {"service": "graph.v1.LanternService", "method": "GetEdge"},
        {"service": "graph.v1.LanternService", "method": "PutVertex"},
        {"service": "graph.v1.LanternService", "method": "PutEdge"},
        {"service": "graph.v1.LanternService", "method": "DeleteVertex"},
        {"service": "graph.v1.LanternService", "method": "DeleteEdge"},
        {"service": "graph.v1.LanternService", "method": "DeleteVertices"},
        {"service": "graph.v1.LanternService", "method": "DeleteEdges"},
        {"service": "graph.v1.LanternService", "method": "Illuminate"}
      ],
      "retryPolicy": {
        "maxAttempts": 4,
        "initialBackoff": "0.1s",
        "maxBackoff": "5s",
        "backoffMultiplier": 2,
        "retryableStatusCodes": ["UNAVAILABLE", "RESOURCE_EXHAUSTED"]
      }
    }
  ]
}`

// defaultBatchChunkSize is used by PutVertices / AddEdges / PutEdges to slice
// large input slices into RPC-sized batches. The server's MaxBatchSize cap
// defaults to 10000 — we stay well below that to leave headroom for
// per-message overhead.
const defaultBatchChunkSize = 1000

// options collects all knobs settable via WithXxx. Zero values keep the
// previous defaults (insecure transport, no per-call timeout, default
// chunking).
type options struct {
	dialOptions       []grpc.DialOption
	transportCreds    credentials.TransportCredentials
	defaultTimeout    time.Duration
	batchChunkSize    int
	serviceConfigJSON string
}

// Option configures a Lantern client. Pass options to NewLantern.
type Option func(*options)

// WithTransportCredentials sets the gRPC transport credentials (TLS, mTLS).
// When unset, the client uses insecure credentials.
func WithTransportCredentials(creds credentials.TransportCredentials) Option {
	return func(o *options) { o.transportCreds = creds }
}

// WithDialOption appends an arbitrary grpc.DialOption. Use this for
// keepalive, balancer choice, or other transport-level tuning.
func WithDialOption(opts ...grpc.DialOption) Option {
	return func(o *options) { o.dialOptions = append(o.dialOptions, opts...) }
}

// WithDefaultServiceConfig overrides the built-in retry policy with a custom
// gRPC service config JSON. Pass "" to disable the default policy entirely.
func WithDefaultServiceConfig(json string) Option {
	return func(o *options) { o.serviceConfigJSON = json }
}

// WithDefaultTimeout applies a per-call timeout to every RPC whose context
// has no deadline. RPCs whose caller already supplied a deadline are left
// alone. Pass 0 (the default) to disable.
func WithDefaultTimeout(d time.Duration) Option {
	return func(o *options) { o.defaultTimeout = d }
}

// WithBatchChunkSize overrides the auto-chunk size used by PutVertices,
// AddEdges, and PutEdges. Must be > 0; otherwise the default is kept.
func WithBatchChunkSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.batchChunkSize = n
		}
	}
}

// WithCompression enables a per-call default compressor (e.g. "gzip"). The
// named compressor must be registered (gzip is registered automatically by
// this package). Pass "" to disable.
func WithCompression(name string) Option {
	return func(o *options) {
		if name != "" {
			o.dialOptions = append(o.dialOptions,
				grpc.WithDefaultCallOptions(grpc.UseCompressor(name)))
		}
	}
}
