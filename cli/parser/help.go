package parser

import (
	"fmt"
	"strings"
)

// HelpTopic is the structured source for family-scoped help. The REPL renders
// it directly and Cobra consumes the same rendered text for `bfs --help`,
// `pagerank --help`, and `community --help`, so signatures, defaults, domains,
// meaning, and examples cannot drift between those Go surfaces.
type HelpTopic struct {
	Name      string
	Signature string
	Defaults  []string
	Domains   []string
	Meaning   string
	Examples  []string
}

var helpTopics = []HelpTopic{
	{
		Name:      "bfs",
		Signature: "bfs <seed> [step] [fan_out] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]",
		Defaults:  []string{"step=5", "fan_out=3", "reduction=none", "objective=max", "weighting=raw", "prefix=all keys"},
		Domains:   []string{"reduction: none|mst|spt", "objective: min|max", "weighting: raw|tfidf|bm25"},
		Meaning:   "Greedy per-hop top-k breadth-first walk. objective controls both frontier pruning and any directed-arborescence / shortest-path reduction.",
		Examples:  []string{"bfs alice 2 5", "bfs alice 3 20 reduction=mst objective=min"},
	},
	{
		Name:      "pagerank",
		Signature: "pagerank <seed> [top_n] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]",
		Defaults:  []string{"top_n=10", "restart_prob=0 (server α=0.15)", "epsilon=0 (server ε=1e-4)", "weighting=raw", "prefix=all keys"},
		Domains:   []string{"restart_prob: 0 or (0,1)", "epsilon: 0 or positive", "weighting: raw|tfidf|bm25"},
		Meaning:   "Seed-anchored Personalized PageRank relevance star. It has no reduction or objective knob.",
		Examples:  []string{"pagerank alice", "pagerank alice 15 restart_prob=0.25 epsilon=0.001"},
	},
	{
		Name:      "community",
		Signature: "community <seed> [max_size] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]",
		Defaults:  []string{"max_size=0 (sweep decides)", "restart_prob=0 (server α=0.15)", "epsilon=0 (server ε=1e-4)", "reduction=none", "objective=max", "weighting=raw", "prefix=all keys"},
		Domains:   []string{"max_size: non-negative", "reduction: none|mst|spt", "objective: min|max", "weighting: raw|tfidf|bm25"},
		Meaning:   "PageRank-Nibble conductance community returned as an induced subgraph; an optional reduction renders a rooted directed arborescence or shortest-path tree.",
		Examples:  []string{"community alice", "community alice 20 reduction=mst objective=min"},
	},
}

// HelpText is the overview printed by bare `help`. The TypeScript port keeps a
// byte-equivalent copy at admin/app/lib/cli/verbs.ts; the shared grammar
// fixture verifies both parsers including scoped topics.
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
  help [bfs|pagerank|community]
  exit

Quoting: "double" with C-style escapes (\" \\ \n \r \t); 'single' verbatim.
Verb/objective case-insensitive; argument values preserve case.`

// HelpParam parses the optional family topic. Unknown topics and excess
// arguments are rejected so operators get an actionable candidate list rather
// than silently receiving unrelated global help.
func HelpParam(s *Source) (*Help, error) {
	if !s.HasNext() {
		return &Help{}, nil
	}
	topic, err := String(s)
	if err != nil {
		return nil, err
	}
	if s.HasNext() {
		return nil, fmt.Errorf("help accepts at most one topic (try: %s)", strings.Join(HelpTopicNames(), ", "))
	}
	topic = strings.ToLower(topic)
	if _, ok := findHelpTopic(topic); !ok {
		return nil, fmt.Errorf("unknown help topic %q (try: %s)", topic, strings.Join(HelpTopicNames(), ", "))
	}
	return &Help{Topic: topic}, nil
}

// HelpTopicNames returns the accepted scoped-help topics in display order.
func HelpTopicNames() []string {
	names := make([]string, len(helpTopics))
	for i, topic := range helpTopics {
		names[i] = topic.Name
	}
	return names
}

// HelpTextFor renders the overview for an empty topic or the structured
// family-only reference for a known topic. Callers that parse user input should
// use HelpParam first; the bool accommodates Cobra's static construction.
func HelpTextFor(topic string) (string, bool) {
	if topic == "" {
		return HelpText, true
	}
	helpTopic, ok := findHelpTopic(topic)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s\n\nSignature\n  %s\n\nDefaults\n  %s\n\nDomains\n  %s\n\nMeaning\n  %s\n\nExamples\n  %s",
		helpTopic.Name,
		helpTopic.Signature,
		strings.Join(helpTopic.Defaults, "\n  "),
		strings.Join(helpTopic.Domains, "\n  "),
		helpTopic.Meaning,
		strings.Join(helpTopic.Examples, "\n  "),
	), true
}

func findHelpTopic(name string) (HelpTopic, bool) {
	for _, topic := range helpTopics {
		if topic.Name == name {
			return topic, true
		}
	}
	return HelpTopic{}, false
}
