package cmd

import (
	"errors"
	"fmt"
	"strings"

	client "github.com/anaregdesign/lantern/sdks/go"
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
)

// parseSearchMode maps the --mode flag onto a client.MatchMode.
func parseSearchMode(s string) (client.MatchMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "any":
		return client.MatchAny, nil
	case "all":
		return client.MatchAll, nil
	case "min-should", "minshould", "min_should":
		return client.MatchMinShould, nil
	default:
		return 0, fmt.Errorf("unknown --mode %q (want any|all|min-should)", s)
	}
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Full-text search vertices by content relevance (BM25)",
	Long: `Search vertices by relevance-ranked full-text over their key and value.

This is the content counterpart to "scan vertices" (a lexicographic key walk):
search by remembered topic words instead of an exact key prefix. Hits print one
per line as "<key>\t<score>", most relevant first; a query that matches nothing
prints nothing and exits 0. Requires the server's search index
(LANTERN_SEARCH_ENABLED, on by default).

The default is an OR-union over the query's words. Tune relevance with:

  --mode all            require every query word (AND)
  --mode min-should --min-should N   require at least N distinct query words
  --phrase              require the words to occur adjacently, in order
  --fuzziness 1|2       also match terms within that edit distance (typos)
  --prefix-terms        also match terms that extend a query word ("lan"→"lantern")

--prefix scopes hits to a key namespace; --limit caps the ranked-hit count
(0 lets the server apply its configured default).

EXAMPLES
  lantern-cli search "calm concise"
  lantern-cli search "rolling update" --mode all
  lantern-cli search "rolling update" --phrase
  lantern-cli search serach --fuzziness 1
  lantern-cli search lan --prefix-terms
  lantern-cli --address host:6380 search espresso --prefix user. --limit 20
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, err := parseSearchMode(searchMode)
		if err != nil {
			return err
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		opts := []client.SearchOption{
			client.WithSearchLimit(searchLimit),
			client.WithSearchPrefix(searchPrefix),
			client.WithMatchMode(mode),
		}
		if searchMinShould > 0 {
			opts = append(opts, client.WithMinShouldMatch(searchMinShould))
		}
		if searchFuzziness > 0 {
			opts = append(opts, client.WithFuzziness(searchFuzziness))
		}
		if searchPrefixTerms {
			opts = append(opts, client.WithPrefixTerms())
		}
		if searchPhrase {
			opts = append(opts, client.WithPhrase())
		}

		hits, err := cli.SearchVertices(cmd.Context(), args[0], opts...)
		if err != nil {
			return actionableSearchError(err)
		}
		out := cmd.OutOrStdout()
		for _, h := range hits {
			_, _ = fmt.Fprintf(out, "%s\t%.4f\n", h.Key, h.Score)
		}
		return nil
	},
}

func actionableSearchError(err error) error {
	switch {
	case errors.Is(err, client.ErrSearchPositionsDisabled):
		return fmt.Errorf("phrase search is unavailable: restart the server with LANTERN_SEARCH_POSITIONS=true or omit --phrase: %w", err)
	case errors.Is(err, client.ErrSearchDisabled):
		return fmt.Errorf("content search is unavailable: restart the server with LANTERN_SEARCH_ENABLED=true: %w", err)
	default:
		return err
	}
}

func init() {
	f := searchCmd.Flags()
	f.Uint32Var(&searchLimit, "limit", 0, "maximum hits to return (0 = server default)")
	f.StringVar(&searchPrefix, "prefix", "", "restrict hits to vertices whose key carries this prefix")
	f.StringVar(&searchMode, "mode", "any", "term combination: any|all|min-should")
	f.Uint32Var(&searchMinShould, "min-should", 0, "minimum distinct query words a hit must carry (with --mode min-should)")
	f.Uint32Var(&searchFuzziness, "fuzziness", 0, "maximum edit distance for fuzzy term matching (0-2)")
	f.BoolVar(&searchPrefixTerms, "prefix-terms", false, "also match dictionary terms that extend a query word")
	f.BoolVar(&searchPhrase, "phrase", false, "require the query's words to occur adjacently, in order")
	rootCmd.AddCommand(searchCmd)
}
