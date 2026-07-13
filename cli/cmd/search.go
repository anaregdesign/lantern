package cmd

import (
	"encoding/base64"
	"encoding/json"
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
	searchCursor      string
	searchAll         bool
	searchProjection  string
)

// parseSearchMode maps the --mode flag onto a client.MatchMode.
func parseSearchMode(s string) (client.MatchMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "server", "default", "unset":
		return client.MatchServerDefault, nil
	case "any":
		return client.MatchAny, nil
	case "all":
		return client.MatchAll, nil
	case "min-should", "minshould", "min_should":
		return client.MatchMinShould, nil
	default:
		return 0, fmt.Errorf("unknown --mode %q (want server|any|all|min-should)", s)
	}
}

func parseSearchProjection(s string) (client.SearchProjection, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "key-score", "key_score", "keyscore":
		return client.SearchProjectionKeyScore, nil
	case "full-vertex", "full_vertex", "fullvertex":
		return client.SearchProjectionFullVertex, nil
	default:
		return 0, fmt.Errorf("unknown --projection %q (want key-score|full-vertex)", s)
	}
}

type searchHitOutput struct {
	Key              string          `json:"key"`
	Score            float64         `json:"score"`
	Vertex           json.RawMessage `json:"vertex,omitempty"`
	ProjectionStatus string          `json:"projection_status"`
}

type searchPageOutput struct {
	Hits                []searchHitOutput `json:"hits"`
	NextCursor          string            `json:"next_cursor"`
	EffectiveLimit      uint32            `json:"effective_limit"`
	Truncated           bool              `json:"truncated"`
	ContinuationLimited bool              `json:"continuation_limited"`
}

func searchProjectionStatusString(status client.SearchHitProjectionStatus) string {
	switch status {
	case client.SearchHitProjectionKeyScore:
		return "key-score"
	case client.SearchHitProjectionSnapshot:
		return "snapshot"
	case client.SearchHitProjectionMissing:
		return "missing"
	case client.SearchHitProjectionReplaced:
		return "replaced"
	default:
		return "unspecified"
	}
}

