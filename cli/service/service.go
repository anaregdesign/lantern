package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anaregdesign/lantern/cli/parser"
	client "github.com/anaregdesign/lantern/sdks/go"
)

var (
	ErrInvalidObjective = errors.New("invalid objective")
	ErrInvalidVerb      = errors.New("invalid verb")
	ErrNotImplemented   = errors.New("not implemented")
	ErrGetVertex        = errors.New("get vertex error")
	ErrGetEdge          = errors.New("get edge error")
	ErrPutVertex        = errors.New("put vertex error")
	ErrPutEdge          = errors.New("put edge error")
	ErrDeleteVertex     = errors.New("delete vertex error")
	ErrDeleteEdge       = errors.New("delete edge error")
	ErrAddEdge          = errors.New("add edge error")
	ErrIlluminate       = errors.New("illuminate error")
	ErrConnection       = errors.New("connection error")
)

type CLIService struct {
	client *client.Lantern
}

func NewCLIService(client *client.Lantern) *CLIService {
	return &CLIService{
		client: client,
	}
}

func (c *CLIService) Run(ctx context.Context, str string) error {
	s := parser.NewSource(str)
	verb, err := parser.Verb(s)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return ErrInvalidVerb
	}
	switch strings.ToLower(verb) {
	case "get":
		obj, err := parser.Objective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		switch strings.ToLower(obj) {
		case "vertex":
			p, err := parser.GetVertexParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrGetVertex
			}
			v, err := c.client.GetVertex(ctx, p.Key)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			if jsonString, err := json.Marshal(v.Value); err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrGetVertex
			} else {
				fmt.Println(string(jsonString))
				return nil
			}
		case "edge":
			p, err := parser.GetEdgeParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrGetEdge
			}
			edge, err := c.client.GetEdge(ctx, p.Tail, p.Head)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			fmt.Printf("%f\n", edge.Weight)
			return nil

		default:
			return ErrInvalidObjective
		}
	case "add":
		obj, err := parser.Objective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		switch obj {
		case "edge":
			p, err := parser.AddEdgeParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrAddEdge
			}
			if err := c.client.AddEdge(ctx, p.Tail, p.Head, p.Weight, p.TTL); err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			return nil
		default:
			return ErrInvalidObjective
		}
	case "put":
		obj, err := parser.Objective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		switch obj {
		case "vertex":
			p, err := parser.PutVertexParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrPutVertex
			}
			if err := c.client.PutVertex(ctx, p.Key, p.Value, p.TTL); err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			return nil
		case "edge":
			p, err := parser.PutEdgeParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrPutEdge
			}
			if err := c.client.PutEdge(ctx, p.Tail, p.Head, p.Weight, p.TTL); err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			return nil
		}

	case "delete":
		obj, err := parser.Objective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		switch obj {
		case "vertex":
			p, err := parser.DeleteVertexParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrDeleteVertex
			}
			if _, err := c.client.DeleteVertex(ctx, p.Key); err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			return nil

		case "edge":
			p, err := parser.DeleteEdgeParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrDeleteEdge
			}
			if _, err := c.client.DeleteEdge(ctx, p.Tail, p.Head); err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			return nil
		default:
			return ErrInvalidObjective
		}

	case "illuminate":
		p, err := parser.IlluminateParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrIlluminate
		}
		algo, ok := algorithmByREPLName[p.Algorithm]
		if !ok {
			fmt.Printf("Error: illuminate: unknown algorithm %q\n", p.Algorithm)
			return ErrIlluminate
		}
		obj, ok := objectiveByREPLName[p.Objective]
		if !ok {
			fmt.Printf("Error: illuminate: unknown objective %q\n", p.Objective)
			return ErrIlluminate
		}
		w, ok := weightingByREPLName[p.Weighting]
		if !ok {
			fmt.Printf("Error: illuminate: unknown weighting %q\n", p.Weighting)
			return ErrIlluminate
		}
		g, err := c.client.Illuminate(ctx, p.Seed,
			client.WithStep(uint32(p.Step)),
			client.WithK(uint32(p.K)),
			client.WithAlgorithm(algo),
			client.WithObjective(obj),
			client.WithWeighting(w),
		)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrConnection
		}
		// Server now owns the post-traversal reduction (#410); the REPL
		// just renders the resulting subgraph.
		jsonString, err := json.MarshalIndent(g, "", "\t")
		if err != nil {
			return err
		}
		fmt.Println(string(jsonString))
		return nil
	default:
		return ErrInvalidVerb
	}
	return nil
}

// algorithmByREPLName / objectiveByREPLName / weightingByREPLName mirror
// the friendly names accepted by the REPL grammar (parser.Illuminate{...})
// and translate to the SDK enums. Kept private to the REPL handler.
var (
	algorithmByREPLName = map[string]client.Algorithm{
		"none": client.AlgorithmUnspecified,
		"mst":  client.AlgorithmMinimumSpanningTree,
		"spt":  client.AlgorithmShortestPathTree,
	}
	objectiveByREPLName = map[string]client.Objective{
		"min": client.ObjectiveMinimize,
		"max": client.ObjectiveMaximize,
	}
	weightingByREPLName = map[string]client.Weighting{
		"raw":   client.WeightingRaw,
		"tfidf": client.WeightingTFIDF,
	}
)
