package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

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
	ErrScan             = errors.New("scan error")
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
	s, err := parser.NewSource(str)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return ErrInvalidVerb
	}
	return c.runSource(ctx, s)
}

// RunArgs dispatches an already-split token stream (verb + arguments)
// through the same grammar the REPL parses from a raw line. The one-shot
// verb-first CLI commands (`lantern get vertex <key>`, …) call this with
// cobra's argv so the one-liner surface and the REPL share exactly one
// grammar and one dispatcher (#672).
func (c *CLIService) RunArgs(ctx context.Context, args []string) error {
	return c.runSource(ctx, parser.NewSourceFromTokens(args))
}

// runSource is the shared verb dispatcher behind both Run (a raw line from
// the REPL) and RunArgs (pre-split argv from the one-shot verbs).
func (c *CLIService) runSource(ctx context.Context, s *parser.Source) error {
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
			fmt.Println(formatWriteEcho(fmt.Sprintf("add edge %q -> %q (weight %g)", p.Tail, p.Head, p.Weight), p.TTL, time.Now()))
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
			// Echo the applied TTL/expiry so a decaying write is never
			// silent (#653) — the REPL's "OK (<elapsed>)" status alone
			// hid that e.g. `put vertex a a 1` expires in one second.
			fmt.Println(formatWriteEcho(fmt.Sprintf("put vertex %q", p.Key), p.TTL, time.Now()))
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
			fmt.Println(formatWriteEcho(fmt.Sprintf("put edge %q -> %q (weight %g)", p.Tail, p.Head, p.Weight), p.TTL, time.Now()))
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

	case "scan":
		obj, err := parser.ScanObjective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		switch obj {
		case "vertices":
			p, err := parser.ScanVerticesParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrScan
			}
			opts := []client.ScanOption{}
			if p.Limit > 0 && p.Limit <= math.MaxUint32 {
				opts = append(opts, client.WithScanLimit(uint32(p.Limit)))
			}
			vs, _, err := c.client.ScanVertices(ctx, p.Prefix, opts...)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			if jsonString, err := json.MarshalIndent(vs, "", "\t"); err == nil {
				fmt.Println(string(jsonString))
			}
			return nil
		case "edges":
			p, err := parser.ScanEdgesParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrScan
			}
			opts := []client.EdgeScanOption{client.WithEdgeScanTailPrefix(p.TailPrefix)}
			if p.Limit > 0 && p.Limit <= math.MaxUint32 {
				opts = append(opts, client.WithEdgeScanLimit(uint32(p.Limit)))
			}
			es, _, err := c.client.ScanEdges(ctx, opts...)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			if jsonString, err := json.MarshalIndent(es, "", "\t"); err == nil {
				fmt.Println(string(jsonString))
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
		opts := []client.IlluminateOption{
			client.WithStep(uint32(p.Step)),
			client.WithK(uint32(p.K)),
			client.WithAlgorithm(algo),
			client.WithObjective(obj),
			client.WithWeighting(w),
		}
		if p.Prefix != "" {
			opts = append(opts, client.WithVertexPrefix(p.Prefix))
		}
		g, err := c.client.Illuminate(ctx, p.Seed, opts...)
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
	case "help":
		// Help prints the per-verb grammar reference (#436). Single
		// source of truth lives in `parser.HelpText`; the TS port keeps
		// a byte-equivalent copy at `admin/app/lib/cli/verbs.ts`
		// `HELP_TEXT`. Extra arguments are accepted silently to mirror
		// `exit`.
		fmt.Println(parser.HelpText)
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

// formatWriteEcho builds the one-line success summary the REPL prints
// after a mutating write so the applied TTL/expiry is never silent
// (#653). The "OK (<elapsed>)" status line alone hid the TTL, so
// `put vertex a a 1` looked permanent yet silently decayed in one
// second. A zero/negative TTL is the permanent (no-decay) sentinel
// (#523); a positive TTL echoes the absolute expiration the server
// decays against (now + ttl), rendered in RFC3339.
func formatWriteEcho(subject string, ttl time.Duration, now time.Time) string {
	if ttl <= 0 {
		return fmt.Sprintf("%s (no ttl)", subject)
	}
	return fmt.Sprintf("%s (ttl %s, expires %s)", subject, ttl, now.Add(ttl).Format(time.RFC3339))
}
