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
	putVertexErr   error
	lastPutKey     string
	lastPutValue   any
	lastPutTTL     time.Duration
	putVertexCalls int

	// GetVertex
	getVertexFn func(ctx context.Context, key string) (*client.Vertex, error)
	lastGetKey  string

	// DeleteVertex
	deleteVertexFn func(ctx context.Context, key string) (bool, error)
	lastDeleteKey  string

	// ScanVertices
	scanVerticesFn  func(ctx context.Context, prefix string, opts ...client.ScanOption) ([]*client.Vertex, []byte, error)
	lastScanPrefix  string
	lastScanOptions []client.ScanOption

	// AddEdge
	addEdgeErr     error
	lastEdgeTail   string
	lastEdgeHead   string
	lastEdgeWeight float32
	lastEdgeTTL    time.Duration
	addEdgeCalls   int

	// GetEdge
	getEdgeFn       func(ctx context.Context, tail, head string) (*client.Edge, error)
	lastGetEdgeTail string
	lastGetEdgeHead string

	// Illuminate
	illuminateFn  func(ctx context.Context, seed string, opts ...client.IlluminateOption) (*client.Graph, error)
	lastSeed      string
	lastIllumOpts []client.IlluminateOption
	illuminateErr error
	illuminateRes *client.Graph

	// Ping
	pingErr error
}

func (f *fakeLantern) PutVertex(_ context.Context, key string, value any, ttl time.Duration) error {
	f.putVertexCalls++
	f.lastPutKey = key
	f.lastPutValue = value
	f.lastPutTTL = ttl
	return f.putVertexErr
}

func (f *fakeLantern) GetVertex(ctx context.Context, key string) (*client.Vertex, error) {
	f.lastGetKey = key
	if f.getVertexFn != nil {
		return f.getVertexFn(ctx, key)
	}
	return nil, nil
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

func (f *fakeLantern) AddEdge(_ context.Context, tail, head string, weight float32, ttl time.Duration) error {
	f.addEdgeCalls++
	f.lastEdgeTail = tail
	f.lastEdgeHead = head
	f.lastEdgeWeight = weight
	f.lastEdgeTTL = ttl
	return f.addEdgeErr
}

func (f *fakeLantern) GetEdge(ctx context.Context, tail, head string) (*client.Edge, error) {
	f.lastGetEdgeTail = tail
	f.lastGetEdgeHead = head
	if f.getEdgeFn != nil {
		return f.getEdgeFn(ctx, tail, head)
	}
	return nil, nil
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
