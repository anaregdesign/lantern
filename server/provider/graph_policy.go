package provider

import (
	"errors"

	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

// NewGraphPolicyOwner creates the operational owner only after restoration and
// exact-runtime certification. Common management composition supplies native
// adoption later; this constructor neither opens authority nor starts GC.
func NewGraphPolicyOwner(cache CacheConfig, runtime *dataruntime.ServingRuntime, primary *dataruntime.Executor, certified runtimeCertified) (*dataruntime.GraphPolicyOwner, error) {
	if !certified.valid || certified.runtime != runtime || certified.primary != primary {
		return nil, errors.New("graph policy: requires the certified restored runtime")
	}
	return dataruntime.NewGraphPolicyOwner(runtime, primary, dataruntime.GraphPolicyConfig{
		// Legacy aggregate admission treats every nonpositive cap as unlimited.
		// Canonicalize only that bootstrap representation; managed policy remains
		// strictly nonnegative.
		MaxVertices: max(cache.MaxVertices, 0), MaxEdges: max(cache.MaxEdges, 0),
		MaxVertexCausalEntries: cache.MaxVertexCausalEntries,
		MaxEdgeCausalEntries:   cache.MaxEdgeCausalEntries,
		GCEdgeBudget:           cache.GCEdgeBudget, GCInterval: cache.GCInterval,
	})
}
