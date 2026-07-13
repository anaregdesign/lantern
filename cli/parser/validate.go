package parser

import (
	"errors"
	"fmt"
)

var ErrParse = errors.New("parse error")

func Validate(input string) error {
	s, err := NewSource(input)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}
	v, err := Verb(s)
	if err != nil {
		return errors.New("usage: { get | put | delete | add | scan | count | delete-prefix | keys | search | bfs | pagerank | community | help | exit } ... ")
	}

	switch v {
	case "get":
		o, err := Objective(s)
		if err != nil {
			return errors.New("usage: get { vertex | edge } ... ")
		}
		switch o {
		case "vertex":
			if _, err := GetVertexParam(s); err != nil {
				return errors.New("usage: get vertex <key: string>")
			}
		case "edge":
			if _, err := GetEdgeParam(s); err != nil {
				return errors.New("usage: get edge <tail: string> <head: string>")
			}
		default:
			return errors.New("usage: get { vertex | edge } ... ")
		}
	case "put":
		o, err := Objective(s)
		if err != nil {
			return errors.New("usage: put { vertex | edge } ... ")
		}
		switch o {
		case "vertex":
			if _, err := PutVertexParam(s); err != nil {
				return errors.New("usage: put vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>]")
			}
		case "edge":
			if _, err := PutEdgeParam(s); err != nil {
				return errors.New("usage: put edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]]")
			}
		default:
			return errors.New("usage: put { vertex | edge } ... ")
		}
	case "delete":
		o, err := Objective(s)
		if err != nil {
			return errors.New("usage: delete { vertex | edge }")
		}
		switch o {
		case "vertex":
			if _, err := DeleteVertexParam(s); err != nil {
				return errors.New("usage: delete vertex <key: string>")
			}
		case "edge":
			if _, err := DeleteEdgeParam(s); err != nil {
				return errors.New("usage: delete edge <tail: string> <head: string>")
			}
		default:
			return errors.New("usage: delete { vertex | edge }")
		}
	case "add":
		o, err := AddObjective(s)
		if err != nil {
			return errors.New("usage: add { edge | decaying-edge } ... ")
		}
		switch o {
		case "edge":
			if _, err := AddEdgeParam(s); err != nil {
				return errors.New("usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]")
			}
		case "decaying-edge":
			if _, err := AddDecayingEdgeParam(s); err != nil {
				return errors.New("usage: add decaying-edge <tail: string> <head: string> <initial_weight: float> <ratio: float> <steps: int> <interval_seconds: int>")
			}
		default:
			return errors.New("usage: add { edge | decaying-edge } ... ")
		}
	case "scan":
		o, err := ScanObjective(s)
		if err != nil {
			return errors.New("usage: scan { vertices | edges } ... ")
		}
		switch o {
		case "vertices":
			if _, err := ScanVerticesParam(s); err != nil {
				return errors.New("usage: scan vertices <prefix: string> [<limit: int>]")
			}
		case "edges":
			if _, err := ScanEdgesParam(s); err != nil {
				return errors.New("usage: scan edges <tail-prefix: string> [<limit: int>]")
			}
		default:
			return errors.New("usage: scan { vertices | edges } ... ")
		}
	case "count":
		o, err := ScanObjective(s)
		if err != nil {
			return errors.New("usage: count vertices <prefix: string>")
		}
		switch o {
		case "vertices":
			if _, err := CountVerticesParam(s); err != nil {
				return errors.New("usage: count vertices <prefix: string>")
			}
		default:
			return errors.New("usage: count vertices <prefix: string>")
		}
	case "delete-prefix":
		o, err := ScanObjective(s)
		if err != nil {
			return errors.New("usage: delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]")
		}
		switch o {
		case "vertices":
			if _, err := DeletePrefixVerticesParam(s); err != nil {
				return errors.New("usage: delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]")
			}
		default:
			return errors.New("usage: delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]")
		}
	case "keys":
		if _, err := KeysParam(s); err != nil {
			return errors.New("usage: keys <prefix: string> [<limit: int>]")
		}
	case "search":
		if _, err := SearchParam(s); err != nil {
			return fmt.Errorf("usage: search <query: string> [limit=<uint32>] [prefix=<string>] [mode=server|any|all|min-should] [min_should=<uint32>] [phrase=<bool>] [fuzziness=0|1|2] [prefix_terms=<bool>] [cursor=<base64url>] [all=<bool>] [projection=key-score|full-vertex] [format=json|ndjson|tsv]: %w", err)
		}
	case "bfs":
		if _, err := BfsParam(s); err != nil {
			return errors.New("usage: bfs <seed: string> [step: int] [fan_out: int] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]")
		}
	case "pagerank":
		if _, err := PagerankParam(s); err != nil {
			return errors.New("usage: pagerank <seed: string> [top_n: int] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]")
		}
	case "community":
		if _, err := CommunityParam(s); err != nil {
			return errors.New("usage: community <seed: string> [max_size: int] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]")
		}

	case "help":
		if _, err := HelpParam(s); err != nil {
			return err
		}

	case "exit":

	default:
		return errors.New("usage: { get | put | delete | add | scan | count | delete-prefix | keys | search | bfs | pagerank | community | help | exit } ... ")
	}
	return nil
}
