package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// unavailableErr builds the connect error the SDK surfaces when a Lantern
// node is unreachable (connection refused) or returns CodeUnavailable.
func unavailableErr() error {
	return connect.NewError(connect.CodeUnavailable, errors.New("node down"))
}

func TestParseLanternAddrs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "http://a:6380", []string{"http://a:6380"}},
		{"two", "http://a:6380,http://b:6380", []string{"http://a:6380", "http://b:6380"}},
		{"trim-and-drop-empties", " http://a:6380 , , http://b:6380 ,", []string{"http://a:6380", "http://b:6380"}},
		{"empty", "", nil},
		{"only-separators", " , ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLanternAddrs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("parseLanternAddrs(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseLanternAddrs(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestIsUnavailable(t *testing.T) {
	if isUnavailable(nil) {
		t.Fatal("nil must not be classified unavailable")
	}
	if !isUnavailable(unavailableErr()) {
		t.Fatal("CodeUnavailable must be classified unavailable")
	}
	if isUnavailable(connect.NewError(connect.CodeNotFound, errors.New("nf"))) {
		t.Fatal("CodeNotFound must not be classified unavailable")
	}
	if isUnavailable(client.ErrNotFound) {
		t.Fatal("sentinel ErrNotFound must not be classified unavailable")
	}
	if isUnavailable(errors.New("plain")) {
		t.Fatal("plain (non-connect) error must not be classified unavailable")
	}
}

func TestNewFailoverClient_RejectsEmpty(t *testing.T) {
	if _, err := newFailoverClient(nil); err == nil {
		t.Fatal("newFailoverClient(nil) returned nil error")
	}
}

func TestNewFailoverClient_DialsAllAddrs(t *testing.T) {
	// NewLantern is lazy (no connection established until first RPC), so
	// this constructs two endpoints without a live server.
	f, err := newFailoverClient([]string{"http://127.0.0.1:6380", "http://127.0.0.1:6381"})
	if err != nil {
		t.Fatalf("newFailoverClient err = %v", err)
	}
	defer func() { _ = f.Close() }()
	if len(f.nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(f.nodes))
	}
}

func TestNewFailoverClient_RejectsBadAddr(t *testing.T) {
	if _, err := newFailoverClient([]string{"http://ok:6380", "no-scheme"}); err == nil {
		t.Fatal("expected error for schemeless address")
	}
}

func TestFailover_RotatesOnUnavailableReadAndSticks(t *testing.T) {
	var n0, n1 int
	node0 := &fakeLantern{getVertexFn: func(_ context.Context, _ string) (*client.Vertex, error) {
		n0++
		return nil, unavailableErr()
	}}
	node1 := &fakeLantern{getVertexFn: func(_ context.Context, key string) (*client.Vertex, error) {
		n1++
		return &client.Vertex{Key: key}, nil
	}}
	f := &failoverClient{nodes: []lanternClient{node0, node1}}

	v, err := f.GetVertex(context.Background(), "k")
	if err != nil {
		t.Fatalf("GetVertex err = %v", err)
	}
	if v == nil || v.Key != "k" {
		t.Fatalf("GetVertex returned %+v, want vertex with key k", v)
	}
	if n0 != 1 || n1 != 1 {
		t.Fatalf("call counts n0=%d n1=%d, want 1 and 1", n0, n1)
	}

	// After rotating to node1 the wrapper is sticky: a second call starts
	// at node1 and never touches the known-dead node0.
	if _, err := f.GetVertex(context.Background(), "k2"); err != nil {
		t.Fatalf("second GetVertex err = %v", err)
	}
	if n0 != 1 {
		t.Fatalf("node0 retried after rotation: n0=%d, want 1", n0)
	}
	if n1 != 2 {
		t.Fatalf("node1 not sticky: n1=%d, want 2", n1)
	}
}

func TestFailover_DoesNotRotateOnAppError(t *testing.T) {
	var n0, n1 int
	node0 := &fakeLantern{getVertexFn: func(_ context.Context, _ string) (*client.Vertex, error) {
		n0++
		return nil, client.ErrNotFound
	}}
	node1 := &fakeLantern{getVertexFn: func(_ context.Context, _ string) (*client.Vertex, error) {
		n1++
		return nil, nil
	}}
	f := &failoverClient{nodes: []lanternClient{node0, node1}}

	_, err := f.GetVertex(context.Background(), "k")
	if !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n0 != 1 || n1 != 0 {
		t.Fatalf("call counts n0=%d n1=%d, want 1 and 0 (no rotation on app error)", n0, n1)
	}
}

func TestFailover_AllNodesUnavailableReturnsError(t *testing.T) {
	mk := func(c *int) *fakeLantern {
		return &fakeLantern{getVertexFn: func(_ context.Context, _ string) (*client.Vertex, error) {
			*c++
			return nil, unavailableErr()
		}}
	}
	var a, b, c int
	f := &failoverClient{nodes: []lanternClient{mk(&a), mk(&b), mk(&c)}}

	_, err := f.GetVertex(context.Background(), "k")
	if !isUnavailable(err) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	if a != 1 || b != 1 || c != 1 {
		t.Fatalf("each node should be tried exactly once: a=%d b=%d c=%d", a, b, c)
	}
}

func TestFailover_PingTriesAllNodesUntilHealthy(t *testing.T) {
	node0 := &fakeLantern{pingErr: errors.New("connection refused")}
	node1 := &fakeLantern{pingErr: nil}
	f := &failoverClient{nodes: []lanternClient{node0, node1}}

	if err := f.Ping(context.Background()); err != nil {
		t.Fatalf("Ping err = %v, want nil (node1 healthy)", err)
	}

	// Ping should leave the wrapper sticky to the healthy node1, so a
	// subsequent data call starts there.
	var n0, n1 int
	node0.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) { n0++; return nil, nil }
	node1.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) { n1++; return nil, nil }
	if _, err := f.GetVertex(context.Background(), "k"); err != nil {
		t.Fatalf("GetVertex err = %v", err)
	}
	if n0 != 0 || n1 != 1 {
		t.Fatalf("after Ping stickiness n0=%d n1=%d, want 0 and 1", n0, n1)
	}
}

