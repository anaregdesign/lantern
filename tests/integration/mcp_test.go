package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	lmcp "github.com/anaregdesign/lantern/mcp"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// newInProcessClientWithPrefix is a variant of newInProcessClient whose
// backing GraphCache has the prefix index enabled, so list_under (which
// drives ScanVertices) actually returns results. The default shared
// helper does not enable it because most integration tests don't need
// the index and we want to keep their behaviour unchanged.
func newInProcessClientWithPrefix(t *testing.T) (*client.Lantern, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 16)

	gc := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	gc.EnablePrefixIndex(func(s string) string { return s })

	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})
	srv := grpc.NewServer(grpc.UnaryInterceptor(vi.UnaryServerInterceptor()))
	pb.RegisterLanternServiceServer(srv, service.NewLanternService(gc))

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("grpc Serve returned: %v", err)
		}
	}()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	l, err := client.NewLantern(
		"passthrough://bufconn",
		client.WithTransportCredentials(insecure.NewCredentials()),
		client.WithDialOption(grpc.WithContextDialer(dialer)),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	cleanup := func() {
		_ = l.Close()
		srv.Stop()
		_ = lis.Close()
	}
	return l, cleanup
}

// TestMCP_EndToEnd exercises the 6-tool MCP surface through a real MCP
// client session over InMemoryTransport, backed by a real Lantern gRPC
// service (bufconn). It is the smoke test that proves the whole stack
// — schema inference, argument validation, SDK round-trip, and result
// shape — agrees end-to-end.
func TestMCP_EndToEnd(t *testing.T) {
	lan, cleanup := newInProcessClientWithPrefix(t)
	defer cleanup()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	srv, err := lmcp.NewServer(lan, logger)
	if err != nil {
		t.Fatalf("lmcp.NewServer: %v", err)
	}

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverSession, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	c := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "test"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool(%s): %v", name, err)
		}
		if res.IsError {
			t.Fatalf("CallTool(%s) IsError; content=%+v", name, res.Content)
		}
		return res
	}

	decode := func(res *mcp.CallToolResult, v any) {
		t.Helper()
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}

	// remember_fact then recall_fact then forget.
	call("remember_fact", map[string]any{"key": "u.tone", "value": "warm", "ttl": "turn"})

	recallRes := call("recall_fact", map[string]any{"key": "u.tone"})
	var recall struct {
		Found bool   `json:"found"`
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	decode(recallRes, &recall)
	if !recall.Found || recall.Value != "warm" {
		t.Fatalf("recall mismatch: %+v", recall)
	}

	forgetRes := call("forget", map[string]any{"key": "u.tone"})
	var forget struct {
		Existed bool `json:"existed"`
	}
	decode(forgetRes, &forget)
	if !forget.Existed {
		t.Fatalf("expected Existed=true; got %+v", forget)
	}

	missRes := call("recall_fact", map[string]any{"key": "u.tone"})
	var miss struct {
		Found bool `json:"found"`
	}
	decode(missRes, &miss)
	if miss.Found {
		t.Fatalf("expected Found=false after forget; got %+v", miss)
	}

	// Additive relations: write twice, recall_related should see the
	// summed weight to the neighbour.
	call("remember_fact", map[string]any{"key": "fact.a", "value": "A", "ttl": "turn"})
	call("remember_fact", map[string]any{"key": "fact.b", "value": "B", "ttl": "turn"})
	call("remember_relation", map[string]any{"from": "fact.a", "to": "fact.b", "ttl": "turn"})
	call("remember_relation", map[string]any{"from": "fact.a", "to": "fact.b", "ttl": "turn"})

	relRes := call("recall_related", map[string]any{"seed": "fact.a", "step": 1, "k": 4})
	var rel struct {
		Seed      string `json:"seed"`
		Neighbors []struct {
			Key    string  `json:"key"`
			Weight float32 `json:"weight"`
		} `json:"neighbors"`
	}
	decode(relRes, &rel)
	if rel.Seed != "fact.a" {
		t.Fatalf("seed = %q", rel.Seed)
	}
	var seenB bool
	for _, n := range rel.Neighbors {
		if n.Key == "fact.b" {
			seenB = true
			if n.Weight < 1.5 {
				t.Errorf("additive write should yield weight \u2265 1.5; got %v", n.Weight)
			}
		}
	}
	if !seenB {
		t.Fatalf("fact.b missing from neighbours: %+v", rel.Neighbors)
	}

	// list_under should see fact.a + fact.b.
	listRes := call("list_under", map[string]any{"prefix": "fact."})
	var list struct {
		Count   int  `json:"count"`
		HasMore bool `json:"has_more"`
	}
	decode(listRes, &list)
	if list.Count < 2 {
		t.Fatalf("expected at least 2 entries under fact.; got %d", list.Count)
	}
}
