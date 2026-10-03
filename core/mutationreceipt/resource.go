package mutationreceipt

import (
	"strings"
	"unicode/utf8"
)

const MaxResourceIdentityBytes = 8 << 20

// ResourceIdentity is opaque original mutation provenance supplied by the
// committing application. Key is a Vertex key or Edge tail; Head is present
// only for an Edge. Core does not interpret namespaces or authorization.
// The zero value denotes an unproven legacy row, never a wildcard resource.
// Value fields keep Intent comparable for duplicate/dominance checks.
type ResourceIdentity struct {
	Key  string
	Head string
}

func (r ResourceIdentity) valid(kind Kind) bool {
	if r == (ResourceIdentity{}) {
		return true
	}
	if r.Key == "" || !utf8.ValidString(r.Key) || !utf8.ValidString(r.Head) || len(r.Key) > MaxResourceIdentityBytes || len(r.Head) > MaxResourceIdentityBytes-len(r.Key) {
		return false
	}
	switch kind {
	case PutVertex, DeleteVertex:
		return r.Head == ""
	case PutEdge, AddEdge, DeleteEdge, DeleteEdgeContribution, CreateEdge:
		return r.Head != ""
	default:
		return false
	}
}

func (r ResourceIdentity) cost() uint64 {
	if r == (ResourceIdentity{}) {
		return 0
	}
	return 8 + uint64(len(r.Key)) + uint64(len(r.Head))
}

func cloneIntent(intent Intent) Intent {
	intent.Resource.Key = strings.Clone(intent.Resource.Key)
	intent.Resource.Head = strings.Clone(intent.Resource.Head)
	return intent
}
