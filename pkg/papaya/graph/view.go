package graph

type VertexView struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`
}

type EdgeView struct {
	From  int     `json:"from"`
	To    int     `json:"to"`
	Value float32 `json:"value,omitempty"`
}

type GraphView struct {
	Vertices []VertexView `json:"nodes"`
	Edges    []EdgeView   `json:"edges"`
}
