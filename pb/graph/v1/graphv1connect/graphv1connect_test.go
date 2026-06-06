package graphv1connect_test

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"

	graphv1connect "github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// TestGeneratedSurface is a thin compile-check that the protoc-gen-connect-go
// output exposes the symbols downstream issues (#337 server, #338 sdks/go,
// #339 admin, #340 sdks/node) will rely on. It does not stand up a server;
// it just asserts the generated handler / client constructors and the
// service constant exist with the expected signatures.
func TestGeneratedSurface(t *testing.T) {
	t.Parallel()

	if graphv1connect.LanternServiceName != "graph.v1.LanternService" {
		t.Fatalf("LanternServiceName = %q, want %q",
			graphv1connect.LanternServiceName, "graph.v1.LanternService")
	}
	if graphv1connect.LanternReplicationServiceName != "graph.v1.LanternReplicationService" {
		t.Fatalf("LanternReplicationServiceName = %q, want %q",
			graphv1connect.LanternReplicationServiceName, "graph.v1.LanternReplicationService")
	}

	// Handler-side: the Unimplemented* handlers must satisfy the handler
	// interface so #337 can wrap them with a Connect mux during the
	// transition window.
	var lanternHandler graphv1connect.LanternServiceHandler = graphv1connect.UnimplementedLanternServiceHandler{}
	var replicationHandler graphv1connect.LanternReplicationServiceHandler = graphv1connect.UnimplementedLanternReplicationServiceHandler{}
	if _, h := graphv1connect.NewLanternServiceHandler(lanternHandler); h == nil {
		t.Fatal("NewLanternServiceHandler returned nil http.Handler")
	}
	if _, h := graphv1connect.NewLanternReplicationServiceHandler(replicationHandler); h == nil {
		t.Fatal("NewLanternReplicationServiceHandler returned nil http.Handler")
	}

	// Client-side: the constructors must accept a stdlib http.Client and a
	// base URL, matching the surface #338 (Go SDK) builds against.
	httpClient := &http.Client{}
	if c := graphv1connect.NewLanternServiceClient(httpClient, "http://example.invalid"); c == nil {
		t.Fatal("NewLanternServiceClient returned nil")
	}
	if c := graphv1connect.NewLanternReplicationServiceClient(httpClient, "http://example.invalid"); c == nil {
		t.Fatal("NewLanternReplicationServiceClient returned nil")
	}

	// connectrpc.com/connect must be available at the version pinned in
	// pb/go.mod. The IsAtLeastVersion1_13_0 constant from the generated
	// file already enforces a lower bound; this just keeps an explicit
	// reference so the dependency is exercised by `go test`.
	_ = connect.CodeUnknown
}