func TestFailover_PingAllNodesFailReturnsError(t *testing.T) {
	f := &failoverClient{nodes: []lanternClient{
		&fakeLantern{pingErr: errors.New("down0")},
		&fakeLantern{pingErr: errors.New("down1")},
	}}
	if err := f.Ping(context.Background()); err == nil {
		t.Fatal("Ping returned nil, want error when all nodes are down")
	}
}

func TestFailover_AdditiveWriteRotatesOnUnavailable(t *testing.T) {
	var n0, n1 int
	node0 := &fakeLantern{addEdgeFn: func(_ context.Context, _, _ string, _ float32, _ time.Duration) error {
		n0++
		return unavailableErr()
	}}
	node1 := &fakeLantern{addEdgeFn: func(_ context.Context, _, _ string, _ float32, _ time.Duration) error {
		n1++
		return nil
	}}
	f := &failoverClient{nodes: []lanternClient{node0, node1}}

	if err := f.AddEdge(context.Background(), "a", "b", 1, time.Hour); err != nil {
		t.Fatalf("AddEdge err = %v", err)
	}
	if n0 != 1 || n1 != 1 {
		t.Fatalf("call counts n0=%d n1=%d, want 1 and 1 (rotated to healthy replica)", n0, n1)
	}
}

func TestFailover_SingleNodePropagatesUnavailable(t *testing.T) {
	node := &fakeLantern{getVertexFn: func(_ context.Context, _ string) (*client.Vertex, error) {
		return nil, unavailableErr()
	}}
	f := &failoverClient{nodes: []lanternClient{node}}
	if _, err := f.GetVertex(context.Background(), "k"); !isUnavailable(err) {
		t.Fatalf("single-node unavailable err = %v, want unavailable", err)
	}
}

func TestFailover_ScanVerticesRotatesAndReturnsCursor(t *testing.T) {
	node0 := &fakeLantern{scanVerticesFn: func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return nil, nil, unavailableErr()
	}}
	node1 := &fakeLantern{scanVerticesFn: func(_ context.Context, prefix string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return []*client.Vertex{{Key: prefix + "x"}}, []byte("cursor"), nil
	}}
	f := &failoverClient{nodes: []lanternClient{node0, node1}}

	vs, cur, err := f.ScanVertices(context.Background(), "p.", client.WithScanLimit(10))
	if err != nil {
		t.Fatalf("ScanVertices err = %v", err)
	}
	if len(vs) != 1 || vs[0].Key != "p.x" {
		t.Fatalf("vertices = %+v, want [p.x]", vs)
	}
	if string(cur) != "cursor" {
		t.Fatalf("cursor = %q, want \"cursor\"", cur)
	}
}
