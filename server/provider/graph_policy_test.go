package provider

import (
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func TestNewGraphPolicyOwnerRequiresRestoredCertifiedIdentity(t *testing.T) {
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly})
	config := CacheConfig{CacheSettings: CacheSettings{
		GCInterval: time.Minute, GCEdgeBudget: 7, MaxVertices: 8, MaxEdges: 9,
		MaxVertexCausalEntries: 10, MaxEdgeCausalEntries: 11,
	}}
	if owner, err := NewGraphPolicyOwner(config, runtime, primary, runtimeCertified{}); err == nil || owner != nil {
		t.Fatalf("uncertified owner = %v, %v", owner, err)
	}
	foreign := runtime.NewExecutor(nil)
	if owner, err := NewGraphPolicyOwner(config, runtime, foreign, certified); err == nil || owner != nil {
		t.Fatalf("foreign primary owner = %v, %v", owner, err)
	}
	owner, err := NewGraphPolicyOwner(config, runtime, primary, certified)
	if err != nil {
		t.Fatal(err)
	}
	want := dataruntime.GraphPolicyConfig{
		GCInterval: time.Minute, GCEdgeBudget: 7, MaxVertices: 8, MaxEdges: 9,
		MaxVertexCausalEntries: 10, MaxEdgeCausalEntries: 11,
	}
	if got := owner.Snapshot(); got.Generation != 0 || got.Config != want {
		t.Fatalf("bootstrap policy = %+v", got)
	}
	if !owner.OwnsInstallation(runtime, primary) || owner.OwnsInstallation(runtime, foreign) {
		t.Fatal("graph policy owner did not retain its exact certified installation")
	}
}

func TestNewGraphPolicyOwnerPreservesLegacyNonpositiveAggregateCaps(t *testing.T) {
	for _, test := range []struct {
		name                    string
		vertices, edges         int
		wantVertices, wantEdges int
	}{
		{"negative vertices", -1, 9, 0, 9},
		{"negative edges", 8, -2, 8, 0},
		{"both negative", -1, -2, 0, 0},
		{"explicit unlimited", 0, 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly})
			config := CacheConfig{CacheSettings: CacheSettings{
				GCInterval: time.Minute, GCEdgeBudget: 7, MaxVertices: test.vertices, MaxEdges: test.edges,
				MaxVertexCausalEntries: 10, MaxEdgeCausalEntries: 11,
			}}
			before := config
			owner, err := NewGraphPolicyOwner(config, runtime, primary, certified)
			if err != nil {
				t.Fatal(err)
			}
			want := dataruntime.GraphPolicyConfig{
				GCInterval: time.Minute, GCEdgeBudget: 7, MaxVertices: test.wantVertices, MaxEdges: test.wantEdges,
				MaxVertexCausalEntries: 10, MaxEdgeCausalEntries: 11,
			}
			if actual := owner.Snapshot(); actual.Generation != 0 || actual.Config != want || config != before {
				t.Fatalf("legacy aggregate bootstrap = %+v, input %+v", actual, config)
			}
			if _, err := primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "a"}, {Key: "b"}}}); err != nil {
				t.Fatalf("legacy vertex admission changed: %v", err)
			}
			if _, err := primary.PutEdges(t.Context(), &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1}, {Tail: "b", Head: "a", Weight: 1}}}); err != nil {
				t.Fatalf("legacy edge admission changed: %v", err)
			}
		})
	}
}
