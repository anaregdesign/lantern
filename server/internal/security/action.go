// Package security implements immutable Server security state. It is separate
// from transport authentication and never imports a client SDK.
package security

// Action is a primitive permission. There are no implicit action hierarchies.
type Action string

const (
	VertexRead     Action = "vertex.read"
	VertexWrite    Action = "vertex.write"
	VertexDelete   Action = "vertex.delete"
	EdgeRead       Action = "edge.read"
	EdgeAdd        Action = "edge.add"
	EdgeWrite      Action = "edge.write"
	EdgeDelete     Action = "edge.delete"
	Query          Action = "query"
	CDCIdentity    Action = "cdc.identity"
	CDCValue       Action = "cdc.value"
	Export         Action = "export"
	ReceiptRead    Action = "receipt.read"
	OperationsRead Action = "operations.read"
	SchemaRead     Action = "schema.read"
	SecurityManage Action = "security.manage"
)

// ResourceKind distinguishes literal data prefixes from global capabilities.
type ResourceKind string

const (
	DataResource   ResourceKind = "data"
	GlobalResource ResourceKind = "global"
)

func actionResource(action Action) (ResourceKind, bool) {
	switch action {
	case VertexRead, VertexWrite, VertexDelete, EdgeRead, EdgeAdd, EdgeWrite,
		EdgeDelete, Query, CDCIdentity, CDCValue, Export, ReceiptRead:
		return DataResource, true
	case OperationsRead, SchemaRead, SecurityManage:
		return GlobalResource, true
	default:
		// cluster.replicate belongs to operator peer trust, never user Roles.
		return "", false
	}
}
