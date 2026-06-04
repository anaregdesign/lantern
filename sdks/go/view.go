package client

// VertexView is the vis.js-compatible projection of a single Vertex. See
// GraphView for the surrounding container.
type VertexView struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

// EdgeView is the vis.js-compatible projection of a single edge.
type EdgeView struct {
	From  int     `json:"from"`
	To    int     `json:"to"`
	Value float32 `json:"value,omitempty"`
}

// GraphView is the vis.js-compatible projection of a Graph: a flat list of
// vertices and edges with integer IDs. Field tags ("nodes" / "edges") match
// the shape expected by vis-network on the client side.
type GraphView struct {
	Vertices []VertexView `json:"nodes"`
	Edges    []EdgeView   `json:"edges"`
}

// Render converts an Illuminate result into the vis.js GraphView shape.
// key2int maps a vertex key to the integer ID vis.js requires; value2string
// produces the display label from the Vertex payload.
func (g *Graph) Render(key2int func(k string) int, value2string func(v *Vertex) string) GraphView {
	var vertices []VertexView
	var edges []EdgeView

	for i, v := range g.Vertices {
		vertices = append(vertices, VertexView{
			ID:    key2int(i),
			Label: value2string(v),
		})
	}

	for from, tos := range g.Edges {
		for to, value := range tos {
			edges = append(edges, EdgeView{
				From:  key2int(from),
				To:    key2int(to),
				Value: value,
			})
		}
	}

	return GraphView{
		Vertices: vertices,
		Edges:    edges,
	}
}
