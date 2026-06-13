package parser

import "time"

type GetVertex struct {
	Key string
}

type GetEdge struct {
	Tail string
	Head string
}

type PutVertex struct {
	Key   string
	Value any
	TTL   time.Duration
}

type PutEdge struct {
	Tail   string
	Head   string
	Weight float32
	TTL    time.Duration
}

type AddEdge struct {
	Tail   string
	Head   string
	Weight float32
	TTL    time.Duration
}

type DeleteVertex struct {
	Key string
}

type DeleteEdge struct {
	Tail string
	Head string
}

type Illuminate struct {
	Seed      string
	Step      int
	K         int
	Algorithm string // "none" | "mst" | "spt" (default: "none")
	Objective string // "min" | "max"           (default: "max")
	Weighting string // "raw" | "tfidf"         (default: "raw")
	Prefix    string // vertex-key prefix filter; "" (default) = no filter (#604)
}

// ScanVertices / ScanEdges back the `scan vertices` / `scan edges` REPL
// verbs that mirror the admin web CLI shape (#411). Limit is optional;
// 0 means "use the server default".
type ScanVertices struct {
	Prefix string
	Limit  int
}

type ScanEdges struct {
	TailPrefix string
	Limit      int
}
