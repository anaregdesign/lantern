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
	ErrAddDecayingEdge  = errors.New("add decaying-edge error")
	ErrScan             = errors.New("scan error")
	ErrCount            = errors.New("count error")
	ErrDeletePrefix     = errors.New("delete-prefix error")
	ErrKeys             = errors.New("keys error")
	ErrBFS              = errors.New("bfs error")
	ErrPagerank         = errors.New("pagerank error")
	ErrCommunity        = errors.New("community error")
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
// verb-first CLI commands (`lantern-cli get vertex <key>`, …) call this with
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
		obj, err := parser.AddObjective(s)
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
			effective, err := c.client.AddEdge(ctx, p.Tail, p.Head, p.Weight, p.TTL)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			fmt.Println(formatWriteEcho(fmt.Sprintf("add edge %q -> %q (weight %g, total %g)", p.Tail, p.Head, p.Weight, effective), p.TTL, time.Now()))
			return nil
		case "decaying-edge":
			p, err := parser.AddDecayingEdgeParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrAddDecayingEdge
			}
			opts := client.DecayOpts{
				InitialWeight: p.InitialWeight,
				Ratio:         p.Ratio,
				Steps:         p.Steps,
				Interval:      p.Interval,
			}
			effective, err := c.client.AddDecayingEdge(ctx, p.Tail, p.Head, opts)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				// A rejected DecayOpts (ratio/steps/interval out of range,
				// underflowing curve) is a client-side usage error, not a
				// transport failure; only genuine wire faults are ErrConnection.
				if errors.Is(err, client.ErrInvalidArgument) {
					return ErrAddDecayingEdge
				}
				return ErrConnection
			}
			// Echo the full decay horizon (Steps×Interval) as the effective
			// TTL so the operator sees when the edge fully decays to zero.
			horizon := time.Duration(p.Steps) * p.Interval
			fmt.Println(formatWriteEcho(
				fmt.Sprintf("add decaying-edge %q -> %q (initial %g, ratio %g, steps %d, total %g)",
					p.Tail, p.Head, p.InitialWeight, p.Ratio, p.Steps, effective),
				horizon, time.Now()))
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
			if len(p.Keys) == 1 {
				if _, err := c.client.DeleteVertex(ctx, p.Keys[0]); err != nil {
					fmt.Printf("Error: %s\n", err)
					return ErrConnection
				}
				return nil
			}
			n, err := c.client.DeleteVertices(ctx, p.Keys)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			fmt.Printf("OK %d\n", n)
			return nil

		case "edge":
			p, err := parser.DeleteEdgeParam(s)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrDeleteEdge
			}
			if len(p.Pairs) == 1 {
				if _, err := c.client.DeleteEdge(ctx, p.Pairs[0].Tail, p.Pairs[0].Head); err != nil {
					fmt.Printf("Error: %s\n", err)
					return ErrConnection
				}
				return nil
			}
			refs := make([]client.EdgeRef, len(p.Pairs))
			for i, pr := range p.Pairs {
				refs[i] = client.EdgeRef{Tail: pr.Tail, Head: pr.Head}
			}
			n, err := c.client.DeleteEdges(ctx, refs)
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return ErrConnection
			}
			fmt.Printf("OK %d\n", n)
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
			var limit uint32
			if p.Limit > 0 && p.Limit <= math.MaxUint32 {
				limit = uint32(p.Limit)
			}
			if p.All {
				var all []*client.Vertex
				for batch, err := range c.client.ScanVerticesAll(ctx, p.Prefix, limit) {
					if err != nil {
						fmt.Printf("Error: %s\n", err)
						return ErrConnection
					}
					all = append(all, batch...)
				}
				if jsonString, err := json.MarshalIndent(all, "", "\t"); err == nil {
					fmt.Println(string(jsonString))
				}
				return nil
			}
			opts := []client.ScanOption{}
			if limit > 0 {
				opts = append(opts, client.WithScanLimit(limit))
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
			var limit uint32
			if p.Limit > 0 && p.Limit <= math.MaxUint32 {
				limit = uint32(p.Limit)
			}
			baseOpts := []client.EdgeScanOption{client.WithEdgeScanTailPrefix(p.TailPrefix)}
			if p.HeadPrefix != "" {
				baseOpts = append(baseOpts, client.WithEdgeScanHeadPrefix(p.HeadPrefix))
			}
			if p.All {
				var all []*client.Edge
				for batch, err := range c.client.ScanEdgesAll(ctx, limit, baseOpts...) {
					if err != nil {
						fmt.Printf("Error: %s\n", err)
						return ErrConnection
					}
					all = append(all, batch...)
				}
				if jsonString, err := json.MarshalIndent(all, "", "\t"); err == nil {
					fmt.Println(string(jsonString))
				}
				return nil
			}
			opts := append([]client.EdgeScanOption{}, baseOpts...)
			if limit > 0 {
				opts = append(opts, client.WithEdgeScanLimit(limit))
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

	case "count":
		obj, err := parser.ScanObjective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		if obj != "vertices" {
			return ErrInvalidObjective
		}
		p, err := parser.CountVerticesParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrCount
		}
		n, err := c.client.CountVerticesByPrefix(ctx, p.Prefix)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrConnection
		}
		fmt.Printf("%d\n", n)
		return nil

	case "delete-prefix":
		obj, err := parser.ScanObjective(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrInvalidObjective
		}
		if obj != "vertices" {
			return ErrInvalidObjective
		}
		p, err := parser.DeletePrefixVerticesParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrDeletePrefix
		}
		opts := []client.DeleteByPrefixOption{}
		if p.Limit > 0 && p.Limit <= math.MaxUint32 {
			opts = append(opts, client.WithDeleteByPrefixLimit(uint32(p.Limit)))
		}
		if p.DryRun {
			opts = append(opts, client.WithDryRun())
		}
		n, err := c.client.DeleteVerticesByPrefix(ctx, p.Prefix, opts...)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrConnection
		}
		word := "deleted"
		if p.DryRun {
			word = "would delete"
		}
		fmt.Printf("%s %d\n", word, n)
		return nil

	case "keys":
		p, err := parser.KeysParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrKeys
		}
		// Redis-style KEYS: list vertex keys under a prefix (keys-only). Mirrors
		// `scan vertices` — a single page with an optional limit — but prints
		// just the keys, one per line, so it pipes cleanly into xargs/jq. The
		// prefix is required (the server rejects an empty prefix).
		opts := []client.ScanOption{}
		if p.Limit > 0 && p.Limit <= math.MaxUint32 {
			opts = append(opts, client.WithScanLimit(uint32(p.Limit)))
		}
		ks, _, err := c.client.ScanVertexKeys(ctx, p.Prefix, opts...)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrConnection
		}
		for _, k := range ks {
			fmt.Println(k)
		}
		return nil

	case "bfs":
		p, err := parser.BfsParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrBFS
		}
		obj, ok := objectiveByREPLName[p.Objective]
		if !ok {
			fmt.Printf("Error: bfs: unknown objective %q\n", p.Objective)
			return ErrBFS
		}
		red, ok := reductionByREPLName[p.Reduction]
		if !ok {
			fmt.Printf("Error: bfs: unknown reduction %q\n", p.Reduction)
			return ErrBFS
		}
		w, ok := weightingByREPLName[p.Weighting]
		if !ok {
			fmt.Printf("Error: bfs: unknown weighting %q\n", p.Weighting)
			return ErrBFS
		}
		opts := []client.IlluminateOption{
			BfsOption(p.Step, p.FanOut, red, obj),
			client.WithWeighting(w),
		}
		if p.Prefix != "" {
			opts = append(opts, client.WithVertexPrefix(p.Prefix))
		}
		return c.runIlluminate(ctx, p.Seed, opts)
	case "pagerank":
		p, err := parser.PagerankParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrPagerank
		}
		w, ok := weightingByREPLName[p.Weighting]
		if !ok {
			fmt.Printf("Error: pagerank: unknown weighting %q\n", p.Weighting)
			return ErrPagerank
		}
		opts := []client.IlluminateOption{
			PagerankOption(p.TopN, p.RestartProb, p.Epsilon),
			client.WithWeighting(w),
		}
		if p.Prefix != "" {
			opts = append(opts, client.WithVertexPrefix(p.Prefix))
		}
		return c.runIlluminate(ctx, p.Seed, opts)
	case "community":
		p, err := parser.CommunityParam(s)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return ErrCommunity
		}
		obj, ok := objectiveByREPLName[p.Objective]
		if !ok {
			fmt.Printf("Error: community: unknown objective %q\n", p.Objective)
			return ErrCommunity
		}
		red, ok := reductionByREPLName[p.Reduction]
		if !ok {
			fmt.Printf("Error: community: unknown reduction %q\n", p.Reduction)
			return ErrCommunity
		}
		w, ok := weightingByREPLName[p.Weighting]
		if !ok {
			fmt.Printf("Error: community: unknown weighting %q\n", p.Weighting)
			return ErrCommunity
		}
		opts := []client.IlluminateOption{
			CommunityOption(p.MaxSize, red, obj, p.RestartProb, p.Epsilon),
			client.WithWeighting(w),
		}
		if p.Prefix != "" {
			opts = append(opts, client.WithVertexPrefix(p.Prefix))
		}
		return c.runIlluminate(ctx, p.Seed, opts)
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

