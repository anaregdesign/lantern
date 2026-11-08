// Package authz compiles and binds immutable permission policies. It performs
// no I/O and owns neither authentication nor durable security state.
package authz

// Action is an internal operation identity, including retained receipt identity.
// It is never a grantable Role permission; OperationPermission maps every
// protected operation to one typed resource and exactly read or write.
type Action string

const (
	VertexRead     Action = "vertex.read"
	VertexWrite    Action = "vertex.write"
	VertexDelete   Action = "vertex.delete"
	EdgeRead       Action = "edge.read"
	EdgeCreate     Action = "edge.create"
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
	SecurityRead   Action = "security.read"
	SettingsRead   Action = "settings.read"
	SettingsWrite  Action = "settings.write"
)

// ResourceKind separates logical data from each management surface.
type ResourceKind string

const (
	DataResource       ResourceKind = "data"
	SecurityResource   ResourceKind = "security"
	SettingsResource   ResourceKind = "settings"
	OperationsResource ResourceKind = "operations"
	SchemaResource     ResourceKind = "schema"
)

// ValidPermission rejects unsupported writes on read-only resource surfaces.
func ValidPermission(resource ResourceKind, permission Permission) bool {
	switch resource {
	case DataResource, SecurityResource, SettingsResource:
		return permission == Read || permission == Write
	case OperationsResource, SchemaResource:
		return permission == Read
	default:
		return false
	}
}

// OperationPermission is the exhaustive operation-to-grant map. Edge mutations
// have additional globally selected endpoint predicates, evaluated separately.
func OperationPermission(action Action) (Permission, bool) {
	switch action {
	case VertexRead, EdgeRead, Query, CDCIdentity, CDCValue, Export, ReceiptRead,
		SecurityRead, SettingsRead, OperationsRead, SchemaRead:
		return Read, true
	case VertexWrite, VertexDelete, EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete,
		SecurityManage, SettingsWrite:
		return Write, true
	default:
		return "", false
	}
}

func ActionResource(action Action) (ResourceKind, bool) {
	switch action {
	case VertexRead, VertexWrite, VertexDelete, EdgeRead, EdgeCreate, EdgeAdd, EdgeWrite,
		EdgeDelete, Query, CDCIdentity, CDCValue, Export, ReceiptRead:
		return DataResource, true
	case SecurityManage, SecurityRead:
		return SecurityResource, true
	case SettingsRead, SettingsWrite:
		return SettingsResource, true
	case OperationsRead:
		return OperationsResource, true
	case SchemaRead:
		return SchemaResource, true
	default:
		// Private peer trust is never a user Role resource.
		return "", false
	}
}

// Edge operations and independent capabilities that accept endpoint selectors.
// These selectors explain an operation; they are not configurable Role pairs.
func EdgeSelectorAction(action Action) bool {
	switch action {
	case EdgeRead, EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete, CDCIdentity, CDCValue, Export, ReceiptRead:
		return true
	default:
		return false
	}
}
