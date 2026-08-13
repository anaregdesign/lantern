package mcp

import (
	"context"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// fakeLantern is a hand-rolled test double for lanternClient. It records
// every call and returns the configured response, making it trivial to
// assert both that a handler invoked the correct SDK method and that it
// propagated the response shape into the MCP tool result.
type fakeLantern struct {
	// PutVertex
	putVertexErr     error
	putVertexOutcome client.PutOutcome
	lastPutKey       string
	lastPutValue     any
	lastPutTTL       time.Duration
	putVertexCalls   int

	// GetVertex
	getVertexFn func(ctx context.Context, key string) (*client.Vertex, error)
	lastGetKey  string

	// GetVertices
	getVerticesFn       func(ctx context.Context, keys []string) ([]*client.Vertex, []string, error)
	lastGetVerticesKeys []string

	// DeleteVertex
	deleteVertexFn func(ctx context.Context, key string) (bool, error)
	lastDeleteKey  string

	// ScanVertices
	scanVerticesFn  func(ctx context.Context, prefix string, opts ...client.ScanOption) ([]*client.Vertex, []byte, error)
	lastScanPrefix  string
	lastScanOptions []client.ScanOption

	// CountVerticesByPrefix
	countByPrefixFn func(ctx context.Context, prefix string) (uint64, error)
	lastCountPrefix string

	// AddEdge
	addEdgeFn      func(ctx context.Context, tail, head string, weight float32, ttl time.Duration) error
	addEdgeErr     error
	lastEdgeTail   string
	lastEdgeHead   string
	lastEdgeWeight float32
	lastEdgeTTL    time.Duration
	addEdgeCalls   int
	// addEdgeEffectiveFn, when set, supplies the effective (live accumulated)
	// weight AddEdge returns — modeling the serving node's
	// AddEdgeResponse.effective_weight (#897). Unset → AddEdge echoes the
	// increment, the legacy single-writer behavior.
	addEdgeEffectiveFn func(tail, head string, weight float32) float32

	// AddEdges
	addEdgesFn   func(ctx context.Context, inputs []client.EdgeInput) error
	lastAddEdges []client.EdgeInput

	// ScanEdges
	scanEdgesFn     func(ctx context.Context, opts ...client.EdgeScanOption) ([]*client.Edge, []byte, error)
	scanEdgesCalls  int
	lastScanEdgeOpt []client.EdgeScanOption

	// Illuminate
	illuminateFn  func(ctx context.Context, seed string, opts ...client.IlluminateOption) (*client.Graph, error)
	lastSeed      string
	lastIllumOpts []client.IlluminateOption
	illuminateErr error
	illuminateRes *client.Graph

	// Ping
	pingErr error
}

func (f *fakeLantern) PutVertex(_ context.Context, key string, value any, ttl time.Duration) (client.PutOutcome, error) {
	f.putVertexCalls++
	f.lastPutKey = key
	f.lastPutValue = value
	f.lastPutTTL = ttl
	outcome := f.putVertexOutcome
	if outcome == 0 {
		outcome = client.PutOutcomeAppliedAndLive
	}
	return outcome, f.putVertexErr
}

func (f *fakeLantern) GetVertex(ctx context.Context, key string) (*client.Vertex, error) {
	f.lastGetKey = key
	if f.getVertexFn != nil {
		return f.getVertexFn(ctx, key)
	}
	return nil, nil
}

func (f *fakeLantern) GetVertices(ctx context.Context, keys []string) ([]*client.Vertex, []string, error) {
	f.lastGetVerticesKeys = keys
	if f.getVerticesFn != nil {
		return f.getVerticesFn(ctx, keys)
	}
	return nil, nil, nil
}

func (f *fakeLantern) DeleteVertex(ctx context.Context, key string) (bool, error) {
	f.lastDeleteKey = key
	if f.deleteVertexFn != nil {
		return f.deleteVertexFn(ctx, key)
	}
	return false, nil
}

func (f *fakeLantern) ScanVertices(ctx context.Context, prefix string, opts ...client.ScanOption) ([]*client.Vertex, []byte, error) {
	f.lastScanPrefix = prefix
	f.lastScanOptions = opts
	if f.scanVerticesFn != nil {
		return f.scanVerticesFn(ctx, prefix, opts...)
	}
	return nil, nil, nil
}

func (f *fakeLantern) CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error) {
	f.lastCountPrefix = prefix
	if f.countByPrefixFn != nil {
		return f.countByPrefixFn(ctx, prefix)
	}
	return 0, nil
}

func (f *fakeLantern) AddEdges(ctx context.Context, inputs []client.EdgeInput) ([]float32, error) {
	f.lastAddEdges = inputs
	if f.addEdgesFn != nil {
		if err := f.addEdgesFn(ctx, inputs); err != nil {
			return nil, err
		}
	}
	eff := make([]float32, len(inputs))
	for i, in := range inputs {
		eff[i] = in.Weight
	}
	return eff, nil
}

func (f *fakeLantern) AddEdge(ctx context.Context, tail, head string, weight float32, ttl time.Duration) (float32, error) {
	f.addEdgeCalls++
	f.lastEdgeTail = tail
	f.lastEdgeHead = head
	f.lastEdgeWeight = weight
	f.lastEdgeTTL = ttl
	if f.addEdgeFn != nil {
		if err := f.addEdgeFn(ctx, tail, head, weight, ttl); err != nil {
			return 0, err
		}
	}
	if f.addEdgeErr != nil {
		return 0, f.addEdgeErr
	}
	if f.addEdgeEffectiveFn != nil {
		return f.addEdgeEffectiveFn(tail, head, weight), nil
	}
	return weight, nil
}

func (f *fakeLantern) ScanEdges(ctx context.Context, opts ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
	f.scanEdgesCalls++
	f.lastScanEdgeOpt = opts
	if f.scanEdgesFn != nil {
		return f.scanEdgesFn(ctx, opts...)
	}
	return nil, nil, nil
}

func (f *fakeLantern) Illuminate(ctx context.Context, seed string, opts ...client.IlluminateOption) (*client.Graph, error) {
	f.lastSeed = seed
	f.lastIllumOpts = opts
	if f.illuminateFn != nil {
		return f.illuminateFn(ctx, seed, opts...)
	}
	if f.illuminateErr != nil {
		return nil, f.illuminateErr
	}
	return f.illuminateRes, nil
}

func (f *fakeLantern) Ping(_ context.Context) error {
	return f.pingErr
}
