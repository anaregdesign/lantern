package security

// AllowsEdgeAction composes an Edge operation or independent capability from
// the endpoint prefix grants. Callers also enforce the operation's base access.
func (a *Access) AllowsEdgeAction(action Action, tail, head string) bool {
	switch action {
	case EdgeRead, EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
		return a.AllowsEdge(action, tail, head)
	case CDCIdentity, CDCValue, Export, ReceiptRead:
		return a.Allows(action, tail) && a.Allows(action, head)
	default:
		return false
	}
}

// EdgeCandidateScope is an endpoint superset for range seeks. A modification
// uses the union of tail read and head write ranges; its final oriented endpoint
// predicate is mandatory. This cache contains policy ranges, not ranking data.
func (a *Access) EdgeCandidateScope(actions ...Action) *Scope {
	if a == nil || len(actions) == 0 {
		return &Scope{}
	}
	var result *Scope
	for _, action := range actions {
		var compiled *Scope
		switch action {
		case EdgeRead:
			compiled = a.Scope(VertexRead)
		case EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete:
			compiled = a.cache.get("head-modification:"+a.setKey, func() *Scope {
				// The builder owns the cache lock; compile without recursively
				// entering that lock through Scope.
				read, write := a.compileScope(VertexRead), a.compileScope(VertexWrite)
				if read.Empty() || write.Empty() {
					return &Scope{}
				}
				return unionScopes(read, write)
			})
		default:
			compiled = a.Scope(action)
		}
		if result == nil {
			result = compiled
		} else {
			result = intersectScopes(result, compiled)
		}
		if result.Empty() {
			return result
		}
	}
	return result
}
