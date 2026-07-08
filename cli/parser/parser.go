package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	Verbs = []string{
		"get",
		"put",
		"add",
		"delete",
		"scan",
		"count",
		"delete-prefix",
		"keys",
		"bfs",
		"pagerank",
		"community",
		"help",
		"exit",
	}

	Objectives = []string{
		"vertex",
		"edge",
	}

	// ScanObjectives are the plural-form objectives accepted by the
	// `scan` verb. They intentionally do NOT overlap with `Objectives`
	// (singular vertex/edge), which the get/put/delete/add verbs use, so
	// `get vertices` and `scan vertex` are both clean parse errors.
	ScanObjectives = []string{
		"vertices",
		"edges",
	}

	// AddObjectives are the objectives accepted by the `add` verb. Unlike
	// the shared Objectives (vertex/edge, used by get/put/delete), `add`
	// takes only edge forms: the plain additive edge and the client-expanded
	// decaying edge (#952). A dedicated set keeps `add vertex` a clean parse
	// error and stops the decay sugar leaking into get/put/delete grammar.
	AddObjectives = []string{
		"edge",
		"decaying-edge",
	}

	// TraversalReductions / TraversalObjectives / TraversalWeightings are the
	// canonical closed sets the family verbs (bfs / pagerank / community) accept
	// for their keyword arguments (#975). The traversal FAMILY is now the verb
	// itself, not an algorithm= kwarg; the reduction is a per-family knob on
	// bfs/community (never pagerank, whose relevance star is already a tree),
	// mirroring the wire oneof (#846).
	TraversalReductions = []string{"none", "mst", "spt"}
	TraversalObjectives = []string{"min", "max"}
	TraversalWeightings = []string{"raw", "tfidf", "bm25"}

	ErrNotFound = errors.New("not found")
	ErrNotEOF   = errors.New("not EOF")
)

func EOF(s *Source) error {
	if s.HasNext() {
		return ErrNotEOF
	}
	return nil
}

func String(s *Source) (string, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return "", err
	}

	return str, nil
}

func Integer(s *Source) (int, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(str)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func Float(s *Source) (float64, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func Bool(s *Source) (bool, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return false, err
	}
	v, err := strconv.ParseBool(str)
	if err != nil {
		return false, err
	}
	return v, nil
}

func Datetime(s *Source) (time.Time, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return time.Time{}, err
	}
	v, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return time.Time{}, err
	}
	return v, nil
}

func Value(s *Source) (any, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return struct{}{}, err
	}
	if v, err := strconv.Atoi(str); err == nil {
		return v, nil
	}
	if v, err := strconv.ParseFloat(str, 64); err == nil {
		return v, nil
	}
	if v, err := strconv.ParseBool(str); err == nil {
		return v, nil
	}
	if v, err := time.Parse(time.RFC3339, str); err == nil {
		return v, nil
	}
	return str, nil
}

func Duration(s *Source) (time.Duration, error) {
	// Delegate cleanly to Integer, which already advances the source via its
	// own `defer s.Next()`. An extra `defer s.Next()` here would double-advance
	// and defeat the trailing EOF check in PutEdgeParam / AddEdgeParam (#932) —
	// mirror the Float32 → Float delegation, which adds no extra advance.
	i, err := Integer(s)
	if err != nil {
		return 0, err
	}
	return time.Duration(i) * time.Second, nil
}

func Float32(s *Source) (float32, error) {
	v, err := Float(s)
	if err != nil {
		return 0, err
	}
	return float32(v), nil
}

// AnyOf returns the canonical lowercase choice that matches the next
// token case-insensitively, or ErrNotFound. The returned value is
// always one of the entries in `choices` (not the raw token), so
// downstream switches can compare against the canonical form. This
// mirrors the Go REPL's existing ToLower-on-dispatch convention and
// keeps argument tokens (vertex keys, edge endpoints, illuminate
// seeds) free of case mangling \u2014 see #437.
func AnyOf(s *Source, choices []string) (string, error) {
	defer s.Next()
	str, err := s.Peek()
	if err != nil {
		return "", err
	}
	for _, v := range choices {
		if strings.EqualFold(str, v) {
			return v, nil
		}
	}
	return "", ErrNotFound
}