// BfsOption / PagerankOption / CommunityOption build the typed per-family SDK
// option from CLI-resolved knobs (#846 oneof, #975 verb split). Each family
// verb owns exactly the knobs its wire arm understands: bfs carries step/fan_out
// plus the tree reduction, pagerank carries top_n plus the α/ε push knobs (no
// reduction — its relevance star is already a tree), and community carries
// max_size plus α/ε and an optional tree reduction (#845). Shared by the REPL
// dispatcher and the cobra family subcommands so both surfaces translate
// identically.
func BfsOption(step, fanOut uint32, red client.Reduction, obj client.Objective) client.IlluminateOption {
	return client.WithBFS(client.BFSOpts{Step: step, FanOut: fanOut, Objective: obj, Reduction: red})
}

func PagerankOption(topN uint32, restartProb, epsilon float32) client.IlluminateOption {
	return client.WithPPR(client.PPROpts{TopN: topN, RestartProb: restartProb, Epsilon: epsilon})
}

func CommunityOption(maxSize uint32, red client.Reduction, obj client.Objective, restartProb, epsilon float32) client.IlluminateOption {
	return client.WithLocalCommunity(client.LocalCommunityOpts{MaxSize: maxSize, RestartProb: restartProb, Epsilon: epsilon, Reduction: red, Objective: obj})
}

