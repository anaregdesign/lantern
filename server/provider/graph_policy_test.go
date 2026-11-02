package provider

import (
	"testing"
	"time"

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
}
