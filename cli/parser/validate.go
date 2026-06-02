package parser

import (
	"errors"
	"strings"
)

var ErrParse = errors.New("parse error")

func Validate(input string) error {
	s := NewSource(strings.ToLower(input))
	v, err := Verb(s)
	if err != nil {
		return errors.New("usage: { get | put | delete | add | illuminate | exit } ... ")
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
	case "illuminate":
		o, err := IlluminateObjective(s)
		if err != nil {
			return errors.New("usage: illuminate { neighbor | spt_relevance | spt_cost | mst_relevance | mst_cost } ... ")
		}
		if _, err := IlluminateParam(s); err != nil {
			return errors.New("usage: illuminate " + o + "<key: string> <step: int> <k: int> <tfidf: bool>")
		}

	case "exit":

	default:
		return errors.New("usage: { get | put | delete | add | illuminate | exit } ... ")
	}
	return nil
}
