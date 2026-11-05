package service

import (
	"context"
	"testing"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func TestRequestPolicyConstructionAliases(t *testing.T) {
	snapshot := RequestPolicySnapshot{Generation: 1, Policy: RequestPolicy{
		Scan:      ScanLimits{ScanDefaultLimit: 1, ScanMaxLimit: 2, DeleteByPrefixDefaultLimit: 1, DeleteByPrefixMaxLimit: 2},
		Traversal: TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 1, MaxTouchedEdges: 2}, MaxResults: 1},
	}}
	owner, err := NewRequestPolicyOwner(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := RequestPolicyFromContext(context.Background()); ok {
		t.Fatal("uncaptured context has policy")
	}
	ctx := dataruntime.CaptureRequestPolicy(context.Background(), owner)
	if got, ok := RequestPolicyFromContext(ctx); !ok || got != snapshot {
		t.Fatalf("snapshot = %+v/%v", got, ok)
	}
}
