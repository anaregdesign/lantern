package service

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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

func TestSearchPolicyConstructionAliases(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute))
	baseline := svc.SearchPolicyBaseline()
	baseline.DefaultMinShould = 1
	initial, err := ResolveSearchPolicy(baseline, 1, SearchRequestPolicyFromLimits(baseline))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewSearchPolicyOwner(baseline, initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if svc.WithSearchPolicyOwner(owner).SearchConfigFingerprint() != initial.Version.Fingerprint {
		t.Fatal("service composition lost effective owner")
	}
}