// runIlluminate executes a family walk and renders the resulting subgraph as
// indented JSON. The server owns the traversal and any post-traversal reduction
// (#410); the REPL/one-liner just prints what comes back. Shared by the three
// family dispatch cases.
func (c *CLIService) runIlluminate(ctx context.Context, seed string, opts []client.IlluminateOption) error {
	g, err := c.client.Illuminate(ctx, seed, opts...)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return ErrConnection
	}
	jsonString, err := json.MarshalIndent(g, "", "\t")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonString))
	return nil
}

// objectiveByREPLName / reductionByREPLName / weightingByREPLName mirror the
// friendly names accepted by the family verbs (parser.Bfs / parser.Community)
// and translate to the SDK enums. Kept private to the REPL handler.
var (
	objectiveByREPLName = map[string]client.Objective{
		"min": client.ObjectiveMinimize,
		"max": client.ObjectiveMaximize,
	}
	reductionByREPLName = map[string]client.Reduction{
		"none": client.ReductionNone,
		"mst":  client.ReductionMinimumSpanningTree,
		"spt":  client.ReductionShortestPathTree,
	}
	weightingByREPLName = map[string]client.Weighting{
		"raw":   client.WeightingRaw,
		"tfidf": client.WeightingTFIDF,
		"bm25":  client.WeightingBM25,
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