func Verb(s *Source) (string, error) {
	return AnyOf(s, Verbs)
}

func Objective(s *Source) (string, error) {
	return AnyOf(s, Objectives)
}

func ScanObjective(s *Source) (string, error) {
	return AnyOf(s, ScanObjectives)
}

func AddObjective(s *Source) (string, error) {
	return AnyOf(s, AddObjectives)
}

func GetVertexParam(s *Source) (*GetVertex, error) {
	var err error
	m := &GetVertex{}
	if m.Key, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func GetEdgeParam(s *Source) (*GetEdge, error) {
	var err error
	m := &GetEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func PutVertexParam(s *Source) (*PutVertex, error) {
	key, err := String(s)
	if err != nil {
		return nil, err
	}
	raw, err := String(s)
	if err != nil {
		return nil, err
	}
	m := &PutVertex{Key: key, Type: "auto"}
	// After the positional key/value, an optional ttl_seconds (a bare
	// integer, the REPL form) and an optional type= override may follow in
	// either order — mirroring illuminate's positional-then-kwarg tail.
	// Omitted ttl_seconds ⇒ permanent (no decay) (#523).
	ttlSet := false
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		if k, v, ok := strings.Cut(tok, "="); ok {
			if strings.ToLower(k) != "type" {
				return nil, fmt.Errorf("put vertex: unknown keyword %q", k)
			}
			m.Type = strings.ToLower(v)
			continue
		}
		if ttlSet {
			return nil, fmt.Errorf("put vertex: unexpected token %q", tok)
		}
		n, perr := strconv.Atoi(tok)
		if perr != nil {
			return nil, perr
		}
		m.TTL = time.Duration(n) * time.Second
		ttlSet = true
	}
	if m.Value, err = CoerceValue(raw, m.Type); err != nil {
		return nil, err
	}
	return m, nil
}

// CoerceValue converts a raw CLI value token into a Go value for PutVertex,
// honouring the optional `type=` override (migrated from the noun-first
// `vertex put --value-type`). "" / "auto" auto-detects
// (int→float→bool→RFC3339→string); otherwise the named type is forced and a
// mismatch is an error. `json` parses the token and re-encodes objects /
// arrays as a compact JSON string (the wire has no nested value variant);
// json scalars pass through as their natural Go type.
func CoerceValue(raw, typ string) (any, error) {
	switch typ {
	case "", "auto":
		if v, err := strconv.Atoi(raw); err == nil {
			return v, nil
		}
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v, nil
		}
		if v, err := strconv.ParseBool(raw); err == nil {
			return v, nil
		}
		if v, err := time.Parse(time.RFC3339, raw); err == nil {
			return v, nil
		}
		return raw, nil
	case "string":
		return raw, nil
	case "int":
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("type=int: %w", err)
		}
		return v, nil
	case "float":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("type=float: %w", err)
		}
		return v, nil
	case "bool":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("type=bool: %w", err)
		}
		return v, nil
	case "datetime":
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("type=datetime (RFC3339): %w", err)
		}
		return v, nil
	case "duration":
		v, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("type=duration: %w", err)
		}
		return v, nil
	case "json":
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("type=json: %w", err)
		}
		// The wire format has no nested value variant; re-encode objects and
		// arrays as a compact JSON string. Scalars forward as-is.
		switch v.(type) {
		case map[string]any, []any:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("type=json re-encode: %w", err)
			}
			return string(b), nil
		default:
			return v, nil
		}
	default:
		return nil, fmt.Errorf("unknown type %q (want auto|string|int|float|bool|datetime|duration|json)", typ)
	}
}