func searchHitForOutput(hit client.SearchHit) (searchHitOutput, error) {
	out := searchHitOutput{
		Key:              hit.Key,
		Score:            hit.Score,
		ProjectionStatus: searchProjectionStatusString(hit.ProjectionStatus),
	}
	if hit.Vertex != nil {
		vertex, err := client.MarshalVertexJSON(hit.Vertex)
		if err != nil {
			return searchHitOutput{}, fmt.Errorf("marshal projected vertex %q: %w", hit.Key, err)
		}
		out.Vertex = vertex
	}
	return out, nil
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Full-text search vertices by content relevance (BM25)",
	Long: `Search vertices by relevance-ranked full-text over their key and value.

This is the content counterpart to "scan vertices" (a lexicographic key walk):
search by remembered topic words instead of an exact key prefix. One page prints
a JSON object with hits, next_cursor, effective_limit, truncated, and
continuation_limited. --all lazily follows the endpoint-sticky cursor and emits
one hit JSON object per NDJSON line; it never accumulates an unbounded array.
A query that matches nothing succeeds. Requires the server's search index
(LANTERN_SEARCH_ENABLED, on by default).

By default the server chooses how query words combine. Override it with:

	--mode server         defer to LANTERN_SEARCH_DEFAULT_MODE (default)
  --mode all            require every query word (AND)
  --mode min-should --min-should N   require at least N distinct query words
  --phrase              require the words to occur adjacently, in order
  --fuzziness 1|2       also match terms within that edit distance (typos)
  --prefix-terms        also match terms that extend a query word ("lan"→"lantern")

--prefix scopes hits to a key namespace; --limit caps the ranked-hit count
(0 lets the server apply its configured default). --projection full-vertex
includes each hit's exact selection-time value/TTL snapshot. --cursor accepts
the URL-safe base64 next_cursor from a previous page; every other option and
the serving endpoint must remain unchanged.

EXAMPLES
  lantern-cli search "calm concise"
  lantern-cli search "rolling update" --mode all
  lantern-cli search "rolling update" --phrase
  lantern-cli search serach --fuzziness 1
  lantern-cli search lan --prefix-terms
  lantern-cli --address host:6380 search espresso --prefix user. --limit 20
	lantern-cli search espresso --limit 20 --all
	lantern-cli search espresso --projection full-vertex
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, err := parseSearchMode(searchMode)
		if err != nil {
			return err
		}
		projection, err := parseSearchProjection(searchProjection)
		if err != nil {
			return err
		}
		var cursor []byte
		if searchCursor != "" {
			cursor, err = base64.RawURLEncoding.DecodeString(searchCursor)
			if err != nil {
				return fmt.Errorf("--cursor must be URL-safe base64 without padding: %w", err)
			}
		}

		opts := []client.SearchOption{
			client.WithSearchLimit(searchLimit),
			client.WithSearchPrefix(searchPrefix),
			client.WithSearchProjection(projection),
			client.WithSearchCursor(cursor),
		}
		if mode != client.MatchServerDefault {
			opts = append(opts, client.WithMatchMode(mode))
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
		if err := client.ValidateSearchOptions(opts...); err != nil {
			return err
		}

		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetEscapeHTML(false)
		if searchAll {
			for hit, iterErr := range cli.SearchVerticesIter(cmd.Context(), args[0], opts...) {
				if iterErr != nil {
					return actionableSearchError(iterErr)
				}
				out, outputErr := searchHitForOutput(hit)
				if outputErr != nil {
					return outputErr
				}
				if outputErr := encoder.Encode(out); outputErr != nil {
					return fmt.Errorf("write search result: %w", outputErr)
				}
			}
			return nil
		}

		page, err := cli.SearchVerticesPage(cmd.Context(), args[0], opts...)
		if err != nil {
			return actionableSearchError(err)
		}
		hits := make([]searchHitOutput, 0, len(page.Hits))
		for _, hit := range page.Hits {
			out, outputErr := searchHitForOutput(hit)
			if outputErr != nil {
				return outputErr
			}
			hits = append(hits, out)
		}
		if err := encoder.Encode(searchPageOutput{
			Hits:                hits,
			NextCursor:          base64.RawURLEncoding.EncodeToString(page.NextCursor),
			EffectiveLimit:      page.EffectiveLimit,
			Truncated:           page.Truncated,
			ContinuationLimited: page.ContinuationLimited,
		}); err != nil {
			return fmt.Errorf("write search page: %w", err)
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
	case errors.Is(err, client.ErrSearchIndexIncomplete):
		return fmt.Errorf("content search is unavailable: the local index is incomplete and requires a bounded rebuild: %w", err)
	case errors.Is(err, client.ErrSearchCursorStale):
		return fmt.Errorf("search cursor is stale: restart explicitly from page 1 on the same endpoint: %w", err)
	case errors.Is(err, client.ErrSearchCursorInvalid):
		return fmt.Errorf("search cursor is invalid for this query, option set, projection, config, or endpoint: %w", err)
	case errors.Is(err, client.ErrSearchContinuationLimited):
		return fmt.Errorf("search continuation reached the server's bounded session cap; narrow the query or raise the server session limits: %w", err)
	default:
		return err
	}
}

func init() {
	f := searchCmd.Flags()
	f.Uint32Var(&searchLimit, "limit", 0, "maximum hits to return (0 = server default)")
	f.StringVar(&searchPrefix, "prefix", "", "restrict hits to vertices whose key carries this prefix")
	f.StringVar(&searchMode, "mode", "server", "term combination: server|any|all|min-should")
	f.Uint32Var(&searchMinShould, "min-should", 0, "minimum distinct query words a hit must carry (with --mode min-should)")
	f.Uint32Var(&searchFuzziness, "fuzziness", 0, "maximum edit distance for fuzzy term matching (0-2)")
	f.BoolVar(&searchPrefixTerms, "prefix-terms", false, "also match dictionary terms that extend a query word")
	f.BoolVar(&searchPhrase, "phrase", false, "require the query's words to occur adjacently, in order")
	f.StringVar(&searchCursor, "cursor", "", "URL-safe base64 cursor returned by the previous page")
	f.BoolVar(&searchAll, "all", false, "lazily follow all retained pages and emit NDJSON hits")
	f.StringVar(&searchProjection, "projection", "key-score", "hit payload: key-score|full-vertex")
	rootCmd.AddCommand(searchCmd)
}
