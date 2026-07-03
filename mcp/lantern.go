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
	GetVertices(ctx context.Context, keys []string) (found []*client.Vertex, missing []string, err error)
	DeleteVertex(ctx context.Context, key string) (bool, error)
	ScanVertices(ctx context.Context, prefix string, opts ...client.ScanOption) (vertices []*client.Vertex, nextCursor []byte, err error)
	SearchVertices(ctx context.Context, query string, opts ...client.SearchOption) (hits []client.SearchHit, err error)
	CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error)
	DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...client.DeleteByPrefixOption) (uint64, error)
	PutVertices(ctx context.Context, inputs []client.VertexInput) error
	AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (float32, error)
	AddEdges(ctx context.Context, inputs []client.EdgeInput) ([]float32, error)
	GetEdge(ctx context.Context, tail, head string) (*client.Edge, error)
	ScanEdges(ctx context.Context, opts ...client.EdgeScanOption) (edges []*client.Edge, nextCursor []byte, err error)
	Illuminate(ctx context.Context, seed string, opts ...client.IlluminateOption) (*client.Graph, error)
	Ping(ctx context.Context) error
}

// mapSDKError translates Lantern SDK sentinel errors into a stable,
// low-cardinality form suitable for an LLM tool result. The original
// error is wrapped so callers using errors.Is on the sentinels still
// match; the prefix is purely additive context.
//
// Only ErrResourceExhausted earns a bespoke label, because "rate limited;
// back off and retry" is actionable guidance the sentinel text ("resource
// exhausted") does not convey. The other sentinels already stringify to a
// clear phrase (ErrInvalidArgument → "invalid argument", ErrNotFound →
// "not found"), so adding a matching literal label only produced doubled
// noise like "invalid argument: invalid argument"; they fall through to the
// generic "%s: %w" form alongside unclassified errors.
func mapSDKError(tool string, err error) error {
	switch {
	case errors.Is(err, client.ErrResourceExhausted):
		return fmt.Errorf("%s: rate limited; back off and retry: %w", tool, err)
	default:
		return fmt.Errorf("%s: %w", tool, err)
	}
}

// Compile-time assertion: *client.Lantern satisfies lanternClient.
var _ lanternClient = (*client.Lantern)(nil)
