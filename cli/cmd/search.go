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
	Long: scopedHelpText("search") + `

CANONICAL CONTRACT
  https://github.com/anaregdesign/lantern/blob/main/docs/search.md
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
	f.Uint32Var(&searchLimit, "limit", 0, "maximum hits per page (0 = endpoint default, capped by endpoint max)")
	f.StringVar(&searchPrefix, "prefix", "", "restrict candidates to vertex keys in this namespace before ranking")
	f.StringVar(&searchMode, "mode", "server", "query-word membership: server|any|all|min-should (server defers to endpoint default)")
	f.Uint32Var(&searchMinShould, "min-should", 0, "minimum distinct query words a hit must carry (with --mode min-should)")
	f.Uint32Var(&searchFuzziness, "fuzziness", 0, "maximum edit distance for fuzzy term matching (0-2)")
	f.BoolVar(&searchPrefixTerms, "prefix-terms", false, "also match dictionary terms that extend a query word")
	f.BoolVar(&searchPhrase, "phrase", false, "require the query's words to occur adjacently, in order")
	f.StringVar(&searchCursor, "cursor", "", "unpadded URL-safe base64 cursor returned by the previous page")
	f.BoolVar(&searchAll, "all", false, "lazily follow the bounded endpoint-sticky cursor session (default output: NDJSON)")
	f.StringVar(&searchProjection, "projection", "key-score", "hit payload: key-score or exact selection-time full-vertex snapshot")
	f.StringVar(&searchFormat, "format", "", "output: json|ndjson|tsv (one page defaults json; --all defaults ndjson)")
	rootCmd.AddCommand(searchCmd)
}
