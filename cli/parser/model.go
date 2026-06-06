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
	Objective string // "min" | "max"           (default: "min")
	Weighting string // "raw" | "tfidf"         (default: "raw")
}
