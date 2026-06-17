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
		return errors.New("usage: { get | put | delete | add | scan | keys | illuminate | help | exit } ... ")
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
		o, err := Objective(s)
		if err != nil {
			return errors.New("usage: add edge ... ")
		}
		switch o {
		case "edge":
			if _, err := AddEdgeParam(s); err != nil {
				return errors.New("usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]")
			}
		default:
			return errors.New("usage: add edge ... ")
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
	case "keys":
		if _, err := KeysParam(s); err != nil {
			return errors.New("usage: keys <prefix: string> [<limit: int>]")
		}
	case "illuminate":
		if _, err := IlluminateParam(s); err != nil {
			return errors.New("usage: illuminate <key: string> <step: int> <k: int> [algorithm=none|mst|spt] [objective=min|max] [weighting=raw|tfidf] [prefix=<string>]")
		}

	case "help":
		// Extra arguments accepted silently — mirrors `exit`. The TS
		// parser side does the same. Discoverability beats strictness:
		// the operator typing `help` is asking for the grammar, not for
		// a usage hint about `help` itself.

	case "exit":

	default:
		return errors.New("usage: { get | put | delete | add | scan | keys | illuminate | help | exit } ... ")
	}
	return nil
}
