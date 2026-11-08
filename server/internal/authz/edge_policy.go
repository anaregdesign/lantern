package authz

// EdgeEndpointMode is one immutable cluster-wide policy, never a Role grant.
type EdgeEndpointMode string

const (
	EdgeReadRead   EdgeEndpointMode = "rr"
	EdgeWriteWrite EdgeEndpointMode = "ww"
	EdgeReadWrite  EdgeEndpointMode = "rw"
	EdgeWriteRead  EdgeEndpointMode = "wr"
)

func (m EdgeEndpointMode) Valid() bool {
	return m == EdgeReadRead || m == EdgeWriteWrite || m == EdgeReadWrite || m == EdgeWriteRead
}
func (a *Access) EdgeEndpointActions() (Action, Action) {
	if a == nil {
		return VertexRead, VertexRead
	}
	switch a.edgeMode {
	case EdgeWriteWrite:
		return VertexWrite, VertexWrite
	case EdgeReadWrite:
		return VertexRead, VertexWrite
	case EdgeWriteRead:
		return VertexWrite, VertexRead
	default:
		return VertexRead, VertexRead
	}
}