func PutEdgeParam(s *Source) (*PutEdge, error) {
	var err error
	m := &PutEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if m.Weight, err = Float32(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		// Omitted ttl_seconds ⇒ permanent (no decay); see PutVertexParam (#523).
		m.TTL = 0
		return m, nil
	}
	if m.TTL, err = Duration(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

func AddEdgeParam(s *Source) (*AddEdge, error) {
	var err error
	m := &AddEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if m.Weight, err = Float32(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		// Omitted ttl_seconds ⇒ permanent (no decay); see PutVertexParam (#523).
		m.TTL = 0
		return m, nil
	}
	if m.TTL, err = Duration(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

// AddDecayingEdgeParam parses
// `add decaying-edge <tail> <head> <initial_weight> <ratio> <steps> <interval_seconds>`.
// All six positional arguments are required (unlike AddEdgeParam's optional
// trailing ttl_seconds) because the decay curve is meaningless without every
// parameter. Numeric-range checks are the SDK's job — this only enforces
// shape and token types, then EOF so a stray trailing token is a usage error
// (mirrors AddEdgeParam's #932 trailing-token guard).
func AddDecayingEdgeParam(s *Source) (*AddDecayingEdge, error) {
	var err error
	m := &AddDecayingEdge{}
	if m.Tail, err = String(s); err != nil {
		return nil, err
	}
	if m.Head, err = String(s); err != nil {
		return nil, err
	}
	if m.InitialWeight, err = Float32(s); err != nil {
		return nil, err
	}
	if m.Ratio, err = Float32(s); err != nil {
		return nil, err
	}
	if m.Steps, err = Integer(s); err != nil {
		return nil, err
	}
	if m.Interval, err = Duration(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

// keys. A one-element batch deletes a single vertex; more than one routes
// to DeleteVertices in the dispatcher.
func DeleteVertexParam(s *Source) (*DeleteVertex, error) {
	m := &DeleteVertex{}
	for s.HasNext() {
		key, err := String(s)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, ErrNotFound
		}
		m.Keys = append(m.Keys, key)
	}
	if len(m.Keys) == 0 {
		return nil, ErrNotFound
	}
	return m, nil
}

// DeleteEdgeParam parses `delete edge <tail> <head> [<tail> <head> …]` —
// one or more (tail, head) pairs (an even, non-zero token count). A single
// pair deletes one edge; more than one routes to DeleteEdges.
func DeleteEdgeParam(s *Source) (*DeleteEdge, error) {
	m := &DeleteEdge{}
	for s.HasNext() {
		tail, err := String(s)
		if err != nil {
			return nil, err
		}
		head, err := String(s)
		if err != nil {
			return nil, err
		}
		if tail == "" || head == "" {
			return nil, ErrNotFound
		}
		m.Pairs = append(m.Pairs, EdgePair{Tail: tail, Head: head})
	}
	if len(m.Pairs) == 0 {
		return nil, ErrNotFound
	}
	return m, nil
}

// BfsParam parses the bfs family verb (#975):
//
//	bfs <seed> [step] [fan_out] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]
//
// Only <seed> is required. step and fan_out are optional positional integers
// (defaults 5 and 3) that may also be supplied as step=/fan_out= kwargs; a bare
// integer fills the next positional slot (step then fan_out). The closed-set
// kwargs default to reduction=none, objective=max, weighting=raw, and prefix is
// free-text (empty = no frontier filter, #604). objective steers both the
// per-hop top-k pruning and the reduction direction (#560). Unknown keywords, a
// non-integer step/fan_out, out-of-set closed values, an empty prefix=, or a
// third bare positional are rejected with a descriptive usage hint.
func BfsParam(s *Source) (*Bfs, error) {
	var err error
	m := &Bfs{Step: 5, FanOut: 3, Reduction: "none", Objective: "max", Weighting: "raw"}
	if m.Seed, err = String(s); err != nil {
		return nil, err
	}
	pos := 0
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		if key, value, ok := splitKeyValue(tok); ok {
			// The keyword KEY is always case-insensitive; the closed-set axes
			// fold their VALUE too (#437), while the free-text prefix VALUE is
			// matched against vertex keys verbatim (case-SENSITIVE, #604).
			key = strings.ToLower(key)
			lvalue := strings.ToLower(value)
			switch key {
			case "step":
				n, perr := strconv.Atoi(value)
				if perr != nil {
					return nil, errors.New("bfs: step=" + value + " (want an integer)")
				}
				m.Step = n
			case "fan_out":
				n, perr := strconv.Atoi(value)
				if perr != nil {
					return nil, errors.New("bfs: fan_out=" + value + " (want an integer)")
				}
				m.FanOut = n
			case "reduction":
				if !contains(TraversalReductions, lvalue) {
					return nil, errors.New("bfs: reduction=" + value + " (want none|mst|spt)")
				}
				m.Reduction = lvalue
			case "objective":
				if !contains(TraversalObjectives, lvalue) {
					return nil, errors.New("bfs: objective=" + value + " (want min|max)")
				}
				m.Objective = lvalue
			case "weighting":
				if !contains(TraversalWeightings, lvalue) {
					return nil, errors.New("bfs: weighting=" + value + " (want raw|tfidf|bm25)")
				}
				m.Weighting = lvalue
			case "prefix":
				if value == "" {
					return nil, errors.New("bfs: prefix= requires a non-empty value")
				}
				m.Prefix = value
			default:
				return nil, errors.New("bfs: unknown key " + key + " (want step|fan_out|reduction|objective|weighting|prefix)")
			}
			continue
		}
		switch pos {
		case 0:
			n, perr := strconv.Atoi(tok)
			if perr != nil {
				return nil, errors.New("bfs: step " + tok + " (want an integer)")
			}
			m.Step = n
		case 1:
			n, perr := strconv.Atoi(tok)
			if perr != nil {
				return nil, errors.New("bfs: fan_out " + tok + " (want an integer)")
			}
			m.FanOut = n
		default:
			return nil, errors.New("bfs: unexpected token " + tok)
		}
		pos++
	}
	return m, nil
}

// PagerankParam parses the pagerank family verb (#975):
//
//	pagerank <seed> [top_n] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]
//
// Only <seed> is required. top_n is an optional positional integer (default 10;
// 0 = every positive-mass vertex) that may also be supplied as top_n=. The
// locality knobs restart_prob (α) and epsilon (ε) default to 0, which the server
// resolves to α=0.15 / ε=1e-4. Personalized PageRank returns a relevance star,
// so it has NO reduction or objective knob — passing either is an unknown-key
// error. weighting defaults to raw; prefix is free-text (empty = no filter).
func PagerankParam(s *Source) (*Pagerank, error) {
	var err error
	m := &Pagerank{TopN: 10, Weighting: "raw"}
	if m.Seed, err = String(s); err != nil {
		return nil, err
	}
	pos := 0
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		if key, value, ok := splitKeyValue(tok); ok {
			key = strings.ToLower(key)
			lvalue := strings.ToLower(value)
			switch key {
			case "top_n":
				n, perr := strconv.Atoi(value)
				if perr != nil {
					return nil, errors.New("pagerank: top_n=" + value + " (want an integer)")
				}
				m.TopN = n
			case "restart_prob":
				rp, perr := strconv.ParseFloat(value, 32)
				if perr != nil {
					return nil, errors.New("pagerank: restart_prob=" + value + " (want a float in (0,1))")
				}
				m.RestartProb = float32(rp)
			case "epsilon":
				eps, perr := strconv.ParseFloat(value, 32)
				if perr != nil {
					return nil, errors.New("pagerank: epsilon=" + value + " (want a positive float)")
				}
				m.Epsilon = float32(eps)
			case "weighting":
				if !contains(TraversalWeightings, lvalue) {
					return nil, errors.New("pagerank: weighting=" + value + " (want raw|tfidf|bm25)")
				}
				m.Weighting = lvalue
			case "prefix":
				if value == "" {
					return nil, errors.New("pagerank: prefix= requires a non-empty value")
				}
				m.Prefix = value
			default:
				return nil, errors.New("pagerank: unknown key " + key + " (want top_n|restart_prob|epsilon|weighting|prefix)")
			}
			continue
		}
		switch pos {
		case 0:
			n, perr := strconv.Atoi(tok)
			if perr != nil {
				return nil, errors.New("pagerank: top_n " + tok + " (want an integer)")
			}
			m.TopN = n
		default:
			return nil, errors.New("pagerank: unexpected token " + tok)
		}
		pos++
	}
	return m, nil
}

// CommunityParam parses the community family verb (#975):
//
//	community <seed> [max_size] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]
//
// Only <seed> is required. max_size is an optional positional integer (default
// 0 = the conductance sweep alone decides the size) that may also be supplied as
// max_size=. restart_prob (α) and epsilon (ε) share PPR's semantics and defaults
// (0 → server α=0.15 / ε=1e-4). reduction/objective render an optional tree view
// of the community (default reduction=none, objective=max). weighting defaults
// to raw; prefix is free-text (empty = no filter).
func CommunityParam(s *Source) (*Community, error) {
	var err error
	m := &Community{Reduction: "none", Objective: "max", Weighting: "raw"}
	if m.Seed, err = String(s); err != nil {
		return nil, err
	}
	pos := 0
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		if key, value, ok := splitKeyValue(tok); ok {
			key = strings.ToLower(key)
			lvalue := strings.ToLower(value)
			switch key {
			case "max_size":
				n, perr := strconv.Atoi(value)
				if perr != nil {
					return nil, errors.New("community: max_size=" + value + " (want an integer)")
				}
				m.MaxSize = n
			case "restart_prob":
				rp, perr := strconv.ParseFloat(value, 32)
				if perr != nil {
					return nil, errors.New("community: restart_prob=" + value + " (want a float in (0,1))")
				}
				m.RestartProb = float32(rp)
			case "epsilon":
				eps, perr := strconv.ParseFloat(value, 32)
				if perr != nil {
					return nil, errors.New("community: epsilon=" + value + " (want a positive float)")
				}
				m.Epsilon = float32(eps)
			case "reduction":
				if !contains(TraversalReductions, lvalue) {
					return nil, errors.New("community: reduction=" + value + " (want none|mst|spt)")
				}
				m.Reduction = lvalue
			case "objective":
				if !contains(TraversalObjectives, lvalue) {
					return nil, errors.New("community: objective=" + value + " (want min|max)")
				}
				m.Objective = lvalue
			case "weighting":
				if !contains(TraversalWeightings, lvalue) {
					return nil, errors.New("community: weighting=" + value + " (want raw|tfidf|bm25)")
				}
				m.Weighting = lvalue
			case "prefix":
				if value == "" {
					return nil, errors.New("community: prefix= requires a non-empty value")
				}
				m.Prefix = value
			default:
				return nil, errors.New("community: unknown key " + key + " (want max_size|restart_prob|epsilon|reduction|objective|weighting|prefix)")
			}
			continue
		}
		switch pos {
		case 0:
			n, perr := strconv.Atoi(tok)
			if perr != nil {
				return nil, errors.New("community: max_size " + tok + " (want an integer)")
			}
			m.MaxSize = n
		default:
			return nil, errors.New("community: unexpected token " + tok)
		}
		pos++
	}
	return m, nil
}

func splitKeyValue(tok string) (key, value string, ok bool) {
	for i := 0; i < len(tok); i++ {
		if tok[i] == '=' {
			return tok[:i], tok[i+1:], true
		}
	}
	return "", "", false
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// ScanVerticesParam parses `scan vertices <prefix> [limit]`. The objective
// token ("vertices") has already been consumed by the caller; this
// function only reads the prefix and optional limit. An empty prefix is
// rejected because an unbounded vertex scan from the REPL is
// almost never what the operator meant.
func ScanVerticesParam(s *Source) (*ScanVertices, error) {
	prefix, err := String(s)
	if err != nil {
		return nil, err
	}
	m := &ScanVertices{Prefix: prefix}
	limitSet := false
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		key, value, isKwarg := strings.Cut(tok, "=")
		if !isKwarg {
			if limitSet {
				return nil, fmt.Errorf("scan vertices: unexpected token %q", tok)
			}
			n, perr := strconv.Atoi(tok)
			if perr != nil {
				return nil, perr
			}
			m.Limit = n
			limitSet = true
			continue
		}
		switch strings.ToLower(key) {
		case "all":
			b, perr := strconv.ParseBool(value)
			if perr != nil {
				return nil, fmt.Errorf("scan vertices: all must be true or false")
			}
			m.All = b
		default:
			return nil, fmt.Errorf("scan vertices: unknown keyword %q", key)
		}
	}
	return m, nil
}

// ScanEdgesParam parses `scan edges <tail-prefix> [limit]`. The objective
// token ("edges") has already been consumed by the caller. An empty
// tail-prefix is permitted (scans every tail), matching the server's
// ScanEdges semantics where both prefixes default to empty.
func ScanEdgesParam(s *Source) (*ScanEdges, error) {
	tailPrefix, err := String(s)
	if err != nil {
		return nil, err
	}
	m := &ScanEdges{TailPrefix: tailPrefix}
	limitSet := false
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		key, value, isKwarg := strings.Cut(tok, "=")
		if !isKwarg {
			if limitSet {
				return nil, fmt.Errorf("scan edges: unexpected token %q", tok)
			}
			n, perr := strconv.Atoi(tok)
			if perr != nil {
				return nil, perr
			}
			m.Limit = n
			limitSet = true
			continue
		}
		switch strings.ToLower(key) {
		case "head":
			if value == "" {
				return nil, fmt.Errorf("scan edges: empty head prefix")
			}
			m.HeadPrefix = value
		case "all":
			b, perr := strconv.ParseBool(value)
			if perr != nil {
				return nil, fmt.Errorf("scan edges: all must be true or false")
			}
			m.All = b
		default:
			return nil, fmt.Errorf("scan edges: unknown keyword %q", key)
		}
	}
	return m, nil
}

// KeysParam parses `keys <prefix> [limit]` — the Redis-familiar key lister.
// Lantern is a prefix-indexed store, so the argument is a key PREFIX (not a
// glob): `keys user:` lists every vertex key under "user:". A prefix is
// required; the optional trailing limit caps the page (0 = server default),
// mirroring ScanVerticesParam.
func KeysParam(s *Source) (*Keys, error) {
	var err error
	m := &Keys{}
	if m.Prefix, err = String(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		return m, nil
	}
	if m.Limit, err = Integer(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return m, nil
}

// CountVerticesParam parses `count vertices <prefix>` — exactly one prefix
// (migrated from the `vertex count` subcommand). The objective token
// ("vertices") has already been consumed by the caller.
func CountVerticesParam(s *Source) (*CountVertices, error) {
	prefix, err := String(s)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		return nil, ErrNotFound
	}
	if err := EOF(s); err != nil {
		return nil, err
	}
	return &CountVertices{Prefix: prefix}, nil
}

// DeletePrefixVerticesParam parses `delete-prefix vertices <prefix>
// [limit=<int>] [confirm=yes|dry_run=true]`. Exactly one of confirm=yes or
// dry_run=true is REQUIRED — the destructive-op safety gate; a bare
// `delete-prefix vertices p` is a usage error. The objective token
// ("vertices") has already been consumed by the caller.
func DeletePrefixVerticesParam(s *Source) (*DeletePrefixVertices, error) {
	prefix, err := String(s)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		return nil, ErrNotFound
	}
	m := &DeletePrefixVertices{Prefix: prefix}
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		key, value, ok := strings.Cut(tok, "=")
		if !ok {
			return nil, fmt.Errorf("delete-prefix: expected key=value, got %q", tok)
		}
		switch strings.ToLower(key) {
		case "limit":
			n, perr := strconv.Atoi(value)
			if perr != nil {
				return nil, fmt.Errorf("delete-prefix: limit must be an integer")
			}
			m.Limit = n
		case "confirm":
			if !strings.EqualFold(value, "yes") {
				return nil, fmt.Errorf("delete-prefix: confirm must be 'yes'")
			}
			m.Confirm = true
		case "dry_run":
			b, perr := strconv.ParseBool(value)
			if perr != nil {
				return nil, fmt.Errorf("delete-prefix: dry_run must be true or false")
			}
			m.DryRun = b
		default:
			return nil, fmt.Errorf("delete-prefix: unknown keyword %q", key)
		}
	}
	// Safety gate: require EXACTLY one of confirm=yes / dry_run=true. Both
	// unset (no gate) and both set (ambiguous) are usage errors.
	if m.Confirm == m.DryRun {
		return nil, fmt.Errorf("delete-prefix: requires confirm=yes or dry_run=true")
	}
	return m, nil
}

