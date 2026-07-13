package cmd

import (
	"github.com/anaregdesign/lantern/cli/parser"
	"github.com/anaregdesign/lantern/cli/service"
	"github.com/spf13/cobra"
)

var (
	searchLimit       uint32
	searchPrefix      string
	searchMode        string
	searchMinShould   uint32
	searchFuzziness   uint32
	searchPrefixTerms bool
	searchPhrase      bool
	searchCursor      string
	searchAll         bool
	searchProjection  string
	searchFormat      string
)

// searchCommandModel is the Cobra-to-shared-grammar boundary. Request
// construction, validation, paging, and output all live in CLIService, which
// is also what the REPL calls after parsing `search ...` (#1068).
func searchCommandModel(query string) parser.Search {
	return parser.Search{
		Query:       query,
		Limit:       searchLimit,
		Prefix:      searchPrefix,
		Mode:        searchMode,
		MinShould:   searchMinShould,
		Phrase:      searchPhrase,
		Fuzziness:   searchFuzziness,
		PrefixTerms: searchPrefixTerms,
		Cursor:      searchCursor,
		All:         searchAll,
		Projection:  searchProjection,
		Format:      searchFormat,
	}
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Full-text search vertices by content relevance (BM25)",
	Long: `Search vertices by relevance-ranked full-text over their key and value.

This command uses the same Search model, validation, request construction, and
output implementation as "search ..." in the interactive REPL. Admin /cli
accepts the same key=value grammar.

One page defaults to a lossless JSON object containing hits, next_cursor,
effective_limit, truncated, and continuation_limited. --format=ndjson emits one
hit per line. --format=tsv uses a real TSV encoder (fields containing tabs,
newlines, or quotes are quoted); its columns are key, score, projection_status,
and vertex JSON. --all follows the endpoint-sticky cursor without collecting an
unbounded array and defaults to NDJSON. Explicit --all --format=json is refused
so cancellation can never leave a partial JSON document. Completed NDJSON/TSV
records may remain visible if a later page is cancelled or fails.

By default the server chooses how query words combine. Override it with:

  --mode server         defer to LANTERN_SEARCH_DEFAULT_MODE (default)
  --mode all            require every query word (AND)
  --mode min-should --min-should N   require at least N distinct query words
  --phrase              require the words to occur adjacently, in order
  --fuzziness 1|2       also match terms within that edit distance (typos)
  --prefix-terms        also match terms that extend a query word ("lan"→"lantern")

--prefix scopes hits to a key namespace; --limit caps each page (0 lets the
server apply its configured default). --projection full-vertex includes each
hit's exact selection-time value/TTL snapshot. --cursor accepts the unpadded
URL-safe base64 next_cursor from a previous page; every other option and the
serving endpoint must remain unchanged.

CANONICAL CONTRACT
  https://github.com/anaregdesign/lantern/blob/main/docs/search.md

REPL EQUIVALENT
  search "rolling update" mode=all limit=20 format=json
  search espresso limit=20 all=true

EXAMPLES
  lantern-cli search "calm concise"
  lantern-cli search "rolling update" --mode all
  lantern-cli search "rolling update" --phrase
  lantern-cli search serach --fuzziness 1
  lantern-cli search lan --prefix-terms
  lantern-cli --address host:6380 search espresso --prefix user. --limit 20
  lantern-cli search espresso --limit 20 --all
  lantern-cli search espresso --projection full-vertex
  lantern-cli search espresso --format tsv
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		model := searchCommandModel(args[0])
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()
		return service.NewCLIService(cli, service.WithOutput(cmd.OutOrStdout())).RunSearch(cmd.Context(), model)
	},
}

func init() {
	f := searchCmd.Flags()
	f.Uint32Var(&searchLimit, "limit", 0, "maximum hits per page (0 = server default)")
	f.StringVar(&searchPrefix, "prefix", "", "restrict hits to vertices whose key carries this prefix")
	f.StringVar(&searchMode, "mode", "server", "term combination: server|any|all|min-should")
	f.Uint32Var(&searchMinShould, "min-should", 0, "minimum distinct query words a hit must carry (with --mode min-should)")
	f.Uint32Var(&searchFuzziness, "fuzziness", 0, "maximum edit distance for fuzzy term matching (0-2)")
	f.BoolVar(&searchPrefixTerms, "prefix-terms", false, "also match dictionary terms that extend a query word")
	f.BoolVar(&searchPhrase, "phrase", false, "require the query's words to occur adjacently, in order")
	f.StringVar(&searchCursor, "cursor", "", "unpadded URL-safe base64 cursor returned by the previous page")
	f.BoolVar(&searchAll, "all", false, "lazily follow all retained pages (default output: NDJSON)")
	f.StringVar(&searchProjection, "projection", "key-score", "hit payload: key-score|full-vertex")
	f.StringVar(&searchFormat, "format", "", "output: json|ndjson|tsv (default json; --all defaults ndjson)")
	rootCmd.AddCommand(searchCmd)
}
