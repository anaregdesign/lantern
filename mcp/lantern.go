package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// lanternClient is the narrow subset of *client.Lantern that the MCP
// tool handlers depend on. It exists so per-tool tests can swap in a
// fake without dialing the Lantern server.
//
// The interface is intentionally tied to the SDK's concrete option types
// (ScanOption, IlluminateOption); duplicating those would force the fakes
// to mirror an evolving option set. The trade-off is that the test fakes
// observe the option closures via the option-result inspection helpers in
// the SDK rather than by reading inputs directly — see the per-tool tests
// for the pattern.
type lanternClient interface {
	PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error
	GetVertex(ctx context.Context, key string) (*client.Vertex, error)
	DeleteVertex(ctx context.Context, key string) (bool, error)
	ScanVertices(ctx context.Context, prefix string, opts ...client.ScanOption) (vertices []*client.Vertex, nextCursor []byte, err error)
	AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error
	Illuminate(ctx context.Context, seed string, opts ...client.IlluminateOption) (*client.Graph, error)
	Ping(ctx context.Context) error
}

// mapSDKError translates Lantern SDK sentinel errors into a stable,
// low-cardinality form suitable for an LLM tool result. The original
// error is wrapped so callers using errors.Is on the sentinels still
// match; the prefix is purely additive context.
func mapSDKError(tool string, err error) error {
	switch {
	case errors.Is(err, client.ErrInvalidArgument):
		return fmt.Errorf("%s: invalid argument: %w", tool, err)
	case errors.Is(err, client.ErrResourceExhausted):
		return fmt.Errorf("%s: rate limited; back off and retry: %w", tool, err)
	case errors.Is(err, client.ErrNotFound):
		return fmt.Errorf("%s: not found: %w", tool, err)
	default:
		return fmt.Errorf("%s: %w", tool, err)
	}
}

// Compile-time assertion: *client.Lantern satisfies lanternClient.
var _ lanternClient = (*client.Lantern)(nil)
