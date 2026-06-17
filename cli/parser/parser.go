package parser

import (
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
		"illuminate",
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

	// IlluminateAlgorithms / IlluminateObjectives / IlluminateWeightings
	// are the canonical sets the REPL accepts for the keyword arguments of
	// the modernised illuminate verb (#410). The keyword form replaces the
	// legacy positional grammar (neighbor / spt_* / mst_*) entirely.
	IlluminateAlgorithms = []string{"none", "mst", "spt"}
	IlluminateObjectives = []string{"min", "max"}
	IlluminateWeightings = []string{"raw", "tfidf"}

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
	defer s.Next()
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
	var err error
	m := &PutVertex{}
	if m.Key, err = String(s); err != nil {
		return nil, err
	}
	if m.Value, err = Value(s); err != nil {
		return nil, err
	}
	if err := EOF(s); err == nil {
		// Omitted ttl_seconds ⇒ permanent (no decay). Leaving TTL at its
		// zero value makes cli/service forward a ttl<=0 to the SDK, whose
		// convenience methods send the wire's permanent sentinel (#523).
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

// DeleteVertexParam parses `delete vertex <key> [<key> …]` — one or more
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

// IlluminateParam parses the modernised illuminate grammar (#410, #604):
//
//	illuminate <seed> <step> <k> [algorithm=none|mst|spt] [objective=min|max] [weighting=raw|tfidf] [prefix=<string>]
//
// The keyword arguments may appear in any order and any subset. The three
// closed-set axes default to the strongest-edge behaviour (algorithm=none,
// objective=max, weighting=raw); objective defaults to max so a bare
// illuminate keeps the top-k strongest neighbours and the per-hop
// pruning matches the reduction direction (#560). prefix is free-text and
// defaults to empty (no frontier filter; #604).
// Unknown keyword names, malformed `key=value` tokens, closed-set values
// outside the canonical set above, or an empty prefix= value are rejected
// with a descriptive error so the REPL can surface a usage hint.
func IlluminateParam(s *Source) (*Illuminate, error) {
	var err error
	m := &Illuminate{Algorithm: "none", Objective: "max", Weighting: "raw"}
	if m.Seed, err = String(s); err != nil {
		return nil, err
	}
	if m.Step, err = Integer(s); err != nil {
		return nil, err
	}
	if m.K, err = Integer(s); err != nil {
		return nil, err
	}
	for s.HasNext() {
		tok, err := String(s)
		if err != nil {
			return nil, err
		}
		key, value, ok := splitKeyValue(tok)
		if !ok {
			return nil, errors.New("illuminate: expected key=value token, got " + tok)
		}
		// The keyword KEY is always case-insensitive. The three closed-set
		// axes also fold their VALUE because they only ever take a small
		// fixed enum (#437); the free-text prefix VALUE is matched against
		// vertex keys verbatim, so it stays case-SENSITIVE (#604).
		key = strings.ToLower(key)
		lvalue := strings.ToLower(value)
		switch key {
		case "algorithm":
			if !contains(IlluminateAlgorithms, lvalue) {
				return nil, errors.New("illuminate: algorithm=" + value + " (want none|mst|spt)")
			}
			m.Algorithm = lvalue
		case "objective":
			if !contains(IlluminateObjectives, lvalue) {
				return nil, errors.New("illuminate: objective=" + value + " (want min|max)")
			}
			m.Objective = lvalue
		case "weighting":
			if !contains(IlluminateWeightings, lvalue) {
				return nil, errors.New("illuminate: weighting=" + value + " (want raw|tfidf)")
			}
			m.Weighting = lvalue
		case "prefix":
			if value == "" {
				return nil, errors.New("illuminate: prefix= requires a non-empty value")
			}
			m.Prefix = value
		default:
			return nil, errors.New("illuminate: unknown key " + key + " (want algorithm|objective|weighting|prefix)")
		}
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
// `IlluminateParam`'s `IlluminateAlgorithms` / `IlluminateObjectives` /
// `IlluminateWeightings` sets and the corresponding TS parser.
const HelpText = `Lantern CLI grammar:

  get    vertex <key: string>
  get    edge   <tail: string> <head: string>
  put    vertex <key: string> <value: string|int|float|bool|datetime> [<ttl_seconds: int>]
  put    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]
  add    edge   <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]
  delete vertex <key: string> [<key: string> ...]
  delete edge   <tail: string> <head: string> [<tail: string> <head: string> ...]
  scan   vertices <prefix: string> [<limit: int>] [all=true]
  scan   edges    <tail-prefix: string> [<limit: int>] [head=<prefix>] [all=true]
  count  vertices <prefix: string>
  delete-prefix vertices <prefix: string> [limit=<int>] [confirm=yes|dry_run=true]
  keys   <prefix: string> [<limit: int>]
  illuminate <seed: string> <step: int> <k: int>
             [algorithm={none|mst|spt}]  default=none
             [objective={min|max}]       default=max
             [weighting={raw|tfidf}]     default=raw
             [prefix=<string>]           default=all keys
  help
  exit

Quoting: "double" with C-style escapes (\" \\ \n \r \t); 'single' verbatim.
Verb/objective case-insensitive; argument values preserve case.`