// HelpText is the per-verb grammar reference printed by the `help` verb
// (#436). Single source of truth for the Go REPL; the TypeScript port
// keeps a byte-equivalent copy at `admin/app/lib/cli/verbs.ts` `HELP_TEXT`,
// and the shared fixture `admin/test/cli-grammar/verbs.json` exercises both
// parsers against the bare `help` verb.
//
// The kwarg enums and defaults below MUST stay in lockstep with
// `IlluminateParam`'s `IlluminateAlgorithms` / `IlluminateReductions` /
// `IlluminateObjectives` / `IlluminateWeightings` sets and the
// corresponding TS parser.
const HelpText = `Lantern CLI grammar:

  get    vertex <key: string>
  get    edge   <tail: string> <head: string>
  put    vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>] [type=auto|string|int|float|bool|datetime|duration|json]
  put    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]
  add    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]
  add    decaying-edge <tail: string> <head: string> <initial_weight: float> <ratio: float> <steps: int> <interval_seconds: int>
  delete vertex <key: string> [<key: string> ...]
  delete edge   <tail: string> <head: string> [<tail: string> <head: string> ...]
  scan   vertices <prefix: string> [<limit: int>] [all=true]
  scan   edges    <tail-prefix: string> [<limit: int>] [head=<prefix>] [all=true]
  count  vertices <prefix: string>
  delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]
  keys   <prefix: string> [<limit: int>]
  bfs        <seed: string> [step: int] [fan_out: int]
             [reduction={none|mst|spt}]  default=none
             [objective={min|max}]       default=max
             [weighting={raw|tfidf|bm25}] default=raw
             [prefix=<string>]           default=all keys
             defaults: step=5 fan_out=3
  pagerank   <seed: string> [top_n: int]
             [restart_prob=<float>]      default=0 (server α=0.15)
             [epsilon=<float>]           default=0 (server ε=1e-4)
             [weighting={raw|tfidf|bm25}] default=raw
             [prefix=<string>]           default=all keys
             defaults: top_n=10
  community  <seed: string> [max_size: int]
             [restart_prob=<float>]      default=0 (server α=0.15)
             [epsilon=<float>]           default=0 (server ε=1e-4)
             [reduction={none|mst|spt}]  default=none
             [objective={min|max}]       default=max
             [weighting={raw|tfidf|bm25}] default=raw
             [prefix=<string>]           default=all keys
             defaults: max_size=0 (sweep decides)
  help
  exit

Quoting: "double" with C-style escapes (\" \\ \n \r \t); 'single' verbatim.
Verb/objective case-insensitive; argument values preserve case.`
